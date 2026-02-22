package pod

import (
	"strings"
	"testing"

	"github.com/mostlydev/clawdapus/internal/driver"
	"gopkg.in/yaml.v3"
)

func TestEmitComposeBasicService(t *testing.T) {
	p := &Pod{
		Name: "test-pod",
		Services: map[string]*Service{
			"bot": {
				Image: "ghcr.io/example/bot:latest",
				Environment: map[string]string{
					"LOG_LEVEL": "info",
					"MODE":      "default",
				},
			},
		},
	}

	results := map[string]*driver.MaterializeResult{
		"bot": {
			ReadOnly: true,
			Restart:  "on-failure:5",
			Tmpfs:    []string{"/tmp", "/run"},
			Environment: map[string]string{
				"MODE":       "enforced",
				"DRIVER_VAR": "injected",
			},
			Healthcheck: &driver.Healthcheck{
				Test:     []string{"CMD", "curl", "-f", "http://localhost:8080/health"},
				Interval: "30s",
				Timeout:  "10s",
				Retries:  3,
			},
		},
	}

	out, err := EmitCompose(p, results)
	if err != nil {
		t.Fatalf("EmitCompose returned error: %v", err)
	}

	// read_only hardening
	if !strings.Contains(out, "read_only: true") {
		t.Error("expected read_only: true in output")
	}

	// tmpfs paths
	if !strings.Contains(out, "/tmp") {
		t.Error("expected /tmp tmpfs in output")
	}
	if !strings.Contains(out, "/run") {
		t.Error("expected /run tmpfs in output")
	}

	// restart policy
	if !strings.Contains(out, "on-failure:5") {
		t.Error("expected restart on-failure:5 in output")
	}

	// healthcheck
	if !strings.Contains(out, "interval: 30s") {
		t.Error("expected healthcheck interval in output")
	}
	if !strings.Contains(out, "retries: 3") {
		t.Error("expected healthcheck retries in output")
	}

	// driver env wins on conflict: MODE should be "enforced", not "default"
	if !strings.Contains(out, "enforced") {
		t.Error("expected driver env to override pod env for MODE")
	}
	if !strings.Contains(out, "DRIVER_VAR") {
		t.Error("expected DRIVER_VAR in environment")
	}

	// pod env preserved for non-conflicting keys
	if !strings.Contains(out, "LOG_LEVEL") {
		t.Error("expected LOG_LEVEL from pod env in output")
	}

	// labels
	if !strings.Contains(out, "claw.pod: test-pod") {
		t.Error("expected claw.pod label in output")
	}
	if !strings.Contains(out, "claw.service: bot") {
		t.Error("expected claw.service label in output")
	}
}

func TestEmitComposeExpandsCount(t *testing.T) {
	p := &Pod{
		Name: "scale-pod",
		Services: map[string]*Service{
			"worker": {
				Image: "ghcr.io/example/worker:v1",
				Claw: &ClawBlock{
					Count: 3,
				},
			},
		},
	}

	results := map[string]*driver.MaterializeResult{
		"worker": {
			ReadOnly: true,
			Restart:  "on-failure",
		},
	}

	out, err := EmitCompose(p, results)
	if err != nil {
		t.Fatalf("EmitCompose returned error: %v", err)
	}

	// Should have ordinal-named services
	if !strings.Contains(out, "worker-0:") {
		t.Error("expected worker-0 service in output")
	}
	if !strings.Contains(out, "worker-1:") {
		t.Error("expected worker-1 service in output")
	}
	if !strings.Contains(out, "worker-2:") {
		t.Error("expected worker-2 service in output")
	}

	// Bare "worker:" without ordinal should NOT appear as a service key.
	// We check that "worker:" only appears in ordinal form or label context.
	lines := strings.Split(out, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		// A top-level service key would be "worker:" with no ordinal suffix
		if trimmed == "worker:" {
			t.Error("bare 'worker:' should not appear; expected ordinal-named services only")
		}
	}

	// Ordinal labels
	if !strings.Contains(out, "claw.ordinal: \"0\"") {
		t.Error("expected claw.ordinal label for ordinal 0")
	}
	if !strings.Contains(out, "claw.ordinal: \"2\"") {
		t.Error("expected claw.ordinal label for ordinal 2")
	}
}

