package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/spf13/cobra"
)

var composeLogsFollow bool

var composeLogsCmd = &cobra.Command{
	Use:   "logs [service]",
	Short: "Stream logs from a Claw pod",
	RunE: func(cmd *cobra.Command, args []string) error {
		podDir := "."
		generatedPath := filepath.Join(podDir, "compose.generated.yml")

		if _, err := os.Stat(generatedPath); err != nil {
			return fmt.Errorf("no compose.generated.yml found (run 'claw compose up' first)")
		}

		composeArgs := []string{"compose", "-f", generatedPath, "logs"}
		if composeLogsFollow {
			composeArgs = append(composeArgs, "-f")
		}
		composeArgs = append(composeArgs, args...)

		dockerCmd := exec.Command("docker", composeArgs...)
		dockerCmd.Stdout = os.Stdout
		dockerCmd.Stderr = os.Stderr
		return dockerCmd.Run()
	},
}

func init() {
	composeLogsCmd.Flags().BoolVarP(&composeLogsFollow, "follow", "f", false, "Follow log output")
	composeCmd.AddCommand(composeLogsCmd)
}
