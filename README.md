# pi-health

k3s cluster health checker, designed to run on a Raspberry Pi 1 Model B.

The Pi 1 isn't part of the k3s cluster — ARMv6 isn't a k3s-supported
architecture — but its independence from the cluster hardware makes it a good
place to run a checker that still reports in if the cluster goes down.

This is a lightweight cron-based script that checks the k3s
API server's `/healthz` and a workload endpoint every 5 minutes, and posts to
Discord: a throttled heartbeat (once/hour) to a low-noise channel when
healthy, and an immediate alert to a separate channel on any up↔down
transition, so outages don't get buried among routine heartbeats.

## Install

```bash
ssh pi@raspberrypi.local mkdir -p ~/pi-health
scp install.sh health-check.sh env.example pi@raspberrypi.local:~/pi-health/
ssh pi@raspberrypi.local
cd pi-health && ./install.sh
```

Then edit `/etc/health-check/env` on the Pi with the real control-plane IP,
workload URL, and Discord webhook URLs (see `env.example` for
the fields), and test with:

```bash
sudo -u health-check /usr/local/bin/health-check.sh
```
