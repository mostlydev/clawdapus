package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

var skillCmd = &cobra.Command{
	Use:   "skill",
	Short: "Manage Clawdapus AI skills",
}

var skillInstallCmd = &cobra.Command{
	Use:   "install",
	Short: "Install the Clawdapus CLI skill for your coding agents",
	Long: `Installs the clawdapus-cli skill to ~/.claude/skills/ and ~/.agents/skills/.
This gives Claude Code, Codex, Gemini, OpenCode, and other agents full
operational knowledge of the claw CLI, Clawfile syntax, claw-pod.yml,
cllama proxy wiring, driver semantics, and troubleshooting patterns.

Once installed, the skill auto-updates whenever you run any claw command.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		content, err := embeddedSkillContent()
		if err != nil {
			return fmt.Errorf("reading embedded skill: %w", err)
		}

		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("finding home directory: %w", err)
		}

		targets := skillTargetDirs(home)
		for _, dir := range targets {
			if err := writeSkillFile(dir, content); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "  installed → %s\n", filepath.Join(dir, "SKILL.md"))
		}

		if err := writeSkillMarker(home); err != nil {
			return fmt.Errorf("writing marker: %w", err)
		}

		fmt.Fprintln(cmd.OutOrStdout(), "\nSkill will auto-update on future claw runs.")
		return nil
	},
}

func skillTargetDirs(home string) []string {
	return []string{
		filepath.Join(home, ".claude", "skills", "clawdapus-cli"),
		filepath.Join(home, ".agents", "skills", "clawdapus-cli"),
	}
}

func writeSkillFile(dir string, content []byte) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}
	path := filepath.Join(dir, "SKILL.md")
	return os.WriteFile(path, content, 0o644)
}

func init() {
	skillCmd.AddCommand(skillInstallCmd)
	rootCmd.AddCommand(skillCmd)
}
