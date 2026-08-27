#!/usr/bin/env bash
# Builds pi-health for pi1's ARMv6 architecture and installs it there as a
# systemd oneshot service + timer. Run from the operator's machine (needs
# SSH access to the target and a local Go toolchain), or non-interactively
# by CI's apply job (set PI_SSH and SSH_KEY_FILE).
set -euo pipefail

INTERVAL="${INTERVAL:-2min}"

# Non-interactive when PI_SSH is already set (CI always sets it) -- same
# script serves both the automated apply job and a manual local run.
if [[ -z "${PI_SSH:-}" ]]; then
  read -rp "Pi SSH target (e.g. pi@pi1.local): " PI_SSH
  if [[ -z "$PI_SSH" ]]; then
    echo "No target given."
    exit 1
  fi

  echo ""
  echo "Target node:    $PI_SSH"
  echo "Timer interval: $INTERVAL"
  read -rp "Continue? [y/N] " CONFIRM
  if [[ "$CONFIRM" != "y" && "$CONFIRM" != "Y" ]]; then
    echo "Aborted."
    exit 1
  fi
fi

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WORKDIR="$(mktemp -d)"
trap 'rm -rf "$WORKDIR"' EXIT

echo ""
echo "Cross-compiling pi-health for linux/arm (GOARM=6)..."
(
  cd "$SCRIPT_DIR"
  SHA="$(git rev-parse HEAD)"
  GOOS=linux GOARCH=arm GOARM=6 CGO_ENABLED=0 \
    go build -trimpath \
    -ldflags="-s -w -X github.com/mattjmorrison-homelab/pi-health/internal/watchdog.BuildSHA=${SHA}" \
    -o "$WORKDIR/pi-health" ./cmd/pi-health
)

# SSH_KEY_FILE is set by CI (a key fetched from OpenBao); a manual local
# run relies on the operator's own SSH agent/keys instead.
SSH_OPTS=(-o StrictHostKeyChecking=accept-new)
if [[ -n "${SSH_KEY_FILE:-}" ]]; then
  SSH_OPTS+=(-i "$SSH_KEY_FILE")
fi

echo "Copying binary to $PI_SSH..."
scp "${SSH_OPTS[@]}" "$WORKDIR/pi-health" "$PI_SSH:/tmp/pi-health"

echo "Installing on $PI_SSH..."
ssh -t "${SSH_OPTS[@]}" "$PI_SSH" "sudo INTERVAL='${INTERVAL}' bash -s" <<'EOF'
set -euo pipefail

if ! id pi-health >/dev/null 2>&1; then
  useradd --system --no-create-home --shell /usr/sbin/nologin pi-health
fi

if ! getent group textfile-collector >/dev/null; then
  groupadd --system textfile-collector
fi
usermod -a -G textfile-collector pi-health

install -o root -g root -m 755 /tmp/pi-health /usr/local/bin/pi-health
rm -f /tmp/pi-health

mkdir -p /etc/pi-health
if [[ ! -f /etc/pi-health/config.env ]]; then
  echo "No existing config -- creating /etc/pi-health/config.env with placeholders."
  cat >/etc/pi-health/config.env <<'ENVEOF'
PROBE_URL=https://prometheus.morrisons.site/-/healthy
UPTIME_WEBHOOK_URL=https://discord.com/api/webhooks/REPLACE/ME
DOWNTIME_WEBHOOK_URL=https://discord.com/api/webhooks/REPLACE/ME
FAILURE_THRESHOLD=2
HTTP_TIMEOUT_SECONDS=5
ENVEOF
  chown root:root /etc/pi-health/config.env
  chmod 600 /etc/pi-health/config.env
else
  echo "Existing /etc/pi-health/config.env found -- leaving it untouched."
fi

cat >/etc/systemd/system/pi-health.service <<'UNIT'
[Unit]
Description=pi-health homelab watchdog (oneshot)
After=network-online.target
Wants=network-online.target

[Service]
Type=oneshot
User=pi-health
Group=pi-health
EnvironmentFile=/etc/pi-health/config.env
ExecStart=/usr/local/bin/pi-health
StateDirectory=pi-health
ReadWritePaths=/var/lib/node_exporter/textfile_collector
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
UNIT

cat >/etc/systemd/system/pi-health.timer <<UNIT
[Unit]
Description=Run pi-health every ${INTERVAL}

[Timer]
OnBootSec=1min
OnUnitActiveSec=${INTERVAL}
AccuracySec=10s
Unit=pi-health.service

[Install]
WantedBy=timers.target
UNIT

systemctl daemon-reload
systemctl enable --now pi-health.timer
echo "pi-health installed."
systemctl status pi-health.timer --no-pager || true
EOF

echo ""
echo "Done. If this was a first install, edit real secrets, then trigger one run:"
echo "  ssh $PI_SSH sudo \$EDITOR /etc/pi-health/config.env"
echo "  ssh $PI_SSH sudo systemctl start pi-health.service"
echo ""
echo "Watch logs with: ssh $PI_SSH sudo journalctl -u pi-health.service -f"
