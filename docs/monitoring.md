# What to monitor

A shortlist for `wodbuster-bot`, ordered by what has actually broken. Nothing
here is implemented yet except the command counter; this is the menu to pick
from, not a plan of record.

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

## Tier 2 — the booking itself

The system's purpose. Without these, "it works" is an anecdote.

| Metric | Type | Labels | Why |
|---|---|---|---|
| `wodbuster_bot_booking_outcomes_total` | Counter | `outcome` (booked, already-booked, waitlisted, failed), `class` | The headline number: classes booked per week versus classes targeted. |
| `wodbuster_bot_booking_latency_seconds` | Histogram | — | Time from the opening to the place being taken. On 2026-09-27 this was 497ms, 520ms and 833ms. If it drifts towards seconds, places start being lost to faster athletes. Buckets want to be fine-grained and low: 0.1 to 5s. |
| `wodbuster_bot_booking_attempts` | Histogram | — | How many calls it took. Three classes collided with each other that same day; the histogram is where that shows up as a distribution rather than a log line. |
| `wodbuster_bot_booking_failures_total` | Counter | `reason` (full, quota, not-included, busy, session-expired, api-error, not-published) | The `classify` sentinels, as a metric. "Failed because the class was full" and "failed because the login broke" need opposite reactions, and today both are one red line in a log. |

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
| `wodbuster_bot_commands_total` | Counter | `command` | **Implemented.** |
| `wodbuster_bot_updates_total` | Counter | `result` (ok, error) | `getUpdates` failing means the bot is deaf. We have already seen `unexpected EOF` in the logs with nothing watching it. |
| `wodbuster_bot_messages_sent_total` | Counter | `result` | A booking that succeeds but cannot be reported is, to the athlete, a booking that did not happen. |
| `wodbuster_bot_command_duration_seconds` | Histogram | `command` | `/rehearse` takes seconds and drives a browser; the rest should be instant. |

### Process

| Metric | Type | Labels | Why |
|---|---|---|---|
| `wodbuster_bot_build_info` | Gauge (always 1) | `version`, `revision` | Which release is actually running. Standard, cheap, and answers the first question of any incident. |
| `wodbuster_bot_browser_launches_total` | Counter | `result` | Chromium is the memory in this container, ~400MB per login. OOM kills show up here as failures with nothing in the logs. |
| `wodbuster_bot_athletes` / `_scheduled_classes` / `_pending_bookings` | Gauge | — | Whether the thing has users, and whether the Sunday run has anything to do. A run that books nothing because nothing was scheduled is not a failure, and these tell the two apart. |
| Go runtime + process collectors | — | — | **Implemented** (GC, scheduler, memory, fds). |

---

## Alerts worth having before more metrics

Metrics nobody looks at are a cost. The small set that would have caught every
incident so far:

1. **No successful run in 8 days** — `time() - wodbuster_bot_run_last_success_timestamp_seconds > 691200`.
2. **The run started late** — `wodbuster_bot_run_lateness_seconds > 120`.
3. **A booking failed for a reason that is not "full"** — the quota, session,
   busy and api-error buckets of `booking_failures_total`.
4. **Login failures** — any, over a 1h window.
5. **Mongo commands failing** — `rate(mongo_operations_total{stat="failed"}[5m]) > 0`.
6. **Telegram updates erroring** for more than a few minutes.

Alerts 1 and 2 are the ones that matter most, because they fire when the bot
does nothing — which is the failure mode that looks exactly like success.
