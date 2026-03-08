package microclaw

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/docker/docker/client"
	"github.com/mostlydev/clawdapus/internal/cllama"
	"github.com/mostlydev/clawdapus/internal/driver"
	"github.com/mostlydev/clawdapus/internal/driver/shared"
	"gopkg.in/yaml.v3"
)

// Driver implements Clawdapus runtime materialization for MicroClaw.
type Driver struct{}

func init() {
	driver.Register("microclaw", &Driver{})
}

func (d *Driver) Validate(rc *driver.ResolvedClaw) error {
	if rc.AgentHostPath == "" {
		return fmt.Errorf("microclaw driver: no agent host path specified (no contract, no start)")
	}
	if _, err := os.Stat(rc.AgentHostPath); err != nil {
		return fmt.Errorf("microclaw driver: agent file %q not found: %w", rc.AgentHostPath, err)
	}

	modelRef, err := primaryModelRef(rc.Models)
	if err != nil {
		return err
	}
	provider, _, ok := shared.SplitModelRef(modelRef)
	if !ok {
		return fmt.Errorf("microclaw driver: invalid MODEL primary %q (expected provider/model)", modelRef)
	}

	if len(rc.Configures) > 0 {
		for _, cmd := range rc.Configures {
			if _, _, err := parseConfigSetCommand(cmd); err != nil {
				return fmt.Errorf("microclaw driver: unsupported CONFIGURE command %q: %w", cmd, err)
			}
		}
	}

	for platform := range rc.Handles {
		switch platform {
		case "discord":
			if shared.ResolveEnvTokenFromMap(rc.Environment, "DISCORD_BOT_TOKEN") == "" {
				return fmt.Errorf("microclaw driver: HANDLE discord requires DISCORD_BOT_TOKEN in service environment")
			}
		case "telegram":
			if shared.ResolveEnvTokenFromMap(rc.Environment, "TELEGRAM_BOT_TOKEN") == "" {
				return fmt.Errorf("microclaw driver: HANDLE telegram requires TELEGRAM_BOT_TOKEN in service environment")
			}
		case "slack":
			if shared.ResolveEnvTokenFromMap(rc.Environment, "SLACK_BOT_TOKEN") == "" || shared.ResolveEnvTokenFromMap(rc.Environment, "SLACK_APP_TOKEN") == "" {
				return fmt.Errorf("microclaw driver: HANDLE slack requires SLACK_BOT_TOKEN and SLACK_APP_TOKEN in service environment")
			}
		default:
			fmt.Printf("[claw] warning: microclaw driver has no HANDLE mapping for platform %q; skipping channel enablement\n", platform)
		}
	}

	if len(rc.Cllama) == 0 {
		llmProvider := shared.NormalizeProvider(provider)
		if !shared.ProviderAllowsEmptyAPIKey(llmProvider) {
			if key := shared.ResolveProviderAPIKey(llmProvider, rc.Environment); key == "" {
				expected := strings.Join(shared.ExpectedProviderKeys(llmProvider), ", ")
				return fmt.Errorf("microclaw driver: no API key found for provider %q (checked: %s)", llmProvider, expected)
			}
		}
	}

	if len(rc.Invocations) > 0 {
		fmt.Printf("[claw] warning: microclaw driver: INVOKE scheduling not supported; ignoring %d invocations\n", len(rc.Invocations))
	}

	return nil
}

