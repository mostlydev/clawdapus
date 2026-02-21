package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/spf13/cobra"
)

var composePsCmd = &cobra.Command{
	Use:   "ps",
	Short: "Show status of Claw pod containers",
	RunE: func(cmd *cobra.Command, args []string) error {
		podDir := "."
		generatedPath := filepath.Join(podDir, "compose.generated.yml")

		if _, err := os.Stat(generatedPath); err != nil {
			return fmt.Errorf("no compose.generated.yml found (run 'claw compose up' first)")
		}

		dockerCmd := exec.Command("docker", "compose", "-f", generatedPath, "ps")
		dockerCmd.Stdout = os.Stdout
		dockerCmd.Stderr = os.Stderr
		return dockerCmd.Run()
	},
}

func init() {
	composeCmd.AddCommand(composePsCmd)
}
