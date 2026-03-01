package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

var (
	initFromPath   string
	initProject    string
	initAgent      string
	initType       string
	initModel      string
	initCllama     string
	initPlatform   string
	initVolumeSpec string
)

type initScaffoldOptions struct {
	ProjectName string
	AgentName   string
	ClawType    string
	Model       string
	Cllama      string
	Platform    string
	VolumeSpec  string
}

type initResolvedConfig struct {
	ProjectName string
	AgentName   string
	ClawType    string
	Model       string
	Cllama      string
	Platform    string
	VolumeName  string
	VolumeMode  string
}

var initCmd = &cobra.Command{
	Use:   "init [directory]",
	Short: "Initialize a Clawdapus project scaffold",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		targetDir := "."
		if len(args) == 1 {
			targetDir = args[0]
		}

		absTarget, err := filepath.Abs(targetDir)
		if err != nil {
			return fmt.Errorf("resolve target directory %q: %w", targetDir, err)
		}

		opts := initScaffoldOptions{
			ProjectName: initProject,
			AgentName:   initAgent,
			ClawType:    initType,
			Model:       initModel,
			Cllama:      initCllama,
			Platform:    initPlatform,
			VolumeSpec:  initVolumeSpec,
		}

		return runInitWithOptions(absTarget, initFromPath, opts, shouldPromptInteractively())
	},
}

func runInit(dir, fromPath string) error {
	return runInitWithOptions(dir, fromPath, initScaffoldOptions{}, false)
}

func runInitWithOptions(dir, fromPath string, opts initScaffoldOptions, interactive bool) error {
	if fromPath != "" {
		// Intentional: keep --from migration on the legacy flat scaffold.
		// We do not force existing OpenClaw users into the canonical agents/<name>/ layout.
		return runInitFrom(dir, fromPath)
	}
	return runInitScaffold(dir, opts, interactive)
}

func runInitFrom(dir, fromPath string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create target directory: %w", err)
	}

	// Look for openclaw.json at fromPath, fromPath/openclaw.json, or fromPath/config/openclaw.json.
	configPath := findOpenClawConfig(fromPath)
	if configPath == "" {
		return fmt.Errorf("no openclaw.json found in %q or its subdirectories", fromPath)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}

	var config map[string]interface{}
	if err := json.Unmarshal(data, &config); err != nil {
		return fmt.Errorf("parse openclaw.json: %w", err)
	}

	channels := detectChannels(config)
	models := detectModels(config)

	return generateMigrationScaffold(dir, channels, models)
}

