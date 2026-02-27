package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

var initFromPath string

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Scaffold a new Claw project",
	RunE: func(cmd *cobra.Command, args []string) error {
		dir, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("get working directory: %w", err)
		}
		return runInit(dir, initFromPath)
	},
}

func runInit(dir, fromPath string) error {
	if fromPath != "" {
		return runInitFrom(dir, fromPath)
	}
	return runInitScaffold(dir)
}

func runInitFrom(dir, fromPath string) error {
	// Look for openclaw.json at fromPath, fromPath/openclaw.json, or fromPath/config/openclaw.json
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

	// Detect enabled channels
	channels := detectChannels(config)
	models := detectModels(config)

	// Generate scaffold with detected configuration
	return generateMigrationScaffold(dir, channels, models)
}

func findOpenClawConfig(path string) string {
	// Check direct path
	if filepath.Base(path) == "openclaw.json" {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	// Check in path/openclaw.json
	candidate := filepath.Join(path, "openclaw.json")
	if _, err := os.Stat(candidate); err == nil {
		return candidate
	}
	// Check in path/config/openclaw.json
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
	// OpenClaw stores model as agents.defaults.model.primary: "provider/model-name"
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

func getNestedMap(m map[string]interface{}, keys ...string) (map[string]interface{}, bool) {
	current := m
	for _, key := range keys {
		next, ok := current[key].(map[string]interface{})
		if !ok {
			return nil, false
		}
		current = next
	}
	return current, true
}

func generateMigrationScaffold(dir string, channels, models []string) error {
	modelDirective := "openrouter/anthropic/claude-sonnet-4"
	if len(models) > 0 {
		modelDirective = models[0]
	}

	// Build HANDLE lines
	var handleLines []string
	for _, ch := range channels {
		handleLines = append(handleLines, fmt.Sprintf("HANDLE %s", ch))
	}
	// Add commented-out handles for platforms not detected
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

	// Build handle block for claw-pod.yml
	var handleBlock strings.Builder
	for _, ch := range channels {
		tokenVar := strings.ToUpper(ch) + "_BOT_ID"
		handleBlock.WriteString(fmt.Sprintf("      %s:\n", ch))
		handleBlock.WriteString(fmt.Sprintf("        id: \"${%s}\"\n", tokenVar))
		handleBlock.WriteString(fmt.Sprintf("        username: \"my-bot\"\n"))
	}

	// Build environment block
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

	// Build .env.example with detected platforms uncommented
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

	// Check for existing files first
	for name := range files {
		path := filepath.Join(dir, name)
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("%s already exists; refusing to overwrite (delete it first or use a new directory)", name)
		}
	}

	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
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

func runInitScaffold(dir string) error {
	files := map[string]string{
		"Clawfile": `FROM openclaw:latest

CLAW_TYPE openclaw
AGENT AGENTS.md

MODEL primary openrouter/anthropic/claude-sonnet-4

CLLAMA passthrough

# Uncomment the platforms you use:
# HANDLE discord
# HANDLE telegram
# HANDLE slack
`,
		"claw-pod.yml": `services:
  my-agent:
    image: my-claw:latest
    x-claw:
      agent: ./AGENTS.md
      cllama: passthrough
      cllama-env:
        OPENROUTER_API_KEY: "${OPENROUTER_API_KEY}"
      # Uncomment and configure your platform:
      # handles:
      #   discord:
      #     id: "${DISCORD_BOT_ID}"
      #     username: "my-bot"
      #   telegram:
      #     id: "${TELEGRAM_BOT_ID}"
      #     username: "my_bot"
      #   slack:
      #     id: "${SLACK_BOT_ID}"
      #     username: "my-bot"
    environment:
      # Platform tokens (uncomment as needed):
      # DISCORD_BOT_TOKEN: "${DISCORD_BOT_TOKEN}"
      # TELEGRAM_BOT_TOKEN: "${TELEGRAM_BOT_TOKEN}"
      # SLACK_BOT_TOKEN: "${SLACK_BOT_TOKEN}"
`,
		"AGENTS.md": `# Agent Contract

You are a helpful assistant. Follow these rules:

1. Be concise and direct
2. Stay on topic
3. Ask for clarification when instructions are ambiguous
`,
		".env.example": `# LLM Provider (required — used by cllama proxy, never by agent directly)
OPENROUTER_API_KEY=sk-or-...

# Platform credentials (uncomment the one you use)
# DISCORD_BOT_TOKEN=
# DISCORD_BOT_ID=
# TELEGRAM_BOT_TOKEN=
# TELEGRAM_BOT_ID=
# SLACK_BOT_TOKEN=
# SLACK_BOT_ID=
`,
	}

	// Check for existing files first
	for name := range files {
		path := filepath.Join(dir, name)
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("%s already exists; refusing to overwrite (delete it first or use a new directory)", name)
		}
	}

	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			return fmt.Errorf("write %s: %w", name, err)
		}
		fmt.Printf("[claw] created %s\n", name)
	}

	fmt.Println("\n[claw] scaffold ready. Next steps:")
	fmt.Println("  1. cp .env.example .env && edit .env")
	fmt.Println("  2. edit AGENTS.md (your bot's behavioral contract)")
	fmt.Println("  3. claw build -t my-claw .")
	fmt.Println("  4. claw up -d")
	return nil
}

func init() {
	initCmd.Flags().StringVar(&initFromPath, "from", "", "Path to existing OpenClaw config directory to migrate from")
	rootCmd.AddCommand(initCmd)
}
