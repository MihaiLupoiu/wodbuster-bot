# pkg/wodbuster

A Go client for WodBuster's class booking, extracted so that the hard part —
talking to WodBuster and winning the rush when a week is published — can be
tested with a binary and a terminal instead of Mongo, Telegram and a cron.

```
pkg/wodbuster/              core, standard library only
pkg/wodbuster/browserauth/  login via headless Chrome (chromedp)
pkg/wodbuster/race/         waiting and polling policy
pkg/wodbuster/wodbustertest/ a fake WodBuster for tests
cmd/wodbook/                the CLI that proves it works
```

## The one thing to know

A class has two identities:

| | Persistent | Ephemeral |
|---|---|---|
| What | `Target` — date, start time, name | `ClassID` |
| Exists | always | only once the week is published |
| Lasts | forever | that week |

WodBuster creates the class rows when the box publishes, which is the same
instant the race starts. So the ids cannot be known in advance, cannot be
extrapolated, and **must never be persisted**. Store `Target`s; resolve them to
ids inside the booking window; use the id immediately; throw it away.

## Under the hood

Three handlers, all GET:

| | |
|---|---|
| A day's classes | `LoadClass.ashx?ticks=<day>&idu=<athlete>` |
| Book | `Calendario_Inscribir.ashx?id=<class>&ticks=<day>&idu=<athlete>&connectionId=` |
| Waiting list | `Calendario_Avisar.ashx?...` |

`ticks` is the unix time of the calendar date's **UTC midnight** — not "the date
as unix". That encoding never leaves the package: callers pass a `Date`.

`LoadClass` also returns `SegundosHastaPublicacion`, the server's own countdown
to publication, surfaced as `Schedule.OpensIn`. It is why the code never has to
guess when the window opens.

## Why the split

**The core imports nothing outside the standard library.** Authentication needs
a browser today; reading a calendar does not. Keeping `browserauth` separate
means a consumer who only wants schedules does not pull chromedp, and the core's
tests do not need Chrome installed.

**Every method is one HTTP request.** No retries, no sleeps, no polling, no
re-authentication. That is policy, and policy needs credentials, storage and
rate limits that belong to the consumer. When a session dies the client returns
`ErrSessionExpired` and stops.

**Errors are typed.** `errors.Is(err, wodbuster.ErrClassFull)` — retry that one.
`ErrQuotaExceeded` and `ErrNotIncluded` — do not, no speed will help. Anything
unrecognised arrives as `*APIError` with WodBuster's own words intact rather
than being misfiled.

## Testing without waiting a week

The real service can only be tested once a week, and a failure costs seven days.
So `wodbustertest` serves the real protocol and can be told to publish late, run
out of places, reject bookings or expire the session:

```go
srv := wodbustertest.New(t)
srv.PublishAfter(400 * time.Millisecond)
srv.AddClass(wodbustertest.Class{Name: "Wod", Start: "07:00", Capacity: 12, Booked: 11})

client, _ := wodbuster.NewClient(srv.Session(), wodbuster.WithHTTPClient(srv.HTTPClient()))
results := race.Chase(ctx, client, goals, race.Options{})
```

That is how losing the race is covered — the path that matters most and that no
Sunday will reliably reproduce.

```bash
go test ./pkg/...
go test -race ./pkg/...
```

## Rehearsing against the real site, without booking

`cmd/wodbook` is the CLI. Two of its flags — `-dry` and `-now` — exist so that
the six days a week when nothing publishes are not wasted:

| Flag | What it changes |
|---|---|
| `-dry` | does everything except send the booking request |
| `-now` | skips the wait for the opening and acts immediately |
| `-v` | debug logging: every poll, every rejection |
| `-config` | path to the config file (default `config.json`) |
| `-env` | env file holding the credentials (default `.env`, skipped if absent) |

`-now -dry` together are the full dress rehearsal: log in with Chrome,
synchronise with the server's clock, read the real published week, resolve the
real class ids, print what it would have booked, and stop one request short.

```bash
make rehearse                              # -now -dry -v, using ./config.json
make rehearse WODBOOK_CONFIG=mine.json

# or directly
make build-wodbook
./build/wodbook -config config.json -now -dry -v
```

Credentials come from `WODBUSTER_EMAIL` and `WODBUSTER_PASSWORD`. A `./.env` is
read when it is there, `-env other.env` names a different one, and an exported
variable beats both — so nothing has to be written into `config.json`:

```env
WODBUSTER_EMAIL=you@example.com
WODBUSTER_PASSWORD=...
```

A file named with `-env` must exist: being told to read a file and silently not
doing it produces a "missing credentials" error that looks like a wrong
password. The default `.env` is allowed to be absent.

It is a real login against the real site: it needs working credentials and a
Chrome or Chromium on the machine. Nothing is written, nothing is cancelled and
no place is taken — the only call that would change anything is the one `-dry`
skips.

Separating the two flags is the point. `-now` alone answers "can it book?" and
`-dry` alone answers "can it wait?", which are different bugs, and discovering
them together at noon on a Sunday is how you lose a week.

### Pick a target that is already published

The one thing that makes a mid-week rehearsal *look* broken. Targets are written
as a recurring weekday, and the run resolves each one to the **next** date
falling on that weekday. Ask for a day the box has not published yet and the
rehearsal does exactly what it is supposed to: polls for `giveUpAfterMs`, never
sees the day, and exits 1 with `race: day never published`.

So for a rehearsal, name a weekday that still falls inside the week already out.
If the box publishes on Sunday at 12:00, that is any day from tomorrow up to the
coming Sunday:

```jsonc
// rehearsing on a Friday: Saturday and Sunday are published, Monday is not
"targets": [{ "weekday": "saturday", "time": "10:00", "class": "Wod" }],
"giveUpAfterMs": 5000   // fail fast; the 90s default is for the real race
```

What a good rehearsal prints:

```
level=INFO msg="dry run: nothing will be booked"
level=INFO msg="logged in" box=firespain took=7.4s
level=INFO msg="clock synced" offset=-612ms
level=INFO msg=target class="Saturday 2026-09-19 10:00 Wod" waitlist=true
level=INFO msg=chasing
level=INFO msg="class resolved" goal="Saturday 2026-09-19 10:00 Wod" id=36125 free=4 capacity=12 state=bookable
level=INFO msg=done class="Saturday 2026-09-19 10:00 Wod" outcome=dry-run id=36125 free=4
```

`class resolved` is the line that matters: it means the login worked, the clock
is synced, the day parsed and the `(time, name)` pair matched a real class id.
Everything after it is the single request `-dry` is holding back.

Exit codes: `0` everything worked, `1` at least one target failed, `2` bad
config, `3` login failed.

### What a rehearsal still does not prove

- **The booking response.** No real `Calendario_Inscribir` reply has been seen,
  so the rejection strings in `classify` remain a guess (below).
- **The countdown.** `-now` skips the wait, so it never exercises
  `SegundosHastaPublicacion` being positive.
- **Losing.** A rehearsal never races anyone. That path is covered against
  `wodbustertest`, not against the real site.

## Not verified yet

The error strings in `classify` (errors.go) were **inferred from the site's
minified JavaScript, not from observed failures**. No real rejection body has
been seen. Unmatched messages degrade to `*APIError` with the text intact, so a
wrong guess is readable rather than silent — but the list needs confirming
against real failures: a full class, an exhausted plan, an expired session.

Also unconfirmed: whether `SegundosHastaPublicacion` is positive before an
opening (only negative values have been observed, after the fact), and whether
class ids shift if the box edits a published week.
