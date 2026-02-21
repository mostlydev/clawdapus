package main

import (
	"os"
	"os/exec"

	"github.com/spf13/cobra"
)

var composePsCmd = &cobra.Command{
	Use:   "ps",
	Short: "Show status of Claw pod containers",
	RunE: func(cmd *cobra.Command, args []string) error {
		generatedPath, err := resolveComposeGeneratedPath()
		if err != nil {
			return err
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
