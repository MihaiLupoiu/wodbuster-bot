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

## Not verified yet

The error strings in `classify` (errors.go) were **inferred from the site's
minified JavaScript, not from observed failures**. No real rejection body has
been seen. Unmatched messages degrade to `*APIError` with the text intact, so a
wrong guess is readable rather than silent — but the list needs confirming
against real failures: a full class, an exhausted plan, an expired session.

Also unconfirmed: whether `SegundosHastaPublicacion` is positive before an
opening (only negative values have been observed, after the fact), and whether
class ids shift if the box edits a published week.
