package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

var composePodFile string
var skipStalenessCheck bool

type composeStalenessPolicy int

const (
	composeStalenessError composeStalenessPolicy = iota
	composeStalenessWarn
	composeStalenessIgnore
)

func resolveComposeGeneratedPath() (string, error) {
	policy := composeStalenessError
	if skipStalenessCheck {
		policy = composeStalenessIgnore
	}
	return resolveComposeGeneratedPathWithPolicy(policy, nil)
}

func resolveComposeGeneratedPathAllowStale(warningWriter io.Writer) (string, error) {
	return resolveComposeGeneratedPathWithPolicy(composeStalenessWarn, warningWriter)
}

func resolveComposeGeneratedPathWithPolicy(policy composeStalenessPolicy, warningWriter io.Writer) (string, error) {
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
		if err := checkComposeGeneratedStaleness(absPodFile, genInfo, policy, warningWriter); err != nil {
			return "", err
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
	podPath := filepath.Join(cwd, "claw-pod.yml")
	if err := checkComposeGeneratedStaleness(podPath, genInfo, policy, warningWriter); err != nil {
		return "", err
	}
	return generatedPath, nil
}

func checkComposeGeneratedStaleness(podPath string, genInfo os.FileInfo, policy composeStalenessPolicy, warningWriter io.Writer) error {
	if policy == composeStalenessIgnore {
		return nil
	}

	podInfo, err := os.Stat(podPath)
	if err != nil {
		return nil
	}
	if !podInfo.ModTime().After(genInfo.ModTime()) {
		return nil
	}

	if policy == composeStalenessWarn {
		if warningWriter != nil {
			fmt.Fprintln(warningWriter, "[claw] warning: claw-pod.yml is newer than compose.generated.yml; building against the previously generated compose. Run 'claw up' afterward to apply the new pod config.")
		}
		return nil
	}

	return fmt.Errorf("claw-pod.yml is newer than compose.generated.yml — run 'claw up' to regenerate")
}

func init() {
	// Register -f as a persistent flag on root so all lifecycle commands inherit it.
	rootCmd.PersistentFlags().StringVarP(&composePodFile, "file", "f", "", "Path to claw-pod.yml (locates compose.generated.yml next to it)")
}
