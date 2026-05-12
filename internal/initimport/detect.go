package initimport

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

func Detect(fromPath string, override SourceKind) (Descriptor, error) {
	if strings.TrimSpace(fromPath) == "" {
		return Descriptor{}, fmt.Errorf("--from path is required")
	}
	openclawPath := findOpenClawConfig(fromPath)
	hermesPath := findHermesConfig(fromPath)

	switch override {
	case SourceOpenClaw:
		if openclawPath == "" {
			return Descriptor{}, fmt.Errorf("--source=openclaw but no openclaw.json found in %q", fromPath)
		}
		return readOpenClaw(openclawPath)
	case SourceHermes:
		if hermesPath == "" {
			return Descriptor{}, fmt.Errorf("--source=hermes but no Hermes config.yaml found in %q", fromPath)
		}
		return readHermes(hermesPath)
	case "":
	default:
		return Descriptor{}, fmt.Errorf("unsupported --source %q (allowed: openclaw, hermes)", override)
	}

	if openclawPath != "" && hermesPath != "" {
		return Descriptor{}, fmt.Errorf("ambiguous import source %q: found both openclaw.json and Hermes config.yaml; pass --source openclaw or --source hermes", fromPath)
	}
	if openclawPath != "" {
		return readOpenClaw(openclawPath)
	}
	if hermesPath != "" {
		return readHermes(hermesPath)
	}
	return Descriptor{}, fmt.Errorf("no OpenClaw or Hermes config found in %q", fromPath)
}

func findOpenClawConfig(path string) string {
	if info, err := os.Stat(path); err == nil && !info.IsDir() && filepath.Base(path) == "openclaw.json" {
		return path
	}
	for _, candidate := range []string{
		filepath.Join(path, "openclaw.json"),
		filepath.Join(path, "config", "openclaw.json"),
	} {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	return ""
}

func findHermesConfig(path string) string {
	if info, err := os.Stat(path); err == nil && !info.IsDir() {
		base := filepath.Base(path)
		if (base == "config.yaml" || base == "config.yml") && looksLikeHermesConfig(path) {
			return path
		}
	}
	for _, candidate := range []string{
		filepath.Join(path, "config.yaml"),
		filepath.Join(path, "config.yml"),
	} {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() && looksLikeHermesConfig(candidate) {
			return candidate
		}
	}
	return ""
}

func looksLikeHermesConfig(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var raw map[string]any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return false
	}
	model, ok := raw["model"].(map[string]any)
	if !ok {
		return false
	}
	_, hasDefault := model["default"]
	_, hasProvider := model["provider"]
	return hasDefault || hasProvider
}
