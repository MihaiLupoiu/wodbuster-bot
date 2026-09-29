# Code review — `feat/wodbuster-library`

Two rounds against the same branch: the library extraction (round 1, 2026-09-27)
and the cutover commit that retired the legacy client (round 2, 2026-09-29).
Round 2 is the current state; round 1 is kept because most of its findings are
still open.

Each finding was verified by reading the code, and the empirical ones by running
it. Findings that turned out to be false positives are recorded at the bottom so
they are not raised again.

---

## Gate status

| check | round 1 | round 2 (current) |
|---|---|---|
| `make lint` | FAIL — unused `testSecuencial` | **pass** |
| `make build` / `make build-wodbook` | pass | **pass** |
| `go vet` | pass | **pass** |
| `gofmt -l .` | 2 files dirty | **clean** |
| `go test -race ./...` | pass | **pass**, no mock drift |
| full suite wall time | **268 s** (real logins vs the live site) | **4.4 s**, hermetic |

Coverage (round 2): `pkg/wodbuster` 68.7% · `race` 75.3% · `cmd/wodbook` 45.8%
· `internal/telegram/usecase` 31.6% · `internal/storage` 27.8% ·
`internal/booking` 14.0% · `internal/telegram` 0% · `browserauth` **0%**.

## What the cutover fixed