func (d *Driver) Materialize(rc *driver.ResolvedClaw, opts driver.MaterializeOpts) (*driver.MaterializeResult, error) {
	podName := opts.PodName
	if podName == "" {
		podName = rc.ServiceName
	}

	cfg, err := generateConfig(rc)
	if err != nil {
		return nil, err
	}

	for _, cmd := range rc.Configures {
		path, value, err := parseConfigSetCommand(cmd)
		if err != nil {
			return nil, fmt.Errorf("microclaw driver: apply CONFIGURE %q: %w", cmd, err)
		}
		if err := setPath(cfg, path, value); err != nil {
			return nil, fmt.Errorf("microclaw driver: apply CONFIGURE %q: %w", cmd, err)
		}
	}

	cfgBytes, err := yaml.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("microclaw driver: marshal config yaml: %w", err)
	}

	configDir := filepath.Join(opts.RuntimeDir, "config")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		return nil, fmt.Errorf("microclaw driver: create config dir: %w", err)
	}
	configPath := filepath.Join(configDir, "microclaw.config.yaml")
	if err := os.WriteFile(configPath, cfgBytes, 0o644); err != nil {
		return nil, fmt.Errorf("microclaw driver: write config: %w", err)
	}

	dataDir := filepath.Join(opts.RuntimeDir, "data")
	groupsDir := filepath.Join(dataDir, "runtime", "groups")
	if err := os.MkdirAll(groupsDir, 0o700); err != nil {
		return nil, fmt.Errorf("microclaw driver: create runtime groups dir: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(dataDir, "skills"), 0o700); err != nil {
		return nil, fmt.Errorf("microclaw driver: create skills dir: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(dataDir, "working_dir"), 0o700); err != nil {
		return nil, fmt.Errorf("microclaw driver: create working_dir: %w", err)
	}

	agentContent, err := os.ReadFile(rc.AgentHostPath)
	if err != nil {
		return nil, fmt.Errorf("microclaw driver: read agent contract: %w", err)
	}
	clawdapusMd := shared.GenerateClawdapusMD(rc, podName)
	seededAgents := strings.TrimSpace(string(agentContent)) + "\n\n---\n\n" + strings.TrimSpace(clawdapusMd) + "\n"
	seedPath := filepath.Join(groupsDir, "AGENTS.md")
	if err := os.WriteFile(seedPath, []byte(seededAgents), 0o644); err != nil {
		return nil, fmt.Errorf("microclaw driver: write seeded AGENTS.md: %w", err)
	}

	mounts := []driver.Mount{
		{
			HostPath:      configPath,
			ContainerPath: "/app/config/microclaw.config.yaml",
			ReadOnly:      true,
		},
		{
			HostPath:      dataDir,
			ContainerPath: "/claw-data",
			ReadOnly:      false,
		},
	}
	if rc.PersonaHostPath != "" {
		mounts = append(mounts, driver.Mount{
			HostPath:      rc.PersonaHostPath,
			ContainerPath: "/claw-data/persona",
			ReadOnly:      false,
		})
	}

	return &driver.MaterializeResult{
		Mounts:      mounts,
		Tmpfs:       []string{"/tmp"},
		ReadOnly:    false,
		Restart:     "on-failure",
		SkillDir:    "/claw-data/skills",
		SkillLayout: "directory",
		Healthcheck: &driver.Healthcheck{
			Test:     []string{"CMD-SHELL", "pgrep -f 'microclaw' > /dev/null"},
			Interval: "30s",
			Timeout:  "10s",
			Retries:  3,
		},
		Environment: map[string]string{
			"CLAW_MANAGED":     "true",
			"CLAW_PERSONA_DIR": "/claw-data/persona",
			"MICROCLAW_CONFIG": "/app/config/microclaw.config.yaml",
		},
	}, nil
}

func (d *Driver) PostApply(rc *driver.ResolvedClaw, opts driver.PostApplyOpts) error {
	if opts.ContainerID == "" {
		return fmt.Errorf("microclaw driver: post-apply check failed: no container ID")
	}
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return fmt.Errorf("microclaw driver: post-apply docker client: %w", err)
	}
	defer cli.Close()

	info, err := cli.ContainerInspect(context.Background(), opts.ContainerID)
	if err != nil {
		return fmt.Errorf("microclaw driver: post-apply inspect: %w", err)
	}
	if info.State == nil || !info.State.Running {
		status := "unknown"
		if info.State != nil {
			status = info.State.Status
		}
		return fmt.Errorf("microclaw driver: container is not running (status: %s)", status)
	}
	return nil
}

