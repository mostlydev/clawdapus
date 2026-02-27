package main

import (
	"fmt"
	"os"
	"path/filepath"

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

// Stub — full implementation in Task 7.
func runInitFrom(dir, fromPath string) error {
	return fmt.Errorf("--from migration not yet implemented (see: claw init --help)")
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
