# Grafana

`wodbuster-bot.json` is the dashboard. Import it through *Dashboards → New →
Import*, paste the file, and pick your VictoriaMetrics datasource when prompted.
Every query was validated against VictoriaMetrics' own API before shipping.

It answers one question first — is the bot still working — and only then goes
into detail. The reasoning behind each metric is in
[`docs/monitoring.md`](../../docs/monitoring.md).

## Nothing shows up

Then nothing is scraping the bot. **Recording and alerting rules are not
involved**: rules precompute expressions and raise alerts, and a dashboard needs
neither. It needs a scrape target.

Add a job to the scrape config vmagent (or Prometheus) reads:

```yaml
scrape_configs:
  - job_name: wodbuster-bot
    scrape_interval: 30s
    static_configs:
      - targets: ['wodbuster-bot:8080']
```

The target has to be reachable **from the scraper**:

- **Same Docker host** — which is the usual case, and the tidy one. Put the bot
  on the network vmagent already uses and address it by container name, exactly
  as `cadvisor:8080` is addressed. Nothing needs publishing to the host at all:

  ```yaml
  # in the bot's stack
  services:
    bot:
      networks: [default, monitoring]
  networks:
    monitoring:
      external: true          # whatever network vmagent is on
  ```

- **Another host** — then the port must be published on an address that host can
  reach. `127.0.0.1:8090:8080` publishes on loopback only, so it is unreachable
  from anywhere else; use `8090:8080`, and point the scrape target at
  `<host>:8090`.

Confirm with:

```bash
curl -s http://<bot-host>:8090/metrics | grep wodbuster_bot_runs_total
curl -s http://<vmagent>:8429/api/v1/targets | grep wodbuster
```

## What the panels assume

Most of the booking panels are weekly or monthly windows, because the thing
being measured happens once a week. A dashboard set to the last 30 minutes will
look empty even when everything is fine; the saved range is 7 days.

A fresh deployment shows gaps until the first Sunday run: `runs_total`,
`booking_*` and `login_*` have no data until the bot has actually done the work.
