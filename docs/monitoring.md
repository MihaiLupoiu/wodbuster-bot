# What to monitor

A shortlist for `wodbuster-bot`, ordered by what has actually broken.
**Everything below is implemented** except where a row says otherwise.

Where each metric lives: the package that records it owns it, registered against
the platform registry — `internal/telegram/metrics.go`,
`internal/telegram/usecase/metrics.go`, `internal/booking/metrics.go`,
`internal/storage/metrics.go`.

The shape follows `buying-engine-service` (explicit registry, `Observe*` methods
on a struct) and, for the Mongo client, `ssp-service`'s
`internal/platform/mongo/monitor.go`, which hooks the driver's own event
monitors rather than wrapping call sites.

A note that applies throughout: **every label value must be bounded**. Class
names, day names and outcomes are fine — they come from a fixed set. Anything
derived from a Telegram message, an athlete id or a server error string is not,
and needs the `(others)` bucketing already used for commands.

---

## Tier 1 — the bot silently did nothing

These are the failures that produce no error at all, which is why they are
first. Each one has already happened.

| Metric | Type | Labels | Why |
|---|---|---|---|
| `wodbuster_bot_run_last_success_timestamp_seconds` | Gauge | — | The single most valuable number here. `time() - metric > 8d` catches a bot that stopped running, whatever the cause: dead container, paused database, crashed cron, a host that never woke up. |
| `wodbuster_bot_runs_total` | Counter | `result` (completed, failed) | Did the weekly run happen at all. |
| `wodbuster_bot_run_lateness_seconds` | Histogram | — | Actual start minus intended start. On 2026-10-04 the cron fired 30 minutes late because the host had been suspended; nothing measured that, and the run was lost. |
| `wodbuster_bot_run_duration_seconds` | Histogram | — | A run that takes 20 minutes is waiting for something it should not be waiting for. |
| `wodbuster_bot_login_check_last_success_timestamp_seconds` | Gauge | — | The daily login check (`LOGIN_CHECK_CRON`, with the first admin's account) last logged in. On 2026-11-15 WodBuster had broken the login two days before the run found out. `time() - metric > 26h` means the login is broken or the check is not running. |
| `wodbuster_bot_login_checks_total` | Counter | `result` (passed, failed, skipped) | The daily check's history. `skipped` is a run in progress, or no active admin to log in as. |

## Tier 2 — the booking itself

The system's purpose. Without these, "it works" is an anecdote.

| Metric | Type | Labels | Why |
|---|---|---|---|
| `wodbuster_bot_booking_outcomes_total` | Counter | `outcome`, `weekday`, `class` | The headline number: classes booked per week versus classes targeted. |
| `wodbuster_bot_booking_latency_seconds` | Histogram | — | Time from the opening to the place being taken. On 2026-09-27 this was 497ms, 520ms and 833ms. If it drifts towards seconds, places start being lost to faster athletes. Buckets want to be fine-grained and low: 0.1 to 5s. |
| `wodbuster_bot_booking_attempts` | Histogram | — | How many calls it took. Three classes collided with each other that same day; the histogram is where that shows up as a distribution rather than a log line. |
| `wodbuster_bot_booking_failures_total` | Counter | `reason` (full, quota, not-included, busy, session-expired, challenge, api-error, not-published) | The `classify` sentinels, as a metric. "Failed because the class was full" and "failed because the login broke" need opposite reactions, and today both are one red line in a log. |

## Tier 3 — WodBuster itself

The dependency most likely to change under us.

| Metric | Type | Labels | Why |
|---|---|---|---|
| `wodbuster_bot_api_requests_total` | Counter | `endpoint` (LoadClass, Inscribir, Avisar, Borrar, reservas), `status` | Already logged per request at debug; the counter is the version you can alert on. |
| `wodbuster_bot_api_duration_seconds` | Histogram | `endpoint` | Server slowness at the opening is the thing we race against. |
| `wodbuster_bot_login_total` | Counter | `result` (ok, rejected, timeout, error) | The login is the slowest and most fragile step: a browser, Cloudflare, a device prompt, an ASP.NET form that has already changed once. |
| `wodbuster_bot_login_duration_seconds` | Histogram | — | 3-15s normally. A jump means Cloudflare is interposing something new. |
| `wodbuster_bot_login_control_drift_total` | Counter | `route` (id, value, label) | Early warning that WodBuster's DOM moved. `browserauth` already falls back from id to value to label and logs a warning; as a counter it is an alert instead of a line nobody reads. This would have caught the 2026-09-18 breakage before the opening. |
| `wodbuster_bot_server_clock_offset_seconds` | Gauge | — | We book against the server's clock. A growing offset means our timing is quietly wrong. |
| `wodbuster_bot_day_unpublished_total` | Counter | `weekday` | Distinguishes "the box has not published yet" from "the box is closed that day" — the ambiguity in `todo.md` item 7 that made Friday 9 October look like a bug. |

## Tier 3b — stored sessions

The browser login has been the cause of every outage so far (2026-10-14,
2026-10-18, 2026-11-15). Stored sessions move it off the critical path, and
these metrics are how we learn whether a stored session can be trusted: the
probe of 2026-11-16 proved 31 hours in use and 16 hours idle, nothing longer.
All of them live in `internal/telegram/usecase/metrics.go`.

| Metric | Type | Labels | Why |
|---|---|---|---|
| `wodbuster_bot_session_checks_total` | Counter | `source` (refresh, preflight), `result` (ok, expired, challenge, inconclusive, unreadable) | Every check of a stored session. `expired` is WodBuster refusing it; `challenge` and `inconclusive` taught us nothing and keep the session. |
| `wodbuster_bot_session_age_at_failure_seconds` | Histogram | `source`, `reason` | Time from the browser login to the refusal: the measured lifetime of a session. Buckets 1h to 45d. |
| `wodbuster_bot_session_idle_at_failure_seconds` | Histogram | `source`, `reason` | Time since the session was last accepted: the idle gap it did not survive. With the refresh every 12h this should stay under 12h; a failure above that means the refresh did not run. |
| `wodbuster_bot_session_max_age_seconds` | Gauge | — | The oldest working stored session, after each refresh. Climbing week after week is the good news: sessions really do last. |
| `wodbuster_bot_session_auth_expiry_seconds` | Gauge | `chat_id` | Seconds until each stored `.WBAuth` expires; absent for a session cookie. Each use pushes it back to 45 days, so it should sit near 45 days. The `chat_id` label breaks the bounded-label rule on purpose: a handful of athletes, and the series is removed with the session. |
| `wodbuster_bot_sessions_stored` | Gauge | — | Active athletes with a stored session, after the last refresh. |
| `wodbuster_bot_session_reuse_total` | Counter | `phase` (shadow, live), `result` | The Sunday answer, one per athlete. Preflight: `would_work`, `would_fail`, `inconclusive`, `missing` for the days-old session. Run: `used` (booked with the stored session) or `fallback` (had to log in). In shadow mode `would_work` versus `would_fail` is the evidence for going live. |
| `wodbuster_bot_preflight_total` | Counter | `result` (all_ok, partial, failed) | Sunday 11:00 preflights. `failed` with every athlete failing is 2026-11-15 again, found 50 minutes earlier. |

Log lines to search for: `session refused` (with `age`, `idle`, `logged_in_at`,
`last_used_at`, `auth_expires_at`, `trusted_device`), `shadow check`,
`preflight done`, `lost session replaced by a new login`, and
`session renewed` at debug. None of them carries a cookie value.

## Tier 4 — infrastructure

### MongoDB client

Model this on `ssp-service`: the driver's `event.PoolMonitor`,
`event.CommandMonitor` and `event.ServerMonitor` feed the metrics, so no call
site changes. Note this repo is on **mongo-driver v1.17**, while `ssp-service`
is on v2 — the monitors exist in both, but `CommandFailedEvent` differs
slightly.

| Metric | Type | Labels | Why |
|---|---|---|---|
| `wodbuster_bot_mongo_connections` | Gauge | `key` (activeConn, serverActiveConn) | Atlas free tier caps connections; a leak shows here first. |
| `wodbuster_bot_mongo_operations_total` | Counter | `cmd`, `stat` (started, succeeded, failed) | A paused Atlas cluster, an expired password or an IP dropped from the access list all look the same from the bot: commands that stop succeeding. The 2026-10-04 outage was exactly this. |
| `wodbuster_bot_mongo_duration_ms` | Histogram | `cmd`, `stat` | Atlas is now a network hop away rather than a container on the same host. |
| `wodbuster_bot_mongo_read_bytes` / `_written_bytes` | Counter | `cmd` | Cheap, and the free tier has quotas. Lower value here than at SSP volumes. |

### Telegram

| Metric | Type | Labels | Why |
|---|---|---|---|
| `wodbuster_bot_commands_total` | Counter | `command` | What the bot is asked to do. |
| `wodbuster_bot_updates_total` + `wodbuster_bot_last_update_timestamp_seconds` | Counter + Gauge | — | A stalled long-poll means the bot is deaf. The library swallows the `getUpdates` error, so the signal is the timestamp going stale rather than an error count. |
| `wodbuster_bot_messages_sent_total` | Counter | `result` | A booking that succeeds but cannot be reported is, to the athlete, a booking that did not happen. |
| `wodbuster_bot_command_duration_seconds` | Histogram | `command` | `/rehearse` takes seconds and drives a browser; the rest should be instant. |

### Process

| Metric | Type | Labels | Why |
|---|---|---|---|
| `wodbuster_bot_build_info` | Gauge (always 1) | `version`, `revision` | Which release is actually running. Standard, cheap, and answers the first question of any incident. |
| `wodbuster_bot_browser_launches_total` | Counter | `result` | **Not implemented separately** — `login_total{result="error"}` already covers a browser that will not start, and a second counter for the same event would double-count. Worth splitting only if OOM kills turn out to be common. |
| `wodbuster_bot_pending_bookings` | Gauge | — | Whether the Sunday run had anything to do. A run that books nothing because nothing was scheduled is not a failure, and this tells the two apart. **`athletes` and `scheduled_classes` are not implemented**: `usecase.Storage` has no count methods, and adding them touches both storage backends and the mocks. |
| Go runtime + process collectors | — | — | GC, scheduler, memory, fds — registered by `platform/metrics`. |
| `wodbuster_bot_build_info` | Gauge | `version`, `go_version` | Set from `APP_VERSION`, which the release workflow bakes into the image. |

---

## Alerts worth having

Metrics nobody looks at are a cost. The small set that would have caught every
incident so far — none of these is wired up yet, since there is no Alertmanager
in this deployment:

1. **No successful run in 8 days** — `time() - wodbuster_bot_run_last_success_timestamp_seconds > 691200`.
2. **The run started late** — `wodbuster_bot_run_lateness_seconds > 120`.
2b. **The daily login check has not passed in 26 hours** —
   `time() - wodbuster_bot_login_check_last_success_timestamp_seconds > 93600`.
   The bot already messages the admins when a check fails; this catches the
   check itself not running.
3. **A booking failed for a reason that is not "full"** — the quota, session,
   busy and api-error buckets of `booking_failures_total`.
4. **Login failures** — any, over a 1h window.
5. **Mongo commands failing** — `rate(mongo_operations_total{stat="failed"}[5m]) > 0`.
6. **Telegram updates erroring** for more than a few minutes.
7. **Stored sessions keep being refused** —
   `increase(wodbuster_bot_session_checks_total{source="refresh",result="expired"}[25h]) >= 2`.
   The bot already messages the admins when a refused session cannot be
   replaced by a login; this catches sessions dying faster than expected even
   when the re-login works. The metric has no athlete label, so it fires for two
   refusals across everyone; the `session refused` log lines say whose.
8. **An active athlete has no session on Saturday evening** —
   `wodbuster_bot_sessions_stored` below the number of active athletes with
   classes, checked Saturday 20:00. The preflight logs everyone in anyway, so
   this is a warning, not a page.
9. **A `.WBAuth` within 7 days of expiry** —
   `wodbuster_bot_session_auth_expiry_seconds < 7 * 86400`. Every refresh
   pushes it back to 45 days, so this means the refresh has stopped.
10. **The Sunday preflight did not finish clean** —
    `increase(wodbuster_bot_preflight_total{result!="all_ok"}[2h]) > 0`. The
    admins get the summary in Telegram too.
11. *(phase 2 only)* **A run fell back to the browser** —
    `increase(wodbuster_bot_session_reuse_total{phase="live",result="fallback"}[1h]) > 0`.

Alerts 1 and 2 are the ones that matter most, because they fire when the bot
does nothing — which is the failure mode that looks exactly like success.
