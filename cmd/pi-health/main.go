// Command pi-health is the entrypoint for the watchdog binary. All real
// logic lives in internal/watchdog, which is what's unit tested -- this
// file is intentionally trivial and untested (go test never invokes
// main(), so it can't be part of a meaningful coverage figure anyway).
package main

import (
	"os"

	"github.com/mattjmorrison-homelab/pi-health/internal/watchdog"
)

func main() {
	cfg, err := watchdog.LoadConfig(os.Getenv)
	if err != nil {
		_, _ = os.Stderr.WriteString("config error: " + err.Error() + "\n")
		os.Exit(1)
	}
	watchdog.Run(cfg, os.Stderr)
}
