package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/mostlydev/clawdapus/internal/pod"
	"gopkg.in/yaml.v3"
)

type dashboardSettings struct {
	Bind    string
	Addr    string
	Enabled bool
}

// Clean private snapshots left by an interrupted prior up. Never remove a
// symlink, directory or another user's file in the reserved snapshot namespace.
func cleanupDashboardSnapshots(podDir string) error {
	paths, err := filepath.Glob(filepath.Join(podDir, ".claw-previous-*.yml"))
	if err != nil {
		return err
	}
	for _, path := range paths {
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		if !info.Mode().IsRegular() || !ok || st.Uid != uint32(os.Geteuid()) {
			continue
		}
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("remove interrupted dashboard snapshot: %w", err)
		}
	}
	return nil
}

// The result tells the caller whether runtime rollback is still safe. A failed
// removal has not applied the new definition, so its prior runtime must survive.
func applyComposeDefinition(path string, output []byte, mode os.FileMode, previous []byte, disableDashboard, detached bool) (bool, error) {
	if disableDashboard {
		if err := removeDisabledClawdash(path, previous); err != nil {
			return false, err
		}
	}
	if err := os.WriteFile(path, output, mode); err != nil {
		return true, fmt.Errorf("write compose.generated.yml: %w", err)
	}
	if err := os.Chmod(path, mode); err != nil {
		return true, fmt.Errorf("chmod compose.generated.yml: %w", err)
	}
	fmt.Printf("[claw] wrote %s\n", path)
	args := []string{"compose", "-f", path, "up"}
	if detached {
		args = append(args, "-d")
	}
	if err := runComposeDockerCommand(args...); err != nil {
		return true, fmt.Errorf("docker compose up failed: %w", err)
	}
	return true, nil
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
		return fmt.Errorf("cannot read previous clawdash configuration; restore a valid compose.generated.yml and its referenced inputs, then run 'claw compose rm --stop --force clawdash' and retry claw up")
	}
	dash, exists := doc.Services["clawdash"]
	if !exists {
		return nil
	}
	if dash.Labels["claw.role"] != "dashboard" || dash.Labels["claw.service"] != "clawdash" {
		return fmt.Errorf("refusing to remove clawdash without generated dashboard labels")
	}
	// Stay beside the original file: without -p, Compose derives the default
	// project and relative inputs from this directory (or the explicit name/env).
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
		return fmt.Errorf("remove previous clawdash: %w; restore any missing inputs referenced by compose.generated.yml, then run 'claw compose rm --stop --force clawdash' and retry claw up", err)
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