func findOpenClawConfig(path string) string {
	// Check direct path.
	if filepath.Base(path) == "openclaw.json" {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	// Check in path/openclaw.json.
	candidate := filepath.Join(path, "openclaw.json")
	if _, err := os.Stat(candidate); err == nil {
		return candidate
	}
	// Check in path/config/openclaw.json.
	candidate = filepath.Join(path, "config", "openclaw.json")
	if _, err := os.Stat(candidate); err == nil {
		return candidate
	}
	return ""
}

func detectChannels(config map[string]interface{}) []string {
	channels := make([]string, 0)
	channelsMap, ok := config["channels"].(map[string]interface{})
	if !ok {
		return channels
	}
	for platform, v := range channelsMap {
		m, ok := v.(map[string]interface{})
		if !ok {
			continue
		}
		if enabled, ok := m["enabled"].(bool); ok && enabled {
			channels = append(channels, platform)
		}
	}
	sort.Strings(channels)
	return channels
}

func detectModels(config map[string]interface{}) []string {
	models := make([]string, 0)
	// OpenClaw stores model as agents.defaults.model.primary: "provider/model-name".
	if primary, ok := getNestedString(config, "agents", "defaults", "model", "primary"); ok && primary != "" {
		models = append(models, primary)
	}
	return models
}

func getNestedString(m map[string]interface{}, keys ...string) (string, bool) {
	if len(keys) == 0 {
		return "", false
	}
	current := m
	for _, key := range keys[:len(keys)-1] {
		next, ok := current[key].(map[string]interface{})
		if !ok {
			return "", false
		}
		current = next
	}
	s, ok := current[keys[len(keys)-1]].(string)
	return s, ok
}

func generateMigrationScaffold(dir string, channels, models []string) error {
	modelDirective := defaultModel
	if len(models) > 0 {
		modelDirective = models[0]
	}

	// Build HANDLE lines.
	var handleLines []string
	for _, ch := range channels {
		handleLines = append(handleLines, fmt.Sprintf("HANDLE %s", ch))
	}
	// Add commented-out handles for platforms not detected.
	allPlatforms := []string{"discord", "telegram", "slack"}
	for _, p := range allPlatforms {
		found := false
		for _, ch := range channels {
			if ch == p {
				found = true
				break
			}
		}
		if !found {
			handleLines = append(handleLines, fmt.Sprintf("# HANDLE %s", p))
		}
	}

	clawfileContent := fmt.Sprintf(`FROM openclaw:latest

CLAW_TYPE openclaw
AGENT AGENTS.md

MODEL primary %s

CLLAMA passthrough

%s
`, modelDirective, strings.Join(handleLines, "\n"))

	// Build handle block for claw-pod.yml.
	var handleBlock strings.Builder
	for _, ch := range channels {
		tokenVar := strings.ToUpper(ch) + "_BOT_ID"
		handleBlock.WriteString(fmt.Sprintf("      %s:\n", ch))
		handleBlock.WriteString(fmt.Sprintf("        id: \"${%s}\"\n", tokenVar))
		handleBlock.WriteString("        username: \"my-bot\"\n")
	}

	// Build environment block.
	var envLines []string
	for _, ch := range channels {
		tokenVar := strings.ToUpper(ch) + "_BOT_TOKEN"
		envLines = append(envLines, fmt.Sprintf("      %s: \"${%s}\"", tokenVar, tokenVar))
	}

	handlesSection := ""
	if len(channels) > 0 {
		handlesSection = fmt.Sprintf("      handles:\n%s", handleBlock.String())
	}

	envSection := ""
	if len(envLines) > 0 {
		envSection = fmt.Sprintf("    environment:\n%s\n", strings.Join(envLines, "\n"))
	}

	podContent := fmt.Sprintf(`services:
  my-agent:
    image: my-claw:latest
    x-claw:
      agent: ./AGENTS.md
      cllama: passthrough
      cllama-env:
        OPENROUTER_API_KEY: "${OPENROUTER_API_KEY}"
%s%s`, handlesSection, envSection)

	// Build .env.example with detected platforms uncommented.
	var envExampleLines []string
	envExampleLines = append(envExampleLines, "# LLM Provider (required — used by cllama proxy, never by agent directly)")
	envExampleLines = append(envExampleLines, "OPENROUTER_API_KEY=sk-or-...")
	envExampleLines = append(envExampleLines, "")

	enabledSet := make(map[string]bool)
	for _, ch := range channels {
		enabledSet[ch] = true
	}

	envExampleLines = append(envExampleLines, "# Platform credentials")
	for _, p := range allPlatforms {
		prefix := "# "
		if enabledSet[p] {
			prefix = ""
		}
		upper := strings.ToUpper(p)
		envExampleLines = append(envExampleLines, fmt.Sprintf("%s%s_BOT_TOKEN=", prefix, upper))
		envExampleLines = append(envExampleLines, fmt.Sprintf("%s%s_BOT_ID=", prefix, upper))
	}
	envExampleLines = append(envExampleLines, "")

	files := map[string]string{
		"Clawfile":     clawfileContent,
		"claw-pod.yml": podContent,
		"AGENTS.md": `# Agent Contract

You are a helpful assistant. Follow these rules:

1. Be concise and direct
2. Stay on topic
3. Ask for clarification when instructions are ambiguous
`,
		".env.example": strings.Join(envExampleLines, "\n"),
	}

	for name := range files {
		path := filepath.Join(dir, name)
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("%s already exists; refusing to overwrite (delete it first or use a new directory)", name)
		}
	}

	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return fmt.Errorf("write %s: %w", name, err)
		}
		fmt.Printf("[claw] created %s\n", name)
	}

	fmt.Printf("\n[claw] migrated from OpenClaw config (detected: %s)\n", strings.Join(channels, ", "))
	fmt.Println("[claw] scaffold ready. Next steps:")
	fmt.Println("  1. cp .env.example .env && edit .env")
	fmt.Println("  2. edit AGENTS.md (your bot's behavioral contract)")
	fmt.Println("  3. claw build -t my-claw .")
	fmt.Println("  4. claw up -d")
	return nil
}