func (d *Driver) HealthProbe(ref driver.ContainerRef) (*driver.Health, error) {
	if ref.ContainerID == "" {
		return &driver.Health{OK: false, Detail: "no container ID"}, nil
	}
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("microclaw driver: health docker client: %w", err)
	}
	defer cli.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	info, err := cli.ContainerInspect(ctx, ref.ContainerID)
	if err != nil {
		return &driver.Health{OK: false, Detail: fmt.Sprintf("inspect: %v", err)}, nil
	}
	if info.State == nil || !info.State.Running {
		status := "unknown"
		if info.State != nil {
			status = info.State.Status
		}
		return &driver.Health{OK: false, Detail: fmt.Sprintf("not running (%s)", status)}, nil
	}
	return &driver.Health{OK: true, Detail: "container running"}, nil
}

func generateConfig(rc *driver.ResolvedClaw) (map[string]interface{}, error) {
	modelRef, err := primaryModelRef(rc.Models)
	if err != nil {
		return nil, err
	}
	provider, modelID, ok := shared.SplitModelRef(modelRef)
	if !ok {
		return nil, fmt.Errorf("microclaw driver: invalid MODEL primary %q (expected provider/model)", modelRef)
	}

	cfg := map[string]interface{}{
		"data_dir":              "/claw-data",
		"skills_dir":            "/claw-data/skills",
		"working_dir":           "/claw-data/working_dir",
		"working_dir_isolation": "chat",
		"timezone":              "UTC",
		"web_enabled":           true,
		"web_host":              "127.0.0.1",
		"web_port":              10961,
	}

	channels := map[string]interface{}{
		"web": map[string]interface{}{"enabled": true},
	}

	if len(rc.Cllama) > 0 {
		if strings.TrimSpace(rc.CllamaToken) == "" {
			return nil, fmt.Errorf("microclaw driver: CLLAMA is enabled but token is empty")
		}
		firstProxy := cllama.ProxyBaseURL(rc.Cllama[0])
		cfg["llm_base_url"] = firstProxy
		cfg["api_key"] = rc.CllamaToken

		if shared.NormalizeProvider(provider) == "anthropic" {
			cfg["llm_provider"] = "anthropic"
			cfg["model"] = modelID
		} else {
			cfg["llm_provider"] = "openai"
			cfg["model"] = provider + "/" + modelID
		}
	} else {
		llmProvider := shared.NormalizeProvider(provider)
		cfg["llm_provider"] = llmProvider
		cfg["model"] = modelID
		apiKey := shared.ResolveProviderAPIKey(llmProvider, rc.Environment)
		if apiKey != "" {
			cfg["api_key"] = apiKey
		} else {
			cfg["api_key"] = ""
		}
	}

	platforms := make([]string, 0, len(rc.Handles))
	for platform := range rc.Handles {
		platforms = append(platforms, platform)
	}
	sort.Strings(platforms)

	for _, platform := range platforms {
		h := rc.Handles[platform]
		switch platform {
		case "discord":
			discord := map[string]interface{}{"enabled": true}
			if token := shared.ResolveEnvTokenFromMap(rc.Environment, "DISCORD_BOT_TOKEN"); token != "" {
				discord["bot_token"] = token
			}
			if h != nil && strings.TrimSpace(h.Username) != "" {
				discord["bot_username"] = h.Username
			}
			if allowed := discordAllowedChannels(h); len(allowed) > 0 {
				discord["allowed_channels"] = allowed
			}
			channels["discord"] = discord
		case "telegram":
			telegram := map[string]interface{}{"enabled": true}
			if token := shared.ResolveEnvTokenFromMap(rc.Environment, "TELEGRAM_BOT_TOKEN"); token != "" {
				telegram["bot_token"] = token
			}
			if h != nil && strings.TrimSpace(h.Username) != "" {
				telegram["bot_username"] = h.Username
			}
			if allowed := telegramAllowedGroups(h); len(allowed) > 0 {
				telegram["allowed_groups"] = allowed
			}
			channels["telegram"] = telegram
		case "slack":
			slack := map[string]interface{}{"enabled": true}
			if bot := shared.ResolveEnvTokenFromMap(rc.Environment, "SLACK_BOT_TOKEN"); bot != "" {
				slack["bot_token"] = bot
			}
			if app := shared.ResolveEnvTokenFromMap(rc.Environment, "SLACK_APP_TOKEN"); app != "" {
				slack["app_token"] = app
			}
			if allowed := slackAllowedChannels(h); len(allowed) > 0 {
				slack["allowed_channels"] = allowed
			}
			channels["slack"] = slack
		}
	}

	cfg["channels"] = channels
	return cfg, nil
}

