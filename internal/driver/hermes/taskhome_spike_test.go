//go:build spike

package hermes

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mostlydev/clawdapus/internal/infraimages"
	"gopkg.in/yaml.v3"
)

// TestSpikeTaskHomeHermesContract proves pinned CLI behavior, not the future
// supervisor profile or cross-task isolation. It uses only local fake inference.
func TestSpikeTaskHomeHermesContract(t *testing.T) {
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skipf("Docker unavailable: %v", err)
	}
	image := "ghcr.io/mostlydev/hermes-base:" + infraimages.DefaultHermesBaseTag
	if err := exec.Command("docker", "image", "inspect", image).Run(); err != nil {
		t.Fatalf("pull pinned image before this probe: %s", image)
	}
	root := t.TempDir()
	home := filepath.Join(root, "home")
	if err := os.Mkdir(home, 0777); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(home, 0777); err != nil {
		t.Fatal(err)
	}
	script, err := filepath.Abs("testdata/taskhome_probe.py")
	if err != nil {
		t.Fatal(err)
	}
	project := fmt.Sprintf("claw394-contract-%d", time.Now().UnixNano())
	file := filepath.Join(root, "compose.yml")
	doc := map[string]any{
		"services": map[string]any{"probe": map[string]any{
			"image": image, "user": "65534:65534", "read_only": true, "cap_drop": []string{"ALL"}, "security_opt": []string{"no-new-privileges:true"}, "init": true,
			"entrypoint":  []string{"python3", "/probe.py"},
			"environment": map[string]string{"HOME": "/home/task", "HERMES_HOME": "/home/task/hermes", "TERMINAL_ENV": "local", "TERMINAL_CWD": "/home/task/workspace", "HERMES_YOLO_MODE": "1", "HERMES_ACCEPT_HOOKS": "1", "PYTHONDONTWRITEBYTECODE": "1"},
			"volumes":     []any{map[string]any{"type": "bind", "source": home, "target": "/home/task"}, map[string]any{"type": "bind", "source": script, "target": "/probe.py", "read_only": true}},
			"tmpfs":       []string{"/tmp:mode=1777,size=268435456"}, "mem_limit": "2g", "pids_limit": 128, "networks": []string{"probe"},
			"logging": map[string]any{"driver": "json-file", "options": map[string]string{"max-size": "2m", "max-file": "1"}},
		}}, "networks": map[string]any{"probe": map[string]any{"internal": true}}}
	raw, err := yaml.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, raw, 0600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, "docker", "compose", "-p", project, "-f", file, "down", "--remove-orphans").CombinedOutput()
		if err != nil {
			t.Errorf("probe cleanup: %v %s", err, out)
		}
	})
	for _, phase := range []string{"first", "resume"} {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		started := time.Now()
		name := project + "-" + phase
		out, err := exec.CommandContext(ctx, "docker", "compose", "-p", project, "-f", file, "run", "--name", name, "--pull", "never", "-e", "PROBE_PHASE="+phase, "probe").CombinedOutput()
		cancel()
		t.Logf("phase=%s compose-invoke-to-exit=%s\n%s", phase, time.Since(started), out)
		if err != nil {
			t.Fatalf("phase %s: %v", phase, err)
		}
		if !strings.Contains(string(out), `"uid": 65534`) {
			t.Fatal("probe did not verify unprivileged uid")
		}
		createdRaw, err := exec.Command("docker", "inspect", name, "--format", "{{.Created}}").Output()
		if err != nil {
			t.Fatal(err)
		}
		created, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(string(createdRaw)))
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(string(out), "\n") {
			var event struct {
				Path string  `json:"path"`
				At   float64 `json:"time"`
			}
			if json.Unmarshal([]byte(line), &event) == nil && event.Path == "/v1/chat/completions" {
				firstRequest := time.Unix(0, int64(event.At*1e9))
				t.Logf("phase=%s container-created-to-first-model-request=%s (one local sample; image already present)", phase, firstRequest.Sub(created))
				break
			}
		}
	}
}
