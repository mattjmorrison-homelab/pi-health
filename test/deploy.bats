#!/usr/bin/env bats

setup() {
  export PATH="$BATS_TEST_DIRNAME/mocks:$PATH"
  export CAPTURE_DIR="$BATS_TEST_TMPDIR"
  DEPLOY="$BATS_TEST_DIRNAME/../deploy.sh"
  unset PI_SSH SSH_KEY_FILE INTERVAL
}

@test "fails with no target given when the prompt is answered with an empty line" {
  run bash -c "printf '\n' | bash '$DEPLOY'"
  [ "$status" -eq 1 ]
  [[ "$output" == *"No target given."* ]]
}

@test "aborts when the confirmation prompt isn't answered y" {
  run bash -c "printf 'pi@testhost\nn\n' | bash '$DEPLOY'"
  [ "$status" -eq 1 ]
  [[ "$output" == *"Aborted."* ]]
}

@test "proceeds through the confirmation prompt when answered y" {
  run bash -c "printf 'pi@testhost\ny\n' | bash '$DEPLOY'"
  [ "$status" -eq 0 ]
  grep -q "pi@testhost" "$CAPTURE_DIR/scp.calls"
}

@test "skips every prompt when PI_SSH is already set" {
  export PI_SSH="pi@testhost"
  run bash "$DEPLOY"
  [ "$status" -eq 0 ]
  [[ "$output" != *"Pi SSH target"* ]]
  [[ "$output" != *"Continue?"* ]]
}

@test "cross-compiles for linux/arm GOARM=6 with CGO disabled" {
  export PI_SSH="pi@testhost"
  run bash "$DEPLOY"
  [ "$status" -eq 0 ]
  grep -qx "GOOS=linux" "$CAPTURE_DIR/go.env"
  grep -qx "GOARCH=arm" "$CAPTURE_DIR/go.env"
  grep -qx "GOARM=6" "$CAPTURE_DIR/go.env"
  grep -qx "CGO_ENABLED=0" "$CAPTURE_DIR/go.env"
}

@test "embeds the current git commit SHA at the correct package path" {
  export PI_SSH="pi@testhost"
  run bash "$DEPLOY"
  [ "$status" -eq 0 ]
  grep -q -- "-X github.com/mattjmorrison-homelab/pi-health/internal/watchdog.BuildSHA=deadbeefcafef00d1234567890abcdef12345678" "$CAPTURE_DIR/go.calls"
}

@test "copies the built binary to PI_SSH over scp" {
  export PI_SSH="pi@testhost"
  run bash "$DEPLOY"
  [ "$status" -eq 0 ]
  grep -q "pi@testhost:/tmp/pi-health" "$CAPTURE_DIR/scp.calls"
}

@test "installs over ssh against PI_SSH" {
  export PI_SSH="pi@testhost"
  run bash "$DEPLOY"
  [ "$status" -eq 0 ]
  grep -q "pi@testhost" "$CAPTURE_DIR/ssh.calls"
}

@test "passes -i SSH_KEY_FILE to scp and ssh when set (CI mode)" {
  export PI_SSH="pi@testhost"
  export SSH_KEY_FILE="/tmp/fake-key"
  run bash "$DEPLOY"
  [ "$status" -eq 0 ]
  grep -q -- "-i /tmp/fake-key" "$CAPTURE_DIR/scp.calls"
  grep -q -- "-i /tmp/fake-key" "$CAPTURE_DIR/ssh.calls"
}

@test "omits -i when SSH_KEY_FILE is unset (manual local run)" {
  export PI_SSH="pi@testhost"
  run bash "$DEPLOY"
  [ "$status" -eq 0 ]
  ! grep -q -- "-i " "$CAPTURE_DIR/scp.calls"
  ! grep -q -- "-i " "$CAPTURE_DIR/ssh.calls"
}

@test "defaults the timer interval to 2min" {
  export PI_SSH="pi@testhost"
  run bash "$DEPLOY"
  [ "$status" -eq 0 ]
  grep -q "INTERVAL='2min'" "$CAPTURE_DIR/ssh.calls"
}

@test "respects a custom INTERVAL" {
  export PI_SSH="pi@testhost"
  export INTERVAL="5min"
  run bash "$DEPLOY"
  [ "$status" -eq 0 ]
  grep -q "INTERVAL='5min'" "$CAPTURE_DIR/ssh.calls"
}
