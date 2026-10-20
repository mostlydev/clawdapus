//go:build spike

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mostlydev/clawdapus/internal/driver"
	openclawdriver "github.com/mostlydev/clawdapus/internal/driver/openclaw"
)

// This uses the real pinned runtime and emitted driver mounts. It checks the
// schema, non-root startup and per-ordinal persistence without provider spend.
func TestSpikeOpenClawPinnedRefresh(t *testing.T) {
	dir := t.TempDir()
	d := &openclawdriver.Driver{}
	_, recipe := d.BaseImage()
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte(recipe), 0644); err != nil {
		t.Fatal(err)
	}
	tag := fmt.Sprintf("claw-openclaw-refresh-smoke:%d", time.Now().UnixNano())
	spikeBuildImage(t, dir, tag, "Dockerfile")
	t.Cleanup(func() { _ = exec.Command("docker", "image", "rm", tag).Run() })
	agent := filepath.Join(dir, "AGENTS.md")
	if err := os.WriteFile(agent, []byte("# Pinned runtime contract\n"), 0644); err != nil {
		t.Fatal(err)
	}
	rc := &driver.ResolvedClaw{ServiceName: "bot", Count: 2, AgentHostPath: agent, Models: map[string]string{"primary": "openai/gpt-test"}, Cllama: []string{"passthrough"}, CllamaToken: "fixture-proxy-token", Handles: map[string]*driver.HandleInfo{"discord": {ID: "123"}}, Configures: []string{`openclaw config set channels.discord.enabled false`, `openclaw config set tools.deny ["write","exec"]`}}
	result, err := d.Materialize(rc, driver.MaterializeOpts{RuntimeDir: filepath.Join(dir, "runtime"), StateDir: filepath.Join(dir, "durable")})
	if err != nil {
		t.Fatal(err)
	}
	services := map[string]any{}
	for _, name := range []string{"bot-0", "bot-1"} {
		volumes := []string{}
		for _, m := range result.Mounts {
			host := m.HostPath
			if p := m.HostPathByService[name]; p != "" {
				host = p
			}
			if host == "" {
				t.Fatal("missing emitted host path")
			}
			v := host + ":" + m.ContainerPath
			if m.ReadOnly {
				v += ":ro"
			}
			volumes = append(volumes, v)
		}
		env := map[string]string{}
		for k, v := range result.Environment {
			env[k] = v
		}
		env["DISCORD_BOT_TOKEN"] = "disabled-fixture-token"
		services[name] = map[string]any{"image": tag, "user": "10001:10001", "volumes": volumes, "environment": env, "tmpfs": result.Tmpfs, "read_only": result.ReadOnly, "cap_drop": []string{"ALL"}, "security_opt": []string{"no-new-privileges:true"}, "stop_grace_period": result.StopGracePeriod, "healthcheck": map[string]any{"start_period": result.Healthcheck.StartPeriod, "test": result.Healthcheck.Test, "interval": "2s", "timeout": "10s", "retries": 30}}
	}
	raw, _ := json.Marshal(map[string]any{"services": services})
	compose := filepath.Join(dir, "compose.json")
	if err := os.WriteFile(compose, raw, 0600); err != nil {
		t.Fatal(err)
	}
	project := fmt.Sprintf("openclaw-refresh-%d", time.Now().UnixNano())
	base := []string{"compose", "-p", project, "-f", compose}
	run := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("docker", append(append([]string{}, base...), args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("compose %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	t.Cleanup(func() {
		out, err := exec.Command("docker", append(append([]string{}, base...), "down", "--remove-orphans")...).CombinedOutput()
		if err != nil {
			t.Errorf("cleanup: %v %s", err, out)
		}
	})
	t.Cleanup(func() {
		if t.Failed() {
			out, _ := exec.Command("docker", append(append([]string{}, base...), "logs", "--tail", "55")...).CombinedOutput()
			t.Logf("runtime logs:\n%s", out)
		}
	})
	run("up", "-d", "--wait", "--wait-timeout", "120")
	for _, name := range []string{"bot-0", "bot-1"} {
		run("exec", "-T", name, "openclaw", "config", "validate", "--json")
		version := run("exec", "-T", name, "node", "-p", `require('/usr/local/lib/node_modules/openclaw/package.json').version`)
		if strings.TrimSpace(version) != openclawdriver.UpstreamVersion {
			t.Fatalf("unexpected installed runtime %q", version)
		}
		run("exec", "-T", name, "node", "-e", `require('node:fs').writeFileSync('/root/.openclaw/state/refresh-identity',process.argv[1])`, name)
	}
	// Persisted runner state must not carry a copy of the compiled bearer.
	for _, m := range result.Mounts {
		paths := []string{m.HostPath}
		for _, p := range m.HostPathByService {
			paths = append(paths, p)
		}
		for _, root := range paths {
			if root == "" || !strings.Contains(root, "openclaw-state") {
				continue
			}
			err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
				if err != nil {
					return err
				}
				if info.Mode().IsRegular() {
					b, e := os.ReadFile(path)
					if e != nil {
						return e
					}
					if bytes.Contains(b, []byte(rc.CllamaToken)) {
						t.Errorf("bearer persisted outside generated config: %s", filepath.Base(path))
					}
					t.Logf("persistent file: %s", filepath.Base(path))
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	run("up", "-d", "--force-recreate", "--wait", "--wait-timeout", "120")
	logs := run("logs", "--no-color", "--tail", "150")
	if strings.Contains(logs, "Waiting for the previous Gateway") {
		t.Error("graceful recreation left a fresh owner heartbeat")
	}

	for _, name := range []string{"bot-0", "bot-1"} {
		got := run("exec", "-T", name, "node", "-e", `process.stdout.write(require('node:fs').readFileSync('/root/.openclaw/state/refresh-identity'))`)
		if got != name {
			t.Fatalf("state lost or shared after recreate: %s=%q", name, got)
		}
	}
}
