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
	provider, _, ok := splitModelRef(modelRef)
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
			if resolveEnvTokenFromMap(rc.Environment, "DISCORD_BOT_TOKEN") == "" {
				return fmt.Errorf("microclaw driver: HANDLE discord requires DISCORD_BOT_TOKEN in service environment")
			}
		case "telegram":
			if resolveEnvTokenFromMap(rc.Environment, "TELEGRAM_BOT_TOKEN") == "" {
				return fmt.Errorf("microclaw driver: HANDLE telegram requires TELEGRAM_BOT_TOKEN in service environment")
			}
		case "slack":
			if resolveEnvTokenFromMap(rc.Environment, "SLACK_BOT_TOKEN") == "" || resolveEnvTokenFromMap(rc.Environment, "SLACK_APP_TOKEN") == "" {
				return fmt.Errorf("microclaw driver: HANDLE slack requires SLACK_BOT_TOKEN and SLACK_APP_TOKEN in service environment")
			}
		default:
			fmt.Printf("[claw] warning: microclaw driver has no HANDLE mapping for platform %q; skipping channel enablement\n", platform)
		}
	}

	if len(rc.Cllama) == 0 {
		llmProvider := normalizeProvider(provider)
		if !providerAllowsEmptyAPIKey(llmProvider) {
			if key := resolveProviderAPIKey(llmProvider, rc.Environment); key == "" {
				expected := strings.Join(expectedProviderKeys(llmProvider), ", ")
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

	return &driver.MaterializeResult{
		Mounts: []driver.Mount{
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
		},
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
	provider, modelID, ok := splitModelRef(modelRef)
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
		firstProxy := fmt.Sprintf("http://cllama-%s:8080/v1", rc.Cllama[0])
		cfg["llm_base_url"] = firstProxy
		cfg["api_key"] = rc.CllamaToken

		if normalizeProvider(provider) == "anthropic" {
			cfg["llm_provider"] = "anthropic"
			cfg["model"] = modelID
		} else {
			cfg["llm_provider"] = "openai"
			cfg["model"] = provider + "/" + modelID
		}
	} else {
		llmProvider := normalizeProvider(provider)
		cfg["llm_provider"] = llmProvider
		cfg["model"] = modelID
		apiKey := resolveProviderAPIKey(llmProvider, rc.Environment)
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
			if token := resolveEnvTokenFromMap(rc.Environment, "DISCORD_BOT_TOKEN"); token != "" {
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
			if token := resolveEnvTokenFromMap(rc.Environment, "TELEGRAM_BOT_TOKEN"); token != "" {
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
			if bot := resolveEnvTokenFromMap(rc.Environment, "SLACK_BOT_TOKEN"); bot != "" {
				slack["bot_token"] = bot
			}
			if app := resolveEnvTokenFromMap(rc.Environment, "SLACK_APP_TOKEN"); app != "" {
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

func splitModelRef(ref string) (string, string, bool) {
	trimmed := strings.TrimSpace(ref)
	if trimmed == "" {
		return "", "", false
	}

	parts := strings.SplitN(trimmed, "/", 2)
	if len(parts) == 1 {
		return "anthropic", parts[0], true
	}
	provider := strings.ToLower(strings.TrimSpace(parts[0]))
	model := strings.TrimSpace(parts[1])
	if provider == "" || model == "" {
		return "", "", false
	}
	return provider, model, true
}

func normalizeProvider(provider string) string {
	normalized := strings.ToLower(strings.TrimSpace(provider))
	if normalized == "" {
		return "openai"
	}
	return normalized
}

func providerAllowsEmptyAPIKey(provider string) bool {
	switch normalizeProvider(provider) {
	case "ollama":
		return true
	default:
		return false
	}
}

func expectedProviderKeys(provider string) []string {
	p := normalizeProvider(provider)
	sanitized := sanitizeProviderEnvSuffix(p)

	out := []string{fmt.Sprintf("%s_API_KEY", sanitized)}
	out = append(out, fmt.Sprintf("PROVIDER_API_KEY_%s", sanitized))
	out = append(out, "PROVIDER_API_KEY")
	if p != "anthropic" {
		out = append(out, "OPENAI_API_KEY")
	}
	return dedupStrings(out)
}

func resolveProviderAPIKey(provider string, env map[string]string) string {
	for _, key := range expectedProviderKeys(provider) {
		if token := resolveEnvTokenFromMap(env, key); token != "" {
			return token
		}
	}
	return ""
}

func resolveEnvTokenFromMap(env map[string]string, key string) string {
	if env == nil {
		return ""
	}
	return resolveEnvToken(env[key])
}

func resolveEnvToken(raw string) string {
	v := strings.TrimSpace(raw)
	if v == "" {
		return ""
	}

	if strings.HasPrefix(v, "${") && strings.HasSuffix(v, "}") {
		name := strings.TrimSpace(v[2 : len(v)-1])
		if name == "" {
			return ""
		}
		return strings.TrimSpace(os.Getenv(name))
	}
	if strings.HasPrefix(v, "$") {
		name := strings.TrimSpace(strings.TrimPrefix(v, "$"))
		if isEnvVarName(name) {
			return strings.TrimSpace(os.Getenv(name))
		}
	}
	return v
}

func isEnvVarName(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if i == 0 && !(r == '_' || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z')) {
			return false
		}
		if !(r == '_' || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')) {
			return false
		}
	}
	return true
}

func sanitizeProviderEnvSuffix(provider string) string {
	if provider == "" {
		return "OPENAI"
	}
	s := strings.ToUpper(provider)
	s = strings.ReplaceAll(s, "-", "_")
	s = strings.ReplaceAll(s, ".", "_")
	s = strings.ReplaceAll(s, "/", "_")
	return s
}

func dedupStrings(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, v := range in {
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
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