func runInitScaffold(dir string, opts initScaffoldOptions, interactive bool) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create target directory: %w", err)
	}

	cfg, err := resolveInitConfig(dir, opts, interactive)
	if err != nil {
		return err
	}

	agentDirRel := filepath.Join("agents", cfg.AgentName)
	agentDir := filepath.Join(dir, agentDirRel)
	clawfilePath := filepath.Join(agentDir, "Clawfile")
	agentsPath := filepath.Join(agentDir, "AGENTS.md")
	podPath := filepath.Join(dir, "claw-pod.yml")
	envExamplePath := filepath.Join(dir, ".env.example")

	coreTargets := []string{clawfilePath, agentsPath, podPath, envExamplePath}
	for _, target := range coreTargets {
		if _, err := os.Stat(target); err == nil {
			rel, relErr := filepath.Rel(dir, target)
			if relErr != nil {
				rel = target
			}
			return fmt.Errorf("%s already exists; refusing to overwrite (delete it first or use a new directory)", rel)
		} else if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("check existing %s: %w", target, err)
		}
	}

	if err := os.MkdirAll(filepath.Join(agentDir, "skills"), 0o755); err != nil {
		return fmt.Errorf("create agent directory: %w", err)
	}

	files := map[string]string{
		clawfilePath:   renderAgentClawfile(cfg.ClawType, cfg.Model, cfg.Cllama, cfg.Platform),
		agentsPath:     defaultAgentContract(),
		podPath:        renderInitPod(cfg),
		envExamplePath: renderInitEnvExample(cfg),
	}

	for path, content := range files {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			return fmt.Errorf("write %s: %w", path, err)
		}
		rel, _ := filepath.Rel(dir, path)
		fmt.Printf("[claw] created %s\n", filepath.ToSlash(rel))
	}

	gitignorePath := filepath.Join(dir, ".gitignore")
	_, gitignoreExistedErr := os.Stat(gitignorePath)
	gitignoreExisted := gitignoreExistedErr == nil
	added, err := appendMissingGitignoreEntries(gitignorePath, []string{".env", "*.generated.*"})
	if err != nil {
		return err
	}
	if gitignoreExisted && len(added) > 0 {
		fmt.Printf("[claw] updated %s (added: %s)\n", ".gitignore", strings.Join(added, ", "))
	} else if len(added) > 0 {
		fmt.Printf("[claw] created %s\n", ".gitignore")
	}

	imageTag := fmt.Sprintf("%s-%s:latest", cfg.ProjectName, cfg.AgentName)
	fmt.Println("\n[claw] scaffold ready. Next steps:")
	fmt.Println("  1. cp .env.example .env && edit .env")
	fmt.Printf("  2. claw build -t %s ./agents/%s\n", imageTag, cfg.AgentName)
	fmt.Println("  3. claw up -d")
	return nil
}

