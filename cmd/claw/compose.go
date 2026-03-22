package main

import (
	"fmt"
	"os"
	"path/filepath"
)

var composePodFile string
var skipStalenessCheck bool

func resolveComposeGeneratedPath() (string, error) {
	if composePodFile != "" {
		absPodFile, err := filepath.Abs(composePodFile)
		if err != nil {
			return "", fmt.Errorf("resolve pod file path %q: %w", composePodFile, err)
		}
		podDir := filepath.Dir(absPodFile)
		generatedPath := filepath.Join(podDir, "compose.generated.yml")
		genInfo, err := os.Stat(generatedPath)
		if err != nil {
			return "", fmt.Errorf("no compose.generated.yml found next to %q (run 'claw up %s' first)", composePodFile, composePodFile)
		}
		if !skipStalenessCheck {
			if podInfo, err := os.Stat(absPodFile); err == nil {
				if podInfo.ModTime().After(genInfo.ModTime()) {
					return "", fmt.Errorf("claw-pod.yml is newer than compose.generated.yml — run 'claw up' to regenerate")
				}
			}
		}
		return generatedPath, nil
	}

	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("resolve current directory: %w", err)
	}

	generatedPath := filepath.Join(cwd, "compose.generated.yml")
	genInfo, err := os.Stat(generatedPath)
	if err != nil {
		return "", fmt.Errorf("no compose.generated.yml found in %q (rerun from pod directory or pass --file <path-to-claw-pod.yml>)", cwd)
	}
	if !skipStalenessCheck {
		podPath := filepath.Join(cwd, "claw-pod.yml")
		if podInfo, err := os.Stat(podPath); err == nil {
			if podInfo.ModTime().After(genInfo.ModTime()) {
				return "", fmt.Errorf("claw-pod.yml is newer than compose.generated.yml — run 'claw up' to regenerate")
			}
		}
	}
	return generatedPath, nil
}

func init() {
	// Register -f as a persistent flag on root so all lifecycle commands inherit it.
	rootCmd.PersistentFlags().StringVarP(&composePodFile, "file", "f", "", "Path to claw-pod.yml (locates compose.generated.yml next to it)")
}