func TestEmitComposeVolumeSurface(t *testing.T) {
	p := &Pod{
		Name: "vol-pod",
		Services: map[string]*Service{
			"processor": {
				Image: "ghcr.io/example/proc:v1",
				Claw: &ClawBlock{
					Surfaces: []driver.ResolvedSurface{
						{Scheme: "volume", Target: "shared-cache", AccessMode: "read-write"},
					},
				},
			},
		},
	}

	results := map[string]*driver.MaterializeResult{
		"processor": {
			ReadOnly: true,
			Restart:  "on-failure",
		},
	}

	out, err := EmitCompose(p, results)
	if err != nil {
		t.Fatalf("EmitCompose returned error: %v", err)
	}

	// Top-level volumes section should declare shared-cache
	if !strings.Contains(out, "volumes:") {
		t.Error("expected top-level volumes section in output")
	}
	if !strings.Contains(out, "shared-cache") {
		t.Error("expected shared-cache volume in output")
	}

	// Volume mount on the service
	if !strings.Contains(out, "shared-cache:/mnt/shared-cache:rw") {
		t.Error("expected volume mount shared-cache:/mnt/shared-cache:rw in output")
	}
}

func TestEmitComposeRejectsUnknownServiceSurfaceTarget(t *testing.T) {
	p := &Pod{
		Name: "missing-target-pod",
		Services: map[string]*Service{
			"researcher": {
				Image: "ghcr.io/example/researcher:v1",
				Claw: &ClawBlock{
					Surfaces: []driver.ResolvedSurface{
						{Scheme: "service", Target: "gateway"},
					},
				},
			},
		},
	}

	results := map[string]*driver.MaterializeResult{
		"researcher": {
			ReadOnly: true,
			Restart:  "on-failure",
		},
	}

	_, err := EmitCompose(p, results)
	if err == nil {
		t.Fatal("expected error for unknown service surface target")
	}
	if !strings.Contains(err.Error(), "targets unknown service") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestEmitComposeMultiServiceSharedVolume(t *testing.T) {
	p := &Pod{
		Name: "research-pod",
		Services: map[string]*Service{
			"researcher": {
				Image: "claw-openclaw-example",
				Claw: &ClawBlock{
					Surfaces: []driver.ResolvedSurface{
						{Scheme: "volume", Target: "research-cache", AccessMode: "read-write"},
					},
				},
			},
			"analyst": {
				Image: "claw-openclaw-example",
				Claw: &ClawBlock{
					Surfaces: []driver.ResolvedSurface{
						{Scheme: "volume", Target: "research-cache", AccessMode: "read-only"},
					},
				},
			},
		},
	}

	results := map[string]*driver.MaterializeResult{
		"researcher": {
			ReadOnly: true,
			Restart:  "on-failure",
		},
		"analyst": {
			ReadOnly: true,
			Restart:  "on-failure",
		},
	}

	out, err := EmitCompose(p, results)
	if err != nil {
		t.Fatalf("EmitCompose returned error: %v", err)
	}

	// Parse YAML to verify structure
	var cf struct {
		Services map[string]struct {
			Volumes  []string `yaml:"volumes"`
			Networks []string `yaml:"networks"`
		} `yaml:"services"`
		Volumes  map[string]interface{} `yaml:"volumes"`
		Networks map[string]struct {
			Internal bool `yaml:"internal"`
		} `yaml:"networks"`
	}
	if err := yaml.Unmarshal([]byte(out), &cf); err != nil {
		t.Fatalf("failed to parse compose output: %v", err)
	}

	// Top-level volumes declares research-cache
	if _, ok := cf.Volumes["research-cache"]; !ok {
		t.Error("expected research-cache in top-level volumes")
	}

	// Researcher gets rw mount
	researcher, ok := cf.Services["researcher"]
	if !ok {
		t.Fatal("expected researcher service in output")
	}
	foundRW := false
	for _, v := range researcher.Volumes {
		if v == "research-cache:/mnt/research-cache:rw" {
			foundRW = true
		}
	}
	if !foundRW {
		t.Errorf("expected researcher volume mount research-cache:/mnt/research-cache:rw, got %v", researcher.Volumes)
	}

	// Analyst gets ro mount
	analyst, ok := cf.Services["analyst"]
	if !ok {
		t.Fatal("expected analyst service in output")
	}
	foundRO := false
	for _, v := range analyst.Volumes {
		if v == "research-cache:/mnt/research-cache:ro" {
			foundRO = true
		}
	}
	if !foundRO {
		t.Errorf("expected analyst volume mount research-cache:/mnt/research-cache:ro, got %v", analyst.Volumes)
	}

	// Both services on claw-internal network
	if len(researcher.Networks) == 0 || researcher.Networks[0] != "claw-internal" {
		t.Errorf("expected researcher on claw-internal network, got %v", researcher.Networks)
	}
	if len(analyst.Networks) == 0 || analyst.Networks[0] != "claw-internal" {
		t.Errorf("expected analyst on claw-internal network, got %v", analyst.Networks)
	}

	// claw-internal network must exist (internet access is allowed — agents need LLMs, Discord, etc.)
	if _, ok := cf.Networks["claw-internal"]; !ok {
		t.Fatal("expected claw-internal network in output")
	}
}

func TestEmitComposeHandleEnvsBroadcastToAllServices(t *testing.T) {
	p := &Pod{
		Name: "crypto-ops",
		Services: map[string]*Service{
			"crusher": {
				Image: "openclaw:latest",
				Claw: &ClawBlock{
					Handles: map[string]*driver.HandleInfo{
						"discord": {ID: "123456789", Username: "crypto-bot"},
					},
				},
			},
			"api": {
				Image: "custom/api:latest",
				// Not a claw — no x-claw block
			},
		},
	}

	results := map[string]*driver.MaterializeResult{
		"crusher": {ReadOnly: true, Restart: "on-failure"},
	}

	out, err := EmitCompose(p, results)
	if err != nil {
		t.Fatalf("EmitCompose returned error: %v", err)
	}

	// Both crusher and api should have the CLAW_HANDLE vars
	if !strings.Contains(out, "CLAW_HANDLE_CRUSHER_DISCORD_ID: \"123456789\"") {
		t.Error("expected CLAW_HANDLE_CRUSHER_DISCORD_ID in output")
	}
	if !strings.Contains(out, "CLAW_HANDLE_CRUSHER_DISCORD_USERNAME: crypto-bot") {
		t.Error("expected CLAW_HANDLE_CRUSHER_DISCORD_USERNAME in output")
	}
	if !strings.Contains(out, "CLAW_HANDLE_CRUSHER_DISCORD_JSON") {
		t.Error("expected CLAW_HANDLE_CRUSHER_DISCORD_JSON in output")
	}
}

func TestEmitComposeHandleEnvsMultipleServicesAndPlatforms(t *testing.T) {
	p := &Pod{
		Name: "multi-pod",
		Services: map[string]*Service{
			"alpha": {
				Image: "openclaw:latest",
				Claw: &ClawBlock{
					Handles: map[string]*driver.HandleInfo{
						"discord": {ID: "111"},
						"slack":   {ID: "U222"},
					},
				},
			},
			"beta": {
				Image: "openclaw:latest",
				Claw: &ClawBlock{
					Handles: map[string]*driver.HandleInfo{
						"discord": {ID: "333"},
					},
				},
			},
		},
	}

	results := map[string]*driver.MaterializeResult{
		"alpha": {ReadOnly: true, Restart: "on-failure"},
		"beta":  {ReadOnly: true, Restart: "on-failure"},
	}

	out, err := EmitCompose(p, results)
	if err != nil {
		t.Fatalf("EmitCompose returned error: %v", err)
	}

	// All handle vars should appear across all services
	if !strings.Contains(out, "CLAW_HANDLE_ALPHA_DISCORD_ID") {
		t.Error("expected CLAW_HANDLE_ALPHA_DISCORD_ID")
	}
	if !strings.Contains(out, "CLAW_HANDLE_ALPHA_SLACK_ID") {
		t.Error("expected CLAW_HANDLE_ALPHA_SLACK_ID")
	}
	if !strings.Contains(out, "CLAW_HANDLE_BETA_DISCORD_ID") {
		t.Error("expected CLAW_HANDLE_BETA_DISCORD_ID")
	}
}

func TestEmitComposeHandleEnvWithGuilds(t *testing.T) {
	p := &Pod{
		Name: "guild-pod",
		Services: map[string]*Service{
			"bot": {
				Image: "openclaw:latest",
				Claw: &ClawBlock{
					Handles: map[string]*driver.HandleInfo{
						"discord": {
							ID: "999",
							Guilds: []driver.GuildInfo{
								{ID: "aaa", Channels: []driver.ChannelInfo{{ID: "bbb"}}},
								{ID: "ccc"},
							},
						},
					},
				},
			},
		},
	}

	results := map[string]*driver.MaterializeResult{
		"bot": {ReadOnly: true, Restart: "on-failure"},
	}

	out, err := EmitCompose(p, results)
	if err != nil {
		t.Fatalf("EmitCompose returned error: %v", err)
	}

	if !strings.Contains(out, "CLAW_HANDLE_BOT_DISCORD_GUILDS") {
		t.Error("expected CLAW_HANDLE_BOT_DISCORD_GUILDS")
	}
	if !strings.Contains(out, "aaa,ccc") {
		t.Errorf("expected comma-separated guild IDs 'aaa,ccc' in output:\n%s", out)
	}
}

func TestEmitComposeHandleEnvsDoNotOverrideExisting(t *testing.T) {
	p := &Pod{
		Name: "override-pod",
		Services: map[string]*Service{
			"bot": {
				Image: "openclaw:latest",
				Claw: &ClawBlock{
					Handles: map[string]*driver.HandleInfo{
						"discord": {ID: "111"},
					},
				},
				Environment: map[string]string{
					// Operator explicitly overrides the auto-generated _ID var
					"CLAW_HANDLE_BOT_DISCORD_ID": "operator-set",
				},
			},
		},
	}

	results := map[string]*driver.MaterializeResult{
		"bot": {ReadOnly: true, Restart: "on-failure"},
	}

	out, err := EmitCompose(p, results)
	if err != nil {
		t.Fatalf("EmitCompose returned error: %v", err)
	}

	// Operator-declared _ID should win over auto-computed one
	if !strings.Contains(out, "operator-set") {
		t.Error("expected operator-set value to appear in output")
	}
	// Auto-computed _ID value "111" should not appear as the _ID value
	// (it may still appear in _JSON, which is expected — _JSON reflects struct)
	if strings.Contains(out, "CLAW_HANDLE_BOT_DISCORD_ID: \"111\"") {
		t.Error("expected handle _ID env var to be overridden by operator env, but found auto-computed value")
	}
}

func TestEmitComposeNoHandlesMeansNoHandleEnvs(t *testing.T) {
	p := &Pod{
		Name: "no-handles-pod",
		Services: map[string]*Service{
			"bot": {
				Image: "openclaw:latest",
				Claw:  &ClawBlock{},
			},
		},
	}

	results := map[string]*driver.MaterializeResult{
		"bot": {ReadOnly: true, Restart: "on-failure"},
	}

	out, err := EmitCompose(p, results)
	if err != nil {
		t.Fatalf("EmitCompose returned error: %v", err)
	}

	if strings.Contains(out, "CLAW_HANDLE_") {
		t.Error("expected no CLAW_HANDLE_* vars when no handles declared")
	}
}

func TestEmitComposeIsDeterministic(t *testing.T) {
	p := &Pod{
		Name: "det-pod",
		Services: map[string]*Service{
			"zulu": {
				Image: "ghcr.io/example/zulu:v1",
			},
			"alpha": {
				Image: "ghcr.io/example/alpha:v1",
			},
		},
	}

	results := map[string]*driver.MaterializeResult{
		"zulu": {
			ReadOnly: true,
			Restart:  "on-failure",
		},
		"alpha": {
			ReadOnly: true,
			Restart:  "on-failure",
		},
	}

	out1, err := EmitCompose(p, results)
	if err != nil {
		t.Fatalf("first EmitCompose returned error: %v", err)
	}

	out2, err := EmitCompose(p, results)
	if err != nil {
		t.Fatalf("second EmitCompose returned error: %v", err)
	}

	if out1 != out2 {
		t.Errorf("output is not deterministic:\n--- first ---\n%s\n--- second ---\n%s", out1, out2)
	}

	// alpha should appear before zulu due to sorting
	alphaIdx := strings.Index(out1, "alpha:")
	zuluIdx := strings.Index(out1, "zulu:")
	if alphaIdx == -1 || zuluIdx == -1 {
		t.Fatal("expected both alpha and zulu services in output")
	}
	if alphaIdx > zuluIdx {
		t.Error("expected alpha to appear before zulu (sorted order)")
	}
}
