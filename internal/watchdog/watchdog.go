// Package watchdog implements pi-health's check/notify/report logic. It
// runs standalone on pi1 (outside the k3s cluster), probing Prometheus
// directly. Prometheus can't alert on its own total outage -- it's both
// the thing being watched and the thing evaluating alert rules -- so
// this runs independently, triggered by pi-health.timer, not as a
// long-running daemon. The mirror-image check (does Prometheus see
// pi1?) is homelab-prometheus's own PiNodeExporterDown alert.
package watchdog

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// BuildSHA is set at compile time via -ldflags "-X ...BuildSHA=...", see
// deploy.sh. Reported through Prometheus so CI's post-deploy check can
// confirm the right version made it onto pi1 without needing SSH/LAN
// access to the Pi -- it just queries Prometheus, which already scrapes
// this via node_exporter's textfile collector.
var BuildSHA = "unknown"

// Config holds everything a single check run needs. File paths are part
// of the config (not hardcoded constants) so tests can point them at
// temporary files instead of the real system paths.
type Config struct {
	ProbeURL         string
	UptimeWebhook    string
	DowntimeWebhook  string
	FailureThreshold int
	HTTPTimeout      time.Duration
	StateFile        string
	MetricsFile      string
}

// LoadConfig reads configuration from environment variables via getenv
// (os.Getenv in production, a fake map-backed function in tests).
func LoadConfig(getenv func(string) string) (Config, error) {
	cfg := Config{
		FailureThreshold: 2,
		HTTPTimeout:      5 * time.Second,
		StateFile:        "/var/lib/pi-health/state",
		MetricsFile:      "/var/lib/node_exporter/textfile_collector/pi_health.prom",
	}

	cfg.ProbeURL = getenv("PROBE_URL")
	cfg.UptimeWebhook = getenv("UPTIME_WEBHOOK_URL")
	cfg.DowntimeWebhook = getenv("DOWNTIME_WEBHOOK_URL")
	if cfg.ProbeURL == "" || cfg.UptimeWebhook == "" || cfg.DowntimeWebhook == "" {
		return cfg, fmt.Errorf("PROBE_URL, UPTIME_WEBHOOK_URL, and DOWNTIME_WEBHOOK_URL are required")
	}

	if v := getenv("FAILURE_THRESHOLD"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return cfg, fmt.Errorf("invalid FAILURE_THRESHOLD: %q", v)
		}
		cfg.FailureThreshold = n
	}

	if v := getenv("HTTP_TIMEOUT_SECONDS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return cfg, fmt.Errorf("invalid HTTP_TIMEOUT_SECONDS: %q", v)
		}
		cfg.HTTPTimeout = time.Duration(n) * time.Second
	}

	if v := getenv("STATE_FILE"); v != "" {
		cfg.StateFile = v
	}
	if v := getenv("METRICS_FILE"); v != "" {
		cfg.MetricsFile = v
	}

	return cfg, nil
}

func probe(client *http.Client, url string) error {
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	return nil
}

type discordMessage struct {
	Content string `json:"content"`
}

func postDiscord(client *http.Client, webhookURL, text string) error {
	// discordMessage's only field is a plain string, which json.Marshal
	// cannot fail to encode -- no error path to handle here.
	body, _ := json.Marshal(discordMessage{Content: text})
	resp, err := client.Post(webhookURL, "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	return nil
}

func readFailureCount(stateFile string) int {
	data, err := os.ReadFile(stateFile)
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0
	}
	return n
}

func writeFailureCount(stateFile string, n int) error {
	return os.WriteFile(stateFile, []byte(strconv.Itoa(n)), 0644)
}

func writeMetrics(metricsFile, sha string) error {
	content := fmt.Sprintf(
		"# HELP pi_health_build_info Build info for the currently running pi-health binary\n"+
			"# TYPE pi_health_build_info gauge\n"+
			"pi_health_build_info{sha=\"%s\"} 1\n", sha)
	return os.WriteFile(metricsFile, []byte(content), 0644)
}

// Run executes one check cycle: report the build-info metric, probe the
// homelab, notify Discord, and persist the consecutive-failure count.
// Returns the failure count after this run (0 means healthy).
func Run(cfg Config, stderr io.Writer) int {
	client := &http.Client{Timeout: cfg.HTTPTimeout}

	if err := writeMetrics(cfg.MetricsFile, BuildSHA); err != nil {
		_, _ = fmt.Fprintf(stderr, "metrics file write failed: %v\n", err)
	}

	if err := probe(client, cfg.ProbeURL); err == nil {
		if err := postDiscord(client, cfg.UptimeWebhook, fmt.Sprintf("✅ %s reachable from pi1", cfg.ProbeURL)); err != nil {
			_, _ = fmt.Fprintf(stderr, "discord post failed: %v\n", err)
		}
		if err := writeFailureCount(cfg.StateFile, 0); err != nil {
			_, _ = fmt.Fprintf(stderr, "state write failed: %v\n", err)
		}
		return 0
	}

	failures := readFailureCount(cfg.StateFile) + 1
	if err := writeFailureCount(cfg.StateFile, failures); err != nil {
		_, _ = fmt.Fprintf(stderr, "state write failed: %v\n", err)
	}
	_, _ = fmt.Fprintf(stderr, "probe failed (%d/%d consecutive)\n", failures, cfg.FailureThreshold)

	if failures == cfg.FailureThreshold {
		msg := fmt.Sprintf(
			"🔴 **HomelabUnreachable** -- pi1 could not reach %s (%d consecutive failed checks)",
			cfg.ProbeURL, failures)
		if err := postDiscord(client, cfg.DowntimeWebhook, msg); err != nil {
			_, _ = fmt.Fprintf(stderr, "discord post failed: %v\n", err)
		}
	}

	return failures
}
