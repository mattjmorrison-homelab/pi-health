#!/bin/bash
# Run on the Pi 1 (as a user with sudo) to install the k3s health watchdog.
# Copies this directory's script + config template into place, creates a
# dedicated non-root user to run it, and registers the cron job.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

if ! id -u health-check >/dev/null 2>&1; then
  sudo useradd -r -s /usr/sbin/nologin health-check
fi

sudo mkdir -p /etc/health-check /var/lib/health-check

if [[ ! -f /etc/health-check/env ]]; then
  sudo cp "$SCRIPT_DIR/env.example" /etc/health-check/env
  echo "Wrote /etc/health-check/env — edit it with real values before the checks will work."
fi
sudo chown health-check:health-check /etc/health-check/env
sudo chmod 600 /etc/health-check/env

sudo chown health-check:health-check /var/lib/health-check

sudo cp "$SCRIPT_DIR/health-check.sh" /usr/local/bin/health-check.sh
sudo chown health-check:health-check /usr/local/bin/health-check.sh
sudo chmod 750 /usr/local/bin/health-check.sh

echo '*/5 * * * * /usr/local/bin/health-check.sh' | sudo crontab -u health-check -

echo "Installed. Edit /etc/health-check/env, then test with:"
echo "  sudo -u health-check /usr/local/bin/health-check.sh"
