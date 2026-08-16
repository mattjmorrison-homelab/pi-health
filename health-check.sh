#!/bin/bash
# k3s cluster watchdog — checks control plane + workload health, reports to
# Discord. Heartbeat channel on success (throttled), alert channel on
# up<->down transitions only, to avoid burying real outages in noise.
set -uo pipefail

ENV_FILE="/etc/health-check/env"
STATE_DIR="/var/lib/health-check"
STATE_FILE="$STATE_DIR/state"
HEARTBEAT_FILE="$STATE_DIR/last_heartbeat"
HEARTBEAT_INTERVAL=3600

# shellcheck source=/dev/null
source "$ENV_FILE"

mkdir -p "$STATE_DIR"
[[ -f "$STATE_FILE" ]] || echo "up" >"$STATE_FILE"
PREV_STATE=$(cat "$STATE_FILE")

check_url() {
  local url="$1"
  local extra="$2"
  local code
  code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 5 $extra "$url" 2>/dev/null) || code="000"
  [[ "$code" == "200" ]]
}

API_OK=false
WORKLOAD_OK=false
check_url "https://${PI5_IP}:6443/healthz" "-k" && API_OK=true
check_url "${WORKLOAD_URL}" "" && WORKLOAD_OK=true

if $API_OK && $WORKLOAD_OK; then
  CURRENT_STATE="up"
else
  CURRENT_STATE="down"
fi

post_discord() {
  local webhook="$1"
  local message="$2"
  curl -s -H "Content-Type: application/json" \
    -d "{\"content\": \"$message\"}" \
    "$webhook" >/dev/null
}

now=$(date +%s)

if [[ "$CURRENT_STATE" == "up" ]]; then
  if [[ "$PREV_STATE" == "down" ]]; then
    post_discord "$ALERT_WEBHOOK_URL" "✅ k3s cluster recovered — control plane and workload are both healthy again."
    echo "$now" >"$HEARTBEAT_FILE"
  else
    last=0
    [[ -f "$HEARTBEAT_FILE" ]] && last=$(cat "$HEARTBEAT_FILE")
    if ((now - last >= HEARTBEAT_INTERVAL)); then
      post_discord "$HEARTBEAT_WEBHOOK_URL" "💚 k3s cluster healthy (control plane + workload OK)."
      echo "$now" >"$HEARTBEAT_FILE"
    fi
  fi
else
  if [[ "$PREV_STATE" == "up" ]]; then
    detail=""
    $API_OK || detail+="control plane unreachable "
    $WORKLOAD_OK || detail+="workload unreachable"
    post_discord "$ALERT_WEBHOOK_URL" "🚨 k3s cluster DOWN — ${detail}"
  fi
fi

echo "$CURRENT_STATE" >"$STATE_FILE"
