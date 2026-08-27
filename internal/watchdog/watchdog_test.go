package watchdog

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fakeGetenv(values map[string]string) func(string) string {
	return func(key string) string {
		return values[key]
	}
}

func TestLoadConfig(t *testing.T) {
	base := map[string]string{
		"PROBE_URL":            "http://example.invalid/-/healthy",
		"UPTIME_WEBHOOK_URL":   "http://example.invalid/uptime",
		"DOWNTIME_WEBHOOK_URL": "http://example.invalid/downtime",
	}

	withOverride := func(key, value string) map[string]string {
		m := make(map[string]string, len(base)+1)
		for k, v := range base {
			m[k] = v
		}
		m[key] = value
		return m
	}

	withoutKey := func(key string) map[string]string {
		m := make(map[string]string, len(base))
		for k, v := range base {
			if k != key {
				m[k] = v
			}
		}
		return m
	}

	cases := []struct {
		name    string
		env     map[string]string
		wantErr bool
		check   func(t *testing.T, cfg Config)
	}{
		{
			name: "valid config uses defaults",
			env:  base,
			check: func(t *testing.T, cfg Config) {
				if cfg.FailureThreshold != 2 {
					t.Errorf("default FailureThreshold = %d, want 2", cfg.FailureThreshold)
				}
				if cfg.StateFile != "/var/lib/pi-health/state" {
					t.Errorf("default StateFile = %q", cfg.StateFile)
				}
				if cfg.MetricsFile != "/var/lib/node_exporter/textfile_collector/pi_health.prom" {
					t.Errorf("default MetricsFile = %q", cfg.MetricsFile)
				}
			},
		},
		{name: "missing PROBE_URL", env: withoutKey("PROBE_URL"), wantErr: true},
		{name: "missing UPTIME_WEBHOOK_URL", env: withoutKey("UPTIME_WEBHOOK_URL"), wantErr: true},
		{name: "missing DOWNTIME_WEBHOOK_URL", env: withoutKey("DOWNTIME_WEBHOOK_URL"), wantErr: true},
		{name: "non-numeric FAILURE_THRESHOLD", env: withOverride("FAILURE_THRESHOLD", "nope"), wantErr: true},
		{name: "zero FAILURE_THRESHOLD", env: withOverride("FAILURE_THRESHOLD", "0"), wantErr: true},
		{
			name: "valid FAILURE_THRESHOLD override",
			env:  withOverride("FAILURE_THRESHOLD", "5"),
			check: func(t *testing.T, cfg Config) {
				if cfg.FailureThreshold != 5 {
					t.Errorf("FailureThreshold = %d, want 5", cfg.FailureThreshold)
				}
			},
		},
		{name: "non-numeric HTTP_TIMEOUT_SECONDS", env: withOverride("HTTP_TIMEOUT_SECONDS", "nope"), wantErr: true},
		{name: "zero HTTP_TIMEOUT_SECONDS", env: withOverride("HTTP_TIMEOUT_SECONDS", "0"), wantErr: true},
		{
			name: "valid HTTP_TIMEOUT_SECONDS override",
			env:  withOverride("HTTP_TIMEOUT_SECONDS", "10"),
			check: func(t *testing.T, cfg Config) {
				if cfg.HTTPTimeout.Seconds() != 10 {
					t.Errorf("HTTPTimeout = %v, want 10s", cfg.HTTPTimeout)
				}
			},
		},
		{
			name: "STATE_FILE override",
			env:  withOverride("STATE_FILE", "/tmp/custom-state"),
			check: func(t *testing.T, cfg Config) {
				if cfg.StateFile != "/tmp/custom-state" {
					t.Errorf("StateFile = %q", cfg.StateFile)
				}
			},
		},
		{
			name: "METRICS_FILE override",
			env:  withOverride("METRICS_FILE", "/tmp/custom-metrics"),
			check: func(t *testing.T, cfg Config) {
				if cfg.MetricsFile != "/tmp/custom-metrics" {
					t.Errorf("MetricsFile = %q", cfg.MetricsFile)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := LoadConfig(fakeGetenv(tc.env))
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected an error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.check != nil {
				tc.check(t, cfg)
			}
		})
	}
}

// counterServer returns an httptest.Server that counts how many requests
// it receives and responds with the given status code.
func counterServer(status int) (*httptest.Server, *int) {
	count := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count++
		w.WriteHeader(status)
	}))
	return srv, &count
}

