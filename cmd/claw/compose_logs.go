package main

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/spf13/cobra"
)

var composeLogsFollow bool

var composeLogsCmd = &cobra.Command{
	Use:   "logs [service]",
	Short: "Stream logs from a Claw pod",
	RunE: func(cmd *cobra.Command, args []string) error {
		generatedPath, err := resolveComposeGeneratedPath()
		if err != nil {
			return err
		}

		composeArgs := []string{"compose", "-f", generatedPath, "logs"}
		if composeLogsFollow {
			composeArgs = append(composeArgs, "-f")
		}
		composeArgs = append(composeArgs, args...)

		dockerCmd := exec.Command("docker", composeArgs...)
		dockerCmd.Stdout = os.Stdout
		dockerCmd.Stderr = os.Stderr
		if err := dockerCmd.Run(); err != nil {
			return fmt.Errorf("docker compose logs failed: %w", err)
		}
		return nil
	},
}

func init() {
	composeLogsCmd.Flags().BoolVar(&composeLogsFollow, "follow", false, "Follow log output")
	rootCmd.AddCommand(composeLogsCmd)
}