func resolveInitConfig(dir string, opts initScaffoldOptions, interactive bool) (*initResolvedConfig, error) {
	projectDefault := filepath.Base(dir)
	if projectDefault == "." || projectDefault == string(filepath.Separator) || projectDefault == "" {
		projectDefault = "my-project"
	}

	cfg := &initResolvedConfig{
		ProjectName: strings.TrimSpace(opts.ProjectName),
		AgentName:   strings.TrimSpace(opts.AgentName),
		ClawType:    strings.TrimSpace(opts.ClawType),
		Model:       strings.TrimSpace(opts.Model),
		Cllama:      strings.TrimSpace(opts.Cllama),
		Platform:    strings.TrimSpace(opts.Platform),
	}

	reader := bufio.NewReader(os.Stdin)

	if interactive {
		fmt.Println("🐙 Initializing Clawdapus project")
	}

	if cfg.ProjectName == "" {
		if interactive {
			v, err := promptText(reader, os.Stdout, "Project name", projectDefault)
			if err != nil {
				return nil, fmt.Errorf("prompt project name: %w", err)
			}
			cfg.ProjectName = v
		} else {
			cfg.ProjectName = projectDefault
		}
	}

	if cfg.AgentName == "" {
		if interactive {
			v, err := promptText(reader, os.Stdout, "Agent name", defaultAgentName)
			if err != nil {
				return nil, fmt.Errorf("prompt agent name: %w", err)
			}
			cfg.AgentName = v
		} else {
			cfg.AgentName = defaultAgentName
		}
	}

	if cfg.ClawType == "" {
		if interactive {
			v, err := promptSelect(reader, os.Stdout, "Claw type", []string{"openclaw", "generic"}, 0)
			if err != nil {
				return nil, fmt.Errorf("prompt claw type: %w", err)
			}
			cfg.ClawType = v
		} else {
			cfg.ClawType = defaultClawType
		}
	}

	if cfg.Model == "" {
		if interactive {
			v, err := promptText(reader, os.Stdout, "Model (provider/model)", defaultModel)
			if err != nil {
				return nil, fmt.Errorf("prompt model: %w", err)
			}
			cfg.Model = v
		} else {
			cfg.Model = defaultModel
		}
	}

	if cfg.Cllama == "" {
		if interactive {
			v, err := promptSelect(reader, os.Stdout, "Use cllama proxy?", []string{"yes", "no"}, 0)
			if err != nil {
				return nil, fmt.Errorf("prompt cllama: %w", err)
			}
			cfg.Cllama = v
		} else {
			cfg.Cllama = "yes"
		}
	}

	if cfg.Platform == "" {
		if interactive {
			v, err := promptSelect(reader, os.Stdout, "Platform", []string{"discord", "slack", "telegram", "none"}, 0)
			if err != nil {
				return nil, fmt.Errorf("prompt platform: %w", err)
			}
			cfg.Platform = v
		} else {
			cfg.Platform = defaultPlatform
		}
	}

	cllama, err := parseCllamaChoice(cfg.Cllama, defaultCllamaType)
	if err != nil {
		return nil, err
	}
	if cllama == "inherit" {
		return nil, fmt.Errorf("--cllama=inherit is not valid for claw init")
	}
	cfg.Cllama = cllama

	platform, err := parsePlatform(cfg.Platform)
	if err != nil {
		return nil, err
	}
	cfg.Platform = platform

	clawType, err := parseClawType(cfg.ClawType)
	if err != nil {
		return nil, err
	}
	cfg.ClawType = clawType

	if err := validateEntityName("project", cfg.ProjectName); err != nil {
		return nil, err
	}
	if err := validateEntityName("agent", cfg.AgentName); err != nil {
		return nil, err
	}
	if strings.TrimSpace(cfg.Model) == "" {
		return nil, fmt.Errorf("model is required")
	}

	volumeName, volumeMode, err := parseVolumeSpec(opts.VolumeSpec)
	if err != nil {
		return nil, err
	}
	if opts.VolumeSpec == "" && interactive {
		shared, promptErr := promptYesNo(reader, os.Stdout, "Create a shared volume?", false)
		if promptErr != nil {
			return nil, fmt.Errorf("prompt shared volume: %w", promptErr)
		}
		if shared {
			vName, promptErr := promptText(reader, os.Stdout, "Shared volume name", "shared")
			if promptErr != nil {
				return nil, fmt.Errorf("prompt shared volume name: %w", promptErr)
			}
			vMode, promptErr := promptSelect(reader, os.Stdout, "Volume access", []string{"read-write", "read-only"}, 0)
			if promptErr != nil {
				return nil, fmt.Errorf("prompt shared volume access: %w", promptErr)
			}
			volumeName = vName
			volumeMode = vMode
			if _, _, parseErr := parseVolumeSpec(volumeName + ":" + volumeMode); parseErr != nil {
				return nil, parseErr
			}
		}
	}
	cfg.VolumeName = volumeName
	cfg.VolumeMode = volumeMode

	return cfg, nil
}

func renderAgentClawfile(clawType, model, cllama, platform string) string {
	var b strings.Builder
	b.WriteString("FROM openclaw:latest\n\n")
	b.WriteString("CLAW_TYPE ")
	b.WriteString(clawType)
	b.WriteString("\n")
	b.WriteString("AGENT AGENTS.md\n\n")
	b.WriteString("MODEL primary ")
	b.WriteString(model)
	b.WriteString("\n")
	if cllama != "" {
		b.WriteString("\nCLLAMA ")
		b.WriteString(cllama)
		b.WriteString("\n")
	}
	if platform != "none" {
		b.WriteString("\nHANDLE ")
		b.WriteString(platform)
		b.WriteString("\n")
	}
	return b.String()
}