func newTestConfig(t *testing.T, probeStatus int) (Config, *int, *int, func()) {
	t.Helper()

	probeSrv, _ := counterServer(probeStatus)
	uptimeSrv, uptimeHits := counterServer(http.StatusOK)
	downtimeSrv, downtimeHits := counterServer(http.StatusOK)

	dir := t.TempDir()
	cfg := Config{
		ProbeURL:         probeSrv.URL,
		UptimeWebhook:    uptimeSrv.URL,
		DowntimeWebhook:  downtimeSrv.URL,
		FailureThreshold: 2,
		HTTPTimeout:      2 * 1e9, // 2s, avoids importing "time" just for this
		StateFile:        filepath.Join(dir, "state"),
		MetricsFile:      filepath.Join(dir, "metrics.prom"),
	}

	cleanup := func() {
		probeSrv.Close()
		uptimeSrv.Close()
		downtimeSrv.Close()
	}

	return cfg, uptimeHits, downtimeHits, cleanup
}

func seedState(t *testing.T, path string, value string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(value), 0644); err != nil {
		t.Fatalf("seeding state file: %v", err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(data)
}

func TestRun_Success(t *testing.T) {
	cfg, uptimeHits, downtimeHits, cleanup := newTestConfig(t, http.StatusOK)
	defer cleanup()

	var stderr bytes.Buffer
	failures := Run(cfg, &stderr)

	if failures != 0 {
		t.Errorf("failures = %d, want 0", failures)
	}
	if *uptimeHits != 1 {
		t.Errorf("uptime webhook hits = %d, want 1", *uptimeHits)
	}
	if *downtimeHits != 0 {
		t.Errorf("downtime webhook hits = %d, want 0", *downtimeHits)
	}
	if got := strings.TrimSpace(readFile(t, cfg.StateFile)); got != "0" {
		t.Errorf("state file = %q, want %q", got, "0")
	}
	if metrics := readFile(t, cfg.MetricsFile); !strings.Contains(metrics, "pi_health_build_info") {
		t.Errorf("metrics file missing pi_health_build_info: %q", metrics)
	}
}

func TestRun_FailureBelowThreshold(t *testing.T) {
	cfg, _, downtimeHits, cleanup := newTestConfig(t, http.StatusInternalServerError)
	defer cleanup()

	var stderr bytes.Buffer
	failures := Run(cfg, &stderr)

	if failures != 1 {
		t.Errorf("failures = %d, want 1", failures)
	}
	if *downtimeHits != 0 {
		t.Errorf("downtime webhook hits = %d, want 0 (below threshold)", *downtimeHits)
	}
}

func TestRun_FailureAtThreshold(t *testing.T) {
	cfg, _, downtimeHits, cleanup := newTestConfig(t, http.StatusInternalServerError)
	defer cleanup()
	seedState(t, cfg.StateFile, "1")

	var stderr bytes.Buffer
	failures := Run(cfg, &stderr)

	if failures != cfg.FailureThreshold {
		t.Errorf("failures = %d, want %d", failures, cfg.FailureThreshold)
	}
	if *downtimeHits != 1 {
		t.Errorf("downtime webhook hits = %d, want 1 (at threshold)", *downtimeHits)
	}
}

func TestRun_FailurePastThreshold(t *testing.T) {
	cfg, _, downtimeHits, cleanup := newTestConfig(t, http.StatusInternalServerError)
	defer cleanup()
	seedState(t, cfg.StateFile, "2") // already at threshold from a previous run

	var stderr bytes.Buffer
	failures := Run(cfg, &stderr)

	if failures != 3 {
		t.Errorf("failures = %d, want 3", failures)
	}
	if *downtimeHits != 0 {
		t.Errorf("downtime webhook hits = %d, want 0 (already alerted at threshold)", *downtimeHits)
	}
}

func TestRun_Recovery(t *testing.T) {
	cfg, uptimeHits, _, cleanup := newTestConfig(t, http.StatusOK)
	defer cleanup()
	seedState(t, cfg.StateFile, "3") // was failing

	var stderr bytes.Buffer
	failures := Run(cfg, &stderr)

	if failures != 0 {
		t.Errorf("failures = %d, want 0", failures)
	}
	if *uptimeHits != 1 {
		t.Errorf("uptime webhook hits = %d, want 1", *uptimeHits)
	}
	if got := strings.TrimSpace(readFile(t, cfg.StateFile)); got != "0" {
		t.Errorf("state file = %q, want %q", got, "0")
	}
}

func TestRun_ProbeConnectionError(t *testing.T) {
	// A closed server's URL reliably produces a transport-level error
	// (connection refused), exercising probe()'s client.Get err != nil
	// branch specifically, as opposed to a non-200 status.
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	deadURL := dead.URL
	dead.Close()

	_, uptimeHits, downtimeHits, cleanup := newTestConfig(t, http.StatusOK)
	defer cleanup()
	dir := t.TempDir()
	cfg := Config{
		ProbeURL:         deadURL,
		UptimeWebhook:    "http://example.invalid/unused",
		DowntimeWebhook:  "http://example.invalid/unused",
		FailureThreshold: 2,
		HTTPTimeout:      2 * 1e9,
		StateFile:        filepath.Join(dir, "state"),
		MetricsFile:      filepath.Join(dir, "metrics.prom"),
	}

	var stderr bytes.Buffer
	failures := Run(cfg, &stderr)

	if failures != 1 {
		t.Errorf("failures = %d, want 1", failures)
	}
	_ = uptimeHits
	_ = downtimeHits
}

func TestRun_MetricsWriteFailureIsLoggedNotFatal(t *testing.T) {
	cfg, uptimeHits, _, cleanup := newTestConfig(t, http.StatusOK)
	defer cleanup()
	cfg.MetricsFile = filepath.Join(t.TempDir(), "nonexistent-subdir", "metrics.prom")

	var stderr bytes.Buffer
	failures := Run(cfg, &stderr)

	if failures != 0 {
		t.Errorf("failures = %d, want 0 (metrics failure shouldn't affect the probe result)", failures)
	}
	if *uptimeHits != 1 {
		t.Errorf("uptime webhook hits = %d, want 1", *uptimeHits)
	}
	if !strings.Contains(stderr.String(), "metrics file write failed") {
		t.Errorf("stderr = %q, want a metrics write failure logged", stderr.String())
	}
}

func TestRun_CorruptStateFileTreatedAsZero(t *testing.T) {
	cfg, _, downtimeHits, cleanup := newTestConfig(t, http.StatusInternalServerError)
	defer cleanup()
	seedState(t, cfg.StateFile, "not-a-number")

	var stderr bytes.Buffer
	failures := Run(cfg, &stderr)

	if failures != 1 {
		t.Errorf("failures = %d, want 1 (corrupt state should be treated as starting from 0)", failures)
	}
	if *downtimeHits != 0 {
		t.Errorf("downtime webhook hits = %d, want 0", *downtimeHits)
	}
}

func TestRun_FailurePathStateWriteFailureIsLoggedNotFatal(t *testing.T) {
	cfg, _, _, cleanup := newTestConfig(t, http.StatusInternalServerError)
	defer cleanup()
	cfg.StateFile = filepath.Join(t.TempDir(), "nonexistent-subdir", "state")

	var stderr bytes.Buffer
	failures := Run(cfg, &stderr)

	if failures != 1 {
		t.Errorf("failures = %d, want 1", failures)
	}
	if !strings.Contains(stderr.String(), "state write failed") {
		t.Errorf("stderr = %q, want a state write failure logged", stderr.String())
	}
}

func TestRun_StateWriteFailureIsLoggedNotFatal(t *testing.T) {
	cfg, uptimeHits, _, cleanup := newTestConfig(t, http.StatusOK)
	defer cleanup()
	cfg.StateFile = filepath.Join(t.TempDir(), "nonexistent-subdir", "state")

	var stderr bytes.Buffer
	Run(cfg, &stderr)

	if *uptimeHits != 1 {
		t.Errorf("uptime webhook hits = %d, want 1", *uptimeHits)
	}
	if !strings.Contains(stderr.String(), "state write failed") {
		t.Errorf("stderr = %q, want a state write failure logged", stderr.String())
	}
}

func TestRun_DiscordPostFailureIsLoggedNotFatal(t *testing.T) {
	cfg, _, _, cleanup := newTestConfig(t, http.StatusOK)
	defer cleanup()
	cfg.UptimeWebhook = "http://127.0.0.1:1" // reliably refused

	var stderr bytes.Buffer
	failures := Run(cfg, &stderr)

	if failures != 0 {
		t.Errorf("failures = %d, want 0 (a notify failure shouldn't change the probe result)", failures)
	}
	if !strings.Contains(stderr.String(), "discord post failed") {
		t.Errorf("stderr = %q, want a discord post failure logged", stderr.String())
	}
}

func TestRun_DowntimeDiscordPostFailureIsLoggedNotFatal(t *testing.T) {
	cfg, _, _, cleanup := newTestConfig(t, http.StatusInternalServerError)
	defer cleanup()
	seedState(t, cfg.StateFile, "1") // this run crosses the threshold
	cfg.DowntimeWebhook = "http://127.0.0.1:1"

	var stderr bytes.Buffer
	failures := Run(cfg, &stderr)

	if failures != cfg.FailureThreshold {
		t.Errorf("failures = %d, want %d", failures, cfg.FailureThreshold)
	}
	if !strings.Contains(stderr.String(), "discord post failed") {
		t.Errorf("stderr = %q, want a discord post failure logged", stderr.String())
	}
}
