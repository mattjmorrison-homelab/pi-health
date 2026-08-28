# pi-health

A watchdog that runs standalone on `pi1.local`, outside the k3s cluster, checking
whether Prometheus is reachable from the homelab's Pi. It exists because
Prometheus can't alert on its own total outage: it's both the thing being
watched and the thing evaluating alert rules, so if Prometheus itself goes
down, nothing fires. `pi-health` is the other half of a mutual watchdog —
Prometheus already watches `pi1` via the `PiNodeExporterDown` alert; this is
`pi1` watching Prometheus back.

## How it works

`pi-health.timer` runs the `pi-health` binary once every 2 minutes (a oneshot
systemd unit, not a long-running daemon). Each run:

1. GETs `PROBE_URL` (Prometheus's `/-/healthy` endpoint). Resolved via `pi1`'s
   normal LAN DNS, so this proves Prometheus is reachable over the LAN, not
   that the homelab is reachable from the public internet.
2. On success: posts a message to `UPTIME_WEBHOOK_URL` (Discord's `#uptime`
   channel) and resets the consecutive-failure counter. This happens on
   *every* successful run, deliberately, with no debouncing — the constant
   stream is the heartbeat, and a gap in it is as informative as an explicit
   alert.
3. On failure: increments a failure counter persisted at
   `/var/lib/pi-health/state`. Once it reaches `FAILURE_THRESHOLD`
   (default 2, so ~4 minutes at the default interval), posts an alert to
   `DOWNTIME_WEBHOOK_URL` (`#downtime`). It fires once at the threshold
   crossing, not on every failed run during a prolonged outage — the
   `#uptime` stream resuming is what signals recovery.

Every run also writes `/var/lib/node_exporter/textfile_collector/pi_health.prom`,
a Prometheus metric (`pi_health_build_info{sha="..."}`) reporting the build SHA
of the binary currently running. node_exporter's textfile collector picks this
up and Prometheus scrapes it like any other metric, which lets CI's post-deploy
step confirm the right build actually made it onto `pi1` — by querying
Prometheus, not by SSHing back into the Pi.

## Configuration

Environment variables, loaded by systemd from `/etc/pi-health/config.env`
(root-only, `600`, never committed — see `config.env.example` for the shape):

| Variable                | Required | Default |
|--------------------------|----------|---------|
| `PROBE_URL`              | yes      | —       |
| `UPTIME_WEBHOOK_URL`     | yes      | —       |
| `DOWNTIME_WEBHOOK_URL`   | yes      | —       |
| `FAILURE_THRESHOLD`      | no       | `2`     |
| `HTTP_TIMEOUT_SECONDS`   | no       | `5`     |
| `STATE_FILE`             | no       | `/var/lib/pi-health/state` |
| `METRICS_FILE`           | no       | `/var/lib/node_exporter/textfile_collector/pi_health.prom` |

## Layout

```
cmd/pi-health/       thin, untested entrypoint (main.go)
internal/watchdog/   all real logic, 100% unit tested
systemd/             pi-health.service + pi-health.timer
deploy.sh            cross-compiles for pi1 and installs over SSH
```

Logic lives in `internal/watchdog` rather than `main.go` because `go test`
never invokes `main()` — keeping it there would put untestable code in the way
of a meaningful coverage number.

## Deploying

CI deploys automatically on merge to `main`: `deploy.sh` cross-compiles for
`linux/arm` (`GOARM=6`) with the build SHA embedded via `-ldflags -X`, copies
the binary to `pi1` over SSH, and installs/enables the systemd units. The
`apply` job then queries Prometheus for `pi_health_build_info{sha="<sha>"}`
to confirm the new build is actually running before the job is considered
successful.

The same script can be run by hand for a first install or a manual
redeploy:

```
./deploy.sh
```

It prompts for the SSH target when `PI_SSH` isn't already set (CI always
sets it, so CI runs are always non-interactive). On first install it seeds
`/etc/pi-health/config.env` with placeholder values on `pi1` — real webhook
URLs are then filled in by hand.

## Development

```
go build ./...
gofmt -l .
go vet ./...
golangci-lint run
go test -coverprofile=coverage.out ./internal/...
go tool cover -func=coverage.out
```

All of the above run in CI on every PR, along with a check that coverage on
`./internal/...` is 100%.