- The divergent `internal/wodbuster` chromedp fork is deleted. There is now one
  client, and it is the tested one. (Round-1 critical #4.)
- `cmd/script` is deleted, which also clears the lint failure. (Round-1 #3.)
- The Dockerfile installs chromium, so the image can actually log in.
- Live-credential tests are gone; the suite runs offline in seconds.
- The README's Saturday/Sunday contradiction is settled (Sunday 12:00 Madrid).
- `ErrBusy`: a real production collision was found on 2026-09-27, given a
  sentinel, and fixed by serialising each athlete's booking calls through a
  one-slot channel. `ErrBusy` is correctly excluded from the non-retryable set,
  and reads are deliberately left parallel.

---

## Critical

### 1. A scheduled class is booked once, ever

`BookingAttempt` moves `pending -> active -> success|failed` and nothing ever
returns it to `pending`. The only writer of `"pending"` is `/book`
(`internal/telegram/usecase/manager.go:148`); both storage backends filter on it
(`internal/storage/mongodb.go:123`, `internal/storage/memory.go:98`); the
terminal status is written at `internal/telegram/usecase/scheduler.go:241`.

After the first Sunday, `GetAllPendingBookings` returns empty and the weekly run
logs "nothing to book" forever. A crash mid-run strands attempts in `"active"`
permanently.

Pre-existing, but the cutover hides it: `/rehearse` reads the *recurring* list
(`GetClassBookingSchedules`) while the real run reads the *attempt* list. So the
rehearsal keeps passing while the thing it rehearses has stopped working.

**Fix:** drive `processAllBookings` off the recurring schedules, and treat
`BookingAttempt` as a per-run audit record created at the start of each run.

### 2. `/login` silently deletes every scheduled class

`internal/telegram/usecase/manager.go:111-119` builds a fresh `models.User` with
`ClassBookingSchedules: []models.ClassBookingSchedule{}` and passes it to
`SaveUser`, which is `ReplaceOne` (`internal/storage/mongodb.go:47`).

Sessions are no longer stored, so re-running `/login` is now the primary recovery
path — and it wipes the user's classes and their `CreatedAt`.

**Fix:** read-modify-write; preserve `ClassBookingSchedules` and `CreatedAt`.

### 3. Outcomes are recorded against the wrong booking

`internal/telegram/usecase/scheduler.go:163-173` builds `targets` with a
`continue` that **skips** attempts whose `Day` fails `ParseWeekday`. `outcomes`
comes back 1:1 with `targets` (`race.Chase` preserves goal order;
`internal/booking/booking.go:161` maps 1:1). But `scheduler.go:240` pairs
`outcomes[i]` with `attempts[i]`.

One bad day name shifts every later outcome onto the wrong row: a booked class
recorded as the failed one's result, and the last attempt left in `"active"`
forever. `scheduler.go:177` `bs.track(chatID, attempts[0], ...)` has the same
flaw.

**Fix:** carry the source attempt alongside its target instead of re-indexing,
and assert `len(outcomes) == len(kept)`.

Related inconsistency: a bad **weekday** skips one class, but a bad **hour**
aborts the athlete's whole run (`internal/booking/booking.go:124` returns rather
than continuing). Two policies for one class of config error.

### 4. Every documented `ENCRYPTION_KEY` is an invalid length

`EncryptPassword` requires exactly 16, 24 or 32 bytes
(`internal/utils/crypto.go:21`).

| source | bytes |
|---|---|
| `internal/app/config.go:38` default | **31** |
| `docker-compose.yml` (before this change) | **37** |
| `README.md:125` | **37** |

Pre-existing, but before the cutover a stored session could still carry a run.
Now there is no session, so a bad key means `/login` fails *and* every Sunday run
dies in `DecryptPassword`.

**Fix:** validate the key length in `app.New` and refuse to boot. The compose
file and `.env.example` now ship a valid 32-byte dev key; `config.go` and the
README still need fixing.

### 5. No cap on concurrent Chrome processes

`internal/booking/booking.go:78-88` constructs a fresh `browserauth.New(...)` —
and so a fresh Chrome — on every call. `scheduler.go:126-133` fans out one
goroutine per athlete with no semaphore (no `semaphore`/`errgroup`/`SetLimit`
anywhere outside tests). `/rehearse` and `/test` add more from
`go b.handleUpdate(update)`, bounded only by a per-chat token bucket of 5 burst /
1 per 2 s.

At roughly 150-300 MB each, N athletes means N simultaneous browsers at 11:50.
An OOM there costs every athlete their week, not one.

**Fix:** a buffered-channel semaphore of 2-4 in `Service.authenticate`. The run
has a 10-minute budget, so serialising is nearly free.

---

## Warnings

### Concurrency and lifecycle

- **Data races on scheduler state.** `isRunning` is written by `Start`/`Stop` and
  read unsynchronised by `IsRunning` (`scheduler.go:349`), `GetNextRunTime` and
  `GetScheduleInfo`, all reachable from per-update goroutines
  (`internal/telegram/bot.go:110`). `GetActiveBookings` copies the map but shares
  the live `*BookingContext` pointers, whose `Status` `CancelBooking` mutates
  under the lock while `bot.go:234` reads it without one. `-race` is clean only
  because no test exercises this. Use `atomic.Bool` and return value copies.
- **`track`/`untrack` key on `chatID` alone** (`scheduler.go:302-320`). A
  `/rehearse` during the Sunday run overwrites the real run's entry — losing its
  `Cancel` — and the first `defer untrack` deletes the other's. Neither
  `CancelBooking` nor `Stop()` can then cancel the real run, and its browser
  survives shutdown.
- **Shutdown never waits for a run.** `processAllBookings` runs on
  `context.Background()`; `Stop()` cancels tracked runs and returns immediately;
  `cron.Stop()`'s returned context (which reports in-flight jobs) is discarded.
  SIGTERM mid-run orphans Chrome and its temp profile.
- **`Start()` after `Stop()` registers a duplicate cron entry** — `Stop` clears
  `isRunning` but leaves the entry, so a second `Start` calls `AddFunc` again.
  Latent: there is no restart path today.
- **`Bot.Stop` can panic on a double close** (`bot.go:105`); `App.Stop` and
  `App.shutdown` both call it. Use `sync.Once`, or thread a context through
  `Start(ctx)`.
- **Unbounded unowned goroutines per Telegram update**, all on
  `context.Background()` — `Stop()` returns while `/rehearse` is still driving a
  browser.

### Correctness

- **`Opening.Next` swallows its parse error and returns `now`**
  (`internal/telegram/usecase/opening.go:39-45`). A typo'd `Time` yields
  `cronSpec()` = "right now, this weekday" — exactly the silent failure the
  type's own doc comment says it exists to prevent. Validate at construction.
- **A late cron tick skips the whole week.** `NextOpening` is strictly-after, so
  firing at 12:00:00.000 (or after a restart or clock skew) returns *next*
  Sunday; `WaitUntil` then sleeps until the 20-minute run context dies and every
  athlete gets "context deadline exceeded". Clamp when the computed opening is
  implausibly far out.
- **A restart inside the wake-up window silently misses that week** — a process
  started Sunday 11:52 waits for next Sunday, with no log.
- **`ErrAlreadyPublished` is discarded** (`internal/booking/booking.go:143`) —
  same as round 1, now on the bot path: the run sleeps up to 10 minutes while
  places are taken. The drift override also has no upper bound, so a bogus
  countdown can park the run until the context dies.
- **`ErrBusy`'s match is too broad.** `pkg/wodbuster/errors.go:77` puts it first
  in `classify`'s switch and one trigger phrase is `"vuelve a intentarlo"`, a
  generic Spanish "try again". A message like *"La clase está completa, vuelve a
  intentarlo más tarde"* would classify as `ErrBusy` rather than `ErrClassFull`,
  so the race keeps hammering `Book` instead of switching to the waiting list.
  The other two phrases (`"usando la reserva"`, `"espera que termine"`) are
  specific enough on their own.
- **Duplicate `/book` creates duplicate targets** — `SaveClassBookingSchedule`
  dedupes, but `SaveBookingAttempt` gets a fresh timestamped ID each call.
- **Orphaned users are re-fanned-out weekly** — when `GetUser` misses, attempts
  stay `pending` and spawn a goroutine plus a Chrome every run, forever.
- **Class-type vocabulary does not match the box.** `ValidateClassType` allows
  only `wod, open, strength, cardio, yoga`, but `Schedule.Resolve` does an exact
  case-insensitive name match and the real names include `"Open box"` and
  `"HYROX"` — `scheduler_test.go:139` itself uses `"Open box"`. So
  `/book friday 19:00 open` stores `"Open"`, which can never resolve. The user
  finds out at noon on Sunday.

### Design

- **`runForAthlete` signals failure with a nil slice** instead of returning an
  error, forcing `Rehearse` to invent "check the logs" — logs the user cannot
  read. Return `([]booking.Outcome, error)`.
- **Two divergent `formatOutcome`** (`bot.go:276`, `scheduler.go:276`) render the
  same outcome differently for `/rehearse` and the Sunday run.
- **`Outcome.Status` is stringly typed** across three packages; a rename in
  `race` compiles fine and silently breaks every user-facing message.
  `race.Outcome` is already a typed enum.
- **A new `http.Transport` per run, never closed** (`booking.go:111`).
  `IdleConnTimeout` is 30 minutes and `*Client` has no `Close`, so every run and
  every `/rehearse` leaves idle connections and their goroutines alive. The
  library explicitly supports sharing a transport; the adapter does not.
- **Mocks generated into non-test files** (`mocks.go`, not `mocks_test.go`),
  linking `testify` into the production binary.
- **`App.New` discards `Config.Logger`** and builds a second hardcoded
  `LevelInfo` logger, making `LOGGING_LEVEL` dead config. **`NewConfig` calls
  `log.Fatalf`** despite returning an error.
- **`App.Stop` type-asserts on `*storage.MongoStorage`** to find `Close`; assert
  on `io.Closer`.
- **`booking.New` nil-panics on a nil logger**; both `pkg/wodbuster` and `race`
  defend against this.
- **Dead config**: `Config.WODBusterURL` has zero readers. The three vars
  deployment actually needs (`WODBUSTER_BOX`, `WODBUSTER_TIMEZONE`,
  `WODBUSTER_CHROME_PATH`) were in neither compose nor the README — compose is
  fixed, the README is not.
- **Race tuning hardcoded in the bot** while `cmd/wodbook` externalises it,
  including `Waitlist: true` forced for every athlete with no opt-out.

### Tests

- **`booking.Run` is 0% and structurally untestable** — `authenticate` calls
  `browserauth.New` directly. Add an `authFn` seam on `Service` and drive it
  against `wodbustertest`.
- **`processAllBookings` and `groupByChat` are 0%** — the multi-athlete
  concurrency the README advertises is untested.
- **The critical #3 misalignment has no test**; one invalid plus one valid day is
  the whole case.
- **DST lands on a Sunday**, the opening day. `TestNextOpening` only covers
  September; add 2026-03-29 and 2026-10-25.
- **`manager.go` is 0% across every method**, including `TestUserSession`, whose
  meaning changed in this diff (it now performs a live login).
- **Flaky-by-construction tests**: `TestTwoAthletesBookInParallel` asserts
  `MaxConcurrent == 2` exactly off a 40 ms sleep; two other new tests use bare
  `time.Sleep(50ms)` to sequence goroutines; `scheduler_test.go:112` asserts
  `s[0] == 0xE2`, which matches the check and hourglass emoji as readily as the
  cross. `wodbustertest.AllowConcurrentBookings` is added and used by no test.

---

## Still open from round 1

All 11 idiomatic findings are unchanged:

1. `func min(a, b int) int` shadows the Go 1.21 builtin (`client.go:272`).
2. Three hand-rolled discard sinks where `slog.DiscardHandler` / `io.Discard`
   exist (`client.go:279`, `race/race.go:133`, `browserauth.go:242`).
3. `fmt.Errorf` with no format verbs — now **15** sites, 4 of them new.
4. `race` depends on the concrete `*wodbuster.Client` rather than a narrow
   consumer-side interface, so it cannot be tested without HTTP.
5. `race` ignores its injected `Options.Clock` everywhere but `WaitUntil` and
   `OpensAt` (15 direct `time.Now()` calls).
6. `browserauth` has no typed errors; every failure is an opaque string, so
   `cmd/wodbook` collapses wrong-password, no-Chrome and timeout into one exit
   code.
7. The `"wodbuster: "` sentinel prefix ends up mid-string after wrapping.
8. `Session.Valid()` / `Target.Valid()` return `error`, not `bool`.
9. `NewServerClock` returns an interface, forcing callers to type-assert.
10. `wodbustertest.New` takes `*testing.T` instead of `testing.TB`, and the
    package imports `testing` from non-test code.
11. Zero `t.Parallel()` repo-wide.

Also still open:

- **Password-redaction regex** (`browserauth.go:489`) only matches when `value=`
  follows `type="password"`. Verified empirically: 2 of 3 real attribute
  orderings leak the password into a diagnostics dump. Now `cmd/wodbook`-only,
  since the bot never sets `DiagnosticsDir` — a genuine mitigation.
- **`.gitignore` does not cover `diagnostics/`**, while
  `cmd/wodbook/config.example.json` ships `"diagnosticsDir": "./diagnostics"`.
- **`os.Setenv` leaks secrets into forked Chrome** — worse now: the bot forks
  Chrome per user, so `ENCRYPTION_KEY` and `TELEGRAM_BOT_TOKEN` are inherited and
  visible in `ps e`.
- **`chromedp.Flag("no-sandbox", true)` is unconditional.** Partly mitigated: the
  image now runs as a non-root user (see below).
- **`cmd/wodbook` resolves target dates before waiting**, so a Saturday-evening
  launch books the already-published week. (Round-1 critical #1 — the bot path is
  *not* affected; see false positives.)
- **`ParseTimeOfDay` accepts trailing garbage** — verified: `"07abc:00"` parses
  as 07:00, `"0x10:00"` as 00:00.
- **Cookie dedupe ignores the domain** (`browserauth.go:459`), so an ASP.NET
  cookie set on both the apex and the box subdomain resolves non-deterministically.
- **Race diagnostics are all at Debug**; a `Resolve` failure never logs what
  classes the day actually contained — the one diagnostic that separates "wrong
  class name" from "box changed the schedule".
- **`docs/wodbuster-library-design.md`** still specifies a separate Go module and
  still describes the deleted `cmd/script`. It is linked from nowhere and carries
  no superseded banner.
- **README** still documents the removed session fields, still claims "Only
  essential session cookie is stored", and still points at `cmd/script`.
- **CI lint is still commented out** (`.github/workflows/ci.yaml:19-21`) — and
  `make lint` now passes clean, so re-enabling it is free today and will not be
  once it drifts again.

### Newly found: `.env.dev` is tracked in git

`.env.dev` is committed and holds `TELEGRAM_BOT_TOKEN`, `ENCRYPTION_KEY`,
`TEST_EMAIL` and `TEST_PASSWORD`. `.gitignore` covers `.env` but not `.env.dev`.
The token and key are placeholders; **`TEST_EMAIL` and `TEST_PASSWORD` look like
real values**. The variables are also stale — the tests that read them were
deleted with `internal/wodbuster`.

**Action:** rotate the credentials if real, `git rm --cached .env.dev`, and add
`.env*` to `.gitignore`. Note that removing it from HEAD does not remove it from
history.

---

## Nits

Hand-rolled `itoa` in `opening.go:57` (correct for its inputs, but `strconv.Itoa`
exists) · `NextOpening` is calendar logic living in the HTTP adapter package ·
`RunSettings` duplicates `booking.RunOptions` field for field · `Get`-prefixed
getters on fresh API surface · regexes recompiled per call in
`internal/utils/validation.go:25,85` while the same file just hoisted `validDays`
· mutex declared below the field it guards · `App.Start`/`App.Stop` are dead code
· startup logs "Scheduler is not running" because `GetScheduleInfo` is called
before `Start` · `bs.fail` notifies mid-loop and then `record` notifies again, so
one run can send two contradictory Telegram messages · missing godoc on several
new exported identifiers.

---

## Checked and found clean

Recorded so these are not raised again.

- **`NextWeekday` and Sunday targets — false positive for the bot.** The cron
  fires 10 minutes before the opening on the same weekday, so the
  `delta == 0 -> 7` rule yields exactly the newly published week for every
  target including Sunday. Correctness does quietly depend on `StartBefore` never
  crossing midnight. The round-1 finding still holds for `cmd/wodbook`, which can
  be launched a day early.
- **Alpine needs `nss`/fonts for Chrome — false positive.** Verified by running
  it: `apk add --no-cache chromium ca-certificates` on `alpine:3.22` launches
  headless Chrome successfully (exit 0, DOM dumped). The EGL/GPU messages are
  harmless headless noise.
- **`WODBUSTER_CHROME_PATH=/usr/bin/chromium-browser` is correct.** Verified:
  the path exists in `alpine:3.22` as a symlink chain to
  `chromium-launcher.sh`. The Dockerfile now asserts it at build time.
- **The race layer's concurrency.** `Chase` writes into a pre-sized slice by
  index and `wg.Wait()`s; `chaseOne`/`book` honour both the context and the
  deadline on every loop; `sleep` selects on `ctx.Done()`.
- **The new per-client booking gate** (`client.go:215-222`): acquire-or-context-
  done, released by `defer`, no nesting, reads left ungated, and `ErrBusy`
  deliberately outside the non-retryable set so it is retried.
- **Browser reaping in `browserauth`** — all three chromedp cancels are deferred
  in the right LIFO order; no leak per login.
- **`NextOpening`'s DST handling** — normalises into the location first and steps
  with wall-clock-preserving `AddDate`.
- **`time/tzdata` is embedded** in `cmd/bot`, which the Alpine image needs.
- **Nothing real was lost with the deleted tests** — all five
  `internal/wodbuster/*_test.go` files were `t.Skip`-gated live-site tests that
  asserted almost nothing. The one genuine unit test (constructor validation) is
  replaced in `pkg/wodbuster`.
- **Stale `wodbuster_session_cookie` fields** left in existing Mongo documents
  are harmless; the driver ignores unknown BSON fields.
- **Interface placement in the new code is textbook** — `Storage`, `Booker` and
  `Notifier` are all declared at the consumer, and `internal/booking` leaks zero
  `pkg/wodbuster` types.

---

## Suggested order

1. Critical #1 — the recurring-vs-attempt data model. This is a design decision,
   not a patch, and #3 partly dissolves once the run is driven off the recurring
   schedules.
2. Critical #2 and #4 — both are small and both silently destroy user state.
3. Critical #5 — the Chrome semaphore.
4. The two data races and the `track`/`untrack` collision.
5. Re-enable CI lint while it is passing.
6. Tests: the `authFn` seam for `booking.Run`, the #3 misalignment case, and
   `processAllBookings` concurrency.
7. The round-1 idiomatic list, `browserauth` tests first, since every login now
   goes through it.
