# Pending work on `pkg/wodbuster` and `cmd/wodbook`

What is left from the review of the two library patches, against
`docs/wodbuster-api-design_1.md`. The three blockers (cookie jar sharing,
`WithUserAgent`, no CLI tests) are already fixed in `a6c5323`.

Nothing here blocks a real Sunday except items 5 and 6, which decide whether a
goal survives a bad poll at T+0.

---

## 4. `Credentials` and the authenticator contract live in the wrong package

`pkg/wodbuster/browserauth/browserauth.go:125` defines `Credentials`, and there
is no `Authenticator` interface anywhere. The design (§5) puts both in the core.

As it stands, the planned `formauth` would have to import `browserauth` just to
name its own parameter type, and `chain{formauth, browserauth}` has no interface
to chain against. A consumer also cannot express "some authenticator" in a
constructor.

- Move `Credentials` to `pkg/wodbuster`.
- Do **not** add an `Authenticator` interface next to the implementation —
  declare the one-method interface at the call site that needs it
  (`cmd/wodbook`, later the bot's scheduler).

Cheap now, a breaking change for every caller once `formauth` exists.

## 5. `race.Chase` polls once per goal instead of once per date

`pkg/wodbuster/race/race.go:178` starts a goroutine per goal, and each one runs
its own `c.Schedule(ctx, g.Date)` loop (`race.go:196`). Three targets on the
same Monday means three independent `LoadClass` loops at 4/s and three separate
id resolutions of the same response.

Design §3.4 wants a single explorer per date: one `LoadClass`, one version of
the truth, ids fanned out to everyone waiting on that day.

The doc is explicit that at this scale it is a simplification rather than a
necessity, so shipping as is is defensible — but then say so in a comment, so
the next reader does not think it was an oversight. If it does get fixed: group
goals by `Date`, poll once per group, fan the resolved `Schedule` out.

## 6. `chaseOne` gives up forever when the class is not in the day yet

`pkg/wodbuster/race/race.go:211` — on `ErrClassNotFound` the goal returns
immediately and is dead for the rest of the run.

That is the right answer for a typo in the class name and the wrong one at T+0:
if a day's rows appear incrementally, a single unlucky poll can see `Data`
non-empty but not yet contain the 07:00 Wod. One bad millisecond and the goal
never recovers, with the deadline still 89 seconds away.

Keep polling on `ErrClassNotFound` until `GiveUpAfter`, and report the last
error if it never shows up. Retrying costs one request per `PollEvery` and only
happens in the case that is currently unrecoverable.

## 7. `Published` is inferred from `len(Data) > 0`; `TipoNoClases` is dead

`pkg/wodbuster/wire.go:32` decides publication from whether any classes came
back, while `TipoNoClases` (`wire.go:15`) is parsed and never read — the field
the server actually uses to say `"NoCalendar"`.

A published day with no classes (a holiday, a closed box) is therefore
indistinguishable from an unpublished one: `race` keeps polling it until
`GiveUpAfter` and then reports `ErrNeverPublished`, which is a lie.

Use `TipoNoClases` for the decision and keep `len(Data)` as the fallback for
values we have not seen. `wodbustertest` already serves `"NoCalendar"`
(`wodbustertest/server.go`), so the test for this costs nothing.

## 8. The waiting-list error is swallowed

`pkg/wodbuster/race/race.go:267`:

```go
if err := c.JoinWaitlist(ctx, class.ID, class.Date); err == nil {
    ...
}
```

If `JoinWaitlist` fails, its error is dropped and the result reports the
*booking* error instead. A broken waitlist call is then indistinguishable from
a class that was simply full, which is the one diagnosis you would act on.

Join the two with `errors.Join`, or put the waitlist error in `Result.Err` and
keep the booking error as its cause.

---

## Cleanups

- [x] **Lint.** `TimeOfDay.api` was unused and a test had `t.Sub(time.Now())`.
      Both fixed in `a6c5323`; `golangci-lint` is clean on the new packages.

- [ ] **Four hand-rolled copies of things the stdlib has.** `min`
      (`client.go:238`, a builtin since Go 1.21), `discardHandler`
      (`client.go:245`, `slog.DiscardHandler` since Go 1.24), `race.discard`
      (`race.go:115`) and `browserauth.io_discard`
      (`browserauth.go:176`) — the last two are both `io.Discard`. `io_discard`
      is not a Go name either. The module is on `go 1.24.4`, so all four can go.

- [ ] **`wodbustertest`'s package example does not work.**
      `wodbustertest/server.go:13` shows
      `wodbuster.NewClient(srv.Session())` with no
      `wodbuster.WithHTTPClient(srv.HTTPClient())`. The session's box is
      `"fake"`, so as written the example resolves `fake.wodbuster.com` and
      leaves the machine. It is the first thing anyone copies.

- [ ] **`browserauth` logs the athlete's email at `Info`**
      (`browserauth.go:211`). Fine for one person on a VPS, not once the bot
      runs for ten. `Debug`, or log the box only.

- [x] **Nothing builds `wodbook`.** Added `make build-wodbook` and
      `make rehearse` (a `-now -dry -v` run). Still worth adding to CI if the
      CLI is meant to stay the library's proving ground.

- [ ] **`race` tests run on the wall clock** (~4s per run) because
      `Options.Clock` is only consulted in `WaitUntil` and `OpensAt` —
      `Chase`'s deadline is a raw `time.Now()` (`race.go:172`), so a fake clock
      cannot drive them. Route every `time.Now()` in `race` through `o.Clock`
      and the timing tests become instant and deterministic.

- [ ] **Tests use the stdlib, the rest of the repo uses testify.** Not worth a
      rewrite; worth deciding, so the next test file does not have to guess.
      (`cmd/wodbook/config_test.go` follows the repo and uses testify.)

---

## Adjacent, and not part of the patches

- [ ] **The bot and the CLI disagree about when the week opens.** The cron is
      `55 11 * * 6`, Saturday 11:55 (`internal/telegram/usecase/scheduler.go:52`,
      and the root README says the same). `cmd/wodbook/config.example.json` says
      Sunday 12:00, following the design doc, which was written from a real
      observation on a Sunday.

      Both cannot be right, and the wrong one fires on a day when nothing
      publishes — a failure that looks exactly like a bug in the booking code.
      Settle it against the real box before the next opening; `wodbook -now -dry`
      on each candidate day answers it, since an unpublished day comes back
      `NoCalendar`.

- [ ] **`internal/models/user.go:15` still stores a single cookie.**

      ```go
      WODBusterSessionCookie *http.Cookie `bson:"wodbuster_session_cookie,omitempty"`
      ```

      Design §5.1 calls this the one thing that is expensive to fix later,
      because it is in the Mongo schema: a WodBuster login leaves several
      cookies, and keeping only the important-looking one works until it does
      not. The new `wodbuster.Session` already carries the whole jar. The
      collection is empty today, so the change is free; after the first real
      user it is a migration.