func primaryModelRef(models map[string]string) (string, error) {
	if models == nil {
		return "", fmt.Errorf("microclaw driver: missing MODEL primary (set `MODEL primary <provider/model>` in Clawfile)")
	}
	if primary := strings.TrimSpace(models["primary"]); primary != "" {
		return primary, nil
	}
	return "", fmt.Errorf("microclaw driver: missing MODEL primary (set `MODEL primary <provider/model>` in Clawfile)")
}

func discordAllowedChannels(h *driver.HandleInfo) []uint64 {
	if h == nil {
		return nil
	}
	seen := map[uint64]struct{}{}
	out := make([]uint64, 0)
	for _, g := range h.Guilds {
		for _, ch := range g.Channels {
			id, err := strconv.ParseUint(strings.TrimSpace(ch.ID), 10, 64)
			if err != nil || id == 0 {
				continue
			}
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			out = append(out, id)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func telegramAllowedGroups(h *driver.HandleInfo) []int64 {
	if h == nil {
		return nil
	}
	seen := map[int64]struct{}{}
	out := make([]int64, 0)
	for _, g := range h.Guilds {
		id, err := strconv.ParseInt(strings.TrimSpace(g.ID), 10, 64)
		if err != nil || id == 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func slackAllowedChannels(h *driver.HandleInfo) []string {
	if h == nil {
		return nil
	}
	seen := map[string]struct{}{}
	out := make([]string, 0)
	for _, g := range h.Guilds {
		for _, ch := range g.Channels {
			id := strings.TrimSpace(ch.ID)
			if id == "" {
				continue
			}
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

func parseConfigSetCommand(cmd string) (string, interface{}, error) {
	parts := strings.Fields(cmd)
	if len(parts) < 5 || parts[0] != "microclaw" || parts[1] != "config" || parts[2] != "set" {
		return "", nil, fmt.Errorf("expected 'microclaw config set <path> <value>'")
	}
	path := parts[3]
	value := strings.TrimSpace(strings.Join(parts[4:], " "))
	if value == "" {
		return "", nil, fmt.Errorf("empty config value")
	}

	var typed interface{}
	if err := yaml.Unmarshal([]byte(value), &typed); err == nil {
		if typed != nil {
			return path, typed, nil
		}
	}
	return path, value, nil
}

func setPath(obj map[string]interface{}, path string, value interface{}) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("invalid empty config path")
	}
	parts := strings.Split(path, ".")
	current := obj
	for i, part := range parts {
		if part == "" {
			return fmt.Errorf("invalid config path %q", path)
		}
		if i == len(parts)-1 {
			if existing, exists := current[part]; exists {
				if _, isMap := existing.(map[string]interface{}); isMap {
					return fmt.Errorf("path conflict at %q: cannot overwrite object with value", strings.Join(parts[:i+1], "."))
				}
			}
			current[part] = value
			return nil
		}
		nextRaw, exists := current[part]
		if !exists {
			next := make(map[string]interface{})
			current[part] = next
			current = next
			continue
		}
		next, ok := nextRaw.(map[string]interface{})
		if !ok {
			return fmt.Errorf("path conflict at %q: expected object, found %T", strings.Join(parts[:i+1], "."), nextRaw)
		}
		current = next
	}
	return nil
}
