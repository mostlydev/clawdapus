package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

var composePodFile string

var composeCmd = &cobra.Command{
	Use:   "compose",
	Short: "Pod lifecycle commands (up, down, ps, logs)",
}

func resolveComposeGeneratedPath() (string, error) {
	if composePodFile != "" {
		absPodFile, err := filepath.Abs(composePodFile)
		if err != nil {
			return "", fmt.Errorf("resolve pod file path %q: %w", composePodFile, err)
		}
		generatedPath := filepath.Join(filepath.Dir(absPodFile), "compose.generated.yml")
		if _, err := os.Stat(generatedPath); err != nil {
			return "", fmt.Errorf("no compose.generated.yml found next to %q (run 'claw compose up %s' first)", composePodFile, composePodFile)
		}
		return generatedPath, nil
	}

	generatedPath := "compose.generated.yml"
	if _, err := os.Stat(generatedPath); err != nil {
		return "", fmt.Errorf("no compose.generated.yml found (run 'claw compose up' first, or pass -f)")
	}
	return generatedPath, nil
}

func init() {
	rootCmd.AddCommand(composeCmd)
	composeCmd.PersistentFlags().StringVarP(&composePodFile, "file", "f", "", "Path to claw-pod.yml (locates compose.generated.yml next to it)")
}