func renderInitPod(cfg *initResolvedConfig) string {
	var b strings.Builder
	b.WriteString("x-claw:\n")
	b.WriteString("  pod: ")
	b.WriteString(cfg.ProjectName)
	b.WriteString("\n")
	b.WriteString("services:\n")
	b.WriteString("  ")
	b.WriteString(cfg.AgentName)
	b.WriteString(":\n")
	b.WriteString("    image: ")
	b.WriteString(cfg.ProjectName)
	b.WriteString("-")
	b.WriteString(cfg.AgentName)
	b.WriteString(":latest\n")
	b.WriteString("    build:\n")
	b.WriteString("      context: ./agents/")
	b.WriteString(cfg.AgentName)
	b.WriteString("\n")
	b.WriteString("    x-claw:\n")
	b.WriteString("      agent: ./agents/")
	b.WriteString(cfg.AgentName)
	b.WriteString("/AGENTS.md\n")
	if cfg.Cllama != "" {
		b.WriteString("      cllama: ")
		b.WriteString(cfg.Cllama)
		b.WriteString("\n")
		b.WriteString("      cllama-env:\n")
		b.WriteString("        OPENROUTER_API_KEY: \"${OPENROUTER_API_KEY}\"\n")
	}
	if cfg.Platform != "none" {
		b.WriteString("      handles:\n")
		b.WriteString("        ")
		b.WriteString(cfg.Platform)
		b.WriteString(":\n")
		b.WriteString("          id: \"${")
		b.WriteString(platformIDKey(cfg.Platform))
		b.WriteString("}\"\n")
		b.WriteString("          username: \"")
		b.WriteString(cfg.AgentName)
		b.WriteString("\"\n")
		if cfg.Platform == "discord" {
			b.WriteString("          guilds:\n")
			b.WriteString("            - id: \"${DISCORD_GUILD_ID}\"\n")
		}
		if cfg.VolumeName != "" {
			b.WriteString("      surfaces:\n")
			b.WriteString("        - \"volume://")
			b.WriteString(cfg.VolumeName)
			b.WriteString(" ")
			b.WriteString(cfg.VolumeMode)
			b.WriteString("\"\n")
		}
		b.WriteString("    environment:\n")
		tokenKey := platformTokenKey(cfg.Platform)
		idKey := platformIDKey(cfg.Platform)
		if tokenKey != "" {
			b.WriteString("      ")
			b.WriteString(tokenKey)
			b.WriteString(": \"${")
			b.WriteString(tokenKey)
			b.WriteString("}\"\n")
		}
		if idKey != "" {
			b.WriteString("      ")
			b.WriteString(idKey)
			b.WriteString(": \"${")
			b.WriteString(idKey)
			b.WriteString("}\"\n")
		}
	} else if cfg.VolumeName != "" {
		b.WriteString("      surfaces:\n")
		b.WriteString("        - \"volume://")
		b.WriteString(cfg.VolumeName)
		b.WriteString(" ")
		b.WriteString(cfg.VolumeMode)
		b.WriteString("\"\n")
	}
	if cfg.VolumeName != "" {
		b.WriteString("volumes:\n")
		b.WriteString("  ")
		b.WriteString(cfg.VolumeName)
		b.WriteString(": {}\n")
	}
	return b.String()
}

func renderInitEnvExample(cfg *initResolvedConfig) string {
	lines := make([]string, 0, 12)
	if cfg.Cllama != "" || strings.Contains(cfg.Model, "openrouter/") {
		lines = append(lines, "# LLM Provider (required — used by cllama proxy, never by agent directly)")
		lines = append(lines, "OPENROUTER_API_KEY=sk-or-...")
		lines = append(lines, "")
	}

	if cfg.Platform != "none" {
		lines = append(lines, "# Platform credentials")
		lines = append(lines, platformTokenKey(cfg.Platform)+"=")
		lines = append(lines, platformIDKey(cfg.Platform)+"=")
		if cfg.Platform == "discord" {
			lines = append(lines, "DISCORD_GUILD_ID=")
		}
		lines = append(lines, "")
	}

	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n")
}

func defaultAgentContract() string {
	return `# Agent Contract

You are a helpful assistant. Follow these rules:

1. Be concise and direct
2. Stay on topic
3. Ask for clarification when instructions are ambiguous
`
}

func init() {
	initCmd.Flags().StringVar(&initFromPath, "from", "", "Path to existing OpenClaw config directory to migrate from")
	initCmd.Flags().StringVar(&initProject, "project", "", "Project name used for x-claw.pod and image prefix")
	initCmd.Flags().StringVar(&initAgent, "agent", "", "Primary agent name (service + directory name)")
	initCmd.Flags().StringVar(&initType, "type", "", "Claw type (openclaw, generic)")
	initCmd.Flags().StringVar(&initModel, "model", "", "Primary model (provider/model)")
	initCmd.Flags().StringVar(&initCllama, "cllama", "", "Use cllama proxy (yes/no)")
	initCmd.Flags().StringVar(&initPlatform, "platform", "", "Platform handle (discord, slack, telegram, none)")
	initCmd.Flags().StringVar(&initVolumeSpec, "volume", "", "Shared volume spec (<name> or <name>:<mode>)")
	rootCmd.AddCommand(initCmd)
}
