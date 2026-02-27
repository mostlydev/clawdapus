package main

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/spf13/cobra"
)

var composeDownCmd = &cobra.Command{
	Use:   "down",
	Short: "Stop and remove a Claw pod",
	RunE: func(cmd *cobra.Command, args []string) error {
		generatedPath, err := resolveComposeGeneratedPath()
		if err != nil {
			return err
		}

		dockerCmd := exec.Command("docker", "compose", "-f", generatedPath, "down")
		dockerCmd.Stdout = os.Stdout
		dockerCmd.Stderr = os.Stderr
		if err := dockerCmd.Run(); err != nil {
			return fmt.Errorf("docker compose down failed: %w", err)
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(composeDownCmd)
}
