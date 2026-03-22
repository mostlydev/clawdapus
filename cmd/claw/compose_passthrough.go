package main

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/spf13/cobra"
)

var composePassthroughCmd = &cobra.Command{
	Use:                "compose [subcommand] [args...]",
	Short:              "Run any docker compose command against the generated compose file",
	DisableFlagParsing: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			return fmt.Errorf("usage: claw compose <subcommand> [args...]")
		}

		generatedPath, err := resolveComposeGeneratedPath()
		if err != nil {
			return err
		}

		composeArgs := append([]string{"compose", "-f", generatedPath}, args...)
		dockerCmd := exec.Command("docker", composeArgs...)
		dockerCmd.Stdin = os.Stdin
		dockerCmd.Stdout = os.Stdout
		dockerCmd.Stderr = os.Stderr
		if err := dockerCmd.Run(); err != nil {
			return fmt.Errorf("docker compose %s failed: %w", args[0], err)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(composePassthroughCmd)
}
