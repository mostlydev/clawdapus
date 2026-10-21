package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mostlydev/clawdapus/internal/pod"
	"gopkg.in/yaml.v3"
)

type dashboardSettings struct {
	Bind    string
	Addr    string
	Enabled bool
}

// Removing a service from Compose does not stop an existing container. Target
// only our prior generated dashboard, using its original project definition.
func removeDisabledClawdash(generatedPath string, previous []byte) error {
	if len(previous) == 0 {
		return nil
	}
	var doc struct {
		Services map[string]struct {
			Labels map[string]string `yaml:"labels"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(previous, &doc); err != nil {
		return fmt.Errorf("read prior dashboard configuration: %w", err)
	}
	dash, exists := doc.Services["clawdash"]
	if !exists {
		return nil
	}
	if dash.Labels["claw.role"] != "dashboard" || dash.Labels["claw.service"] != "clawdash" {
		return fmt.Errorf("refusing to remove clawdash without generated dashboard labels")
	}
	f, err := os.CreateTemp(filepath.Dir(generatedPath), ".claw-previous-*.yml")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(previous); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := runComposeDockerCommand("compose", "-f", f.Name(), "rm", "--stop", "--force", "clawdash"); err != nil {
		return fmt.Errorf("remove disabled dashboard: %w", err)
	}
	return nil
}

func loadDashboardSettings() (dashboardSettings, error) {
	bind, err := pod.NormalizeDashboardBind(os.Getenv("CLAW_DASHBOARD_BIND"))
	if err != nil {
		return dashboardSettings{}, err
	}
	enabled := true
	if raw, set := os.LookupEnv("CLAWDASH_ENABLED"); set {
		enabled, err = strconv.ParseBool(strings.TrimSpace(raw))
		if err != nil {
			return dashboardSettings{}, fmt.Errorf("CLAWDASH_ENABLED must be true or false")
		}
	}
	addr := ""
	if enabled {
		addr, err = pod.ClawdashListenAddress(os.Getenv("CLAWDASH_ADDR"))
		if err != nil {
			return dashboardSettings{}, err
		}
	}
	return dashboardSettings{Bind: bind, Addr: addr, Enabled: enabled}, nil
}
