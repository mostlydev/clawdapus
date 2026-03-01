package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"unicode"
)

const (
	defaultAgentName  = "assistant"
	defaultClawType   = "openclaw"
	defaultModel      = "openrouter/anthropic/claude-sonnet-4"
	defaultPlatform   = "discord"
	defaultCllamaType = "passthrough"
)

var validNamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]*$`)

func shouldPromptInteractively() bool {
	info, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return (info.Mode() & os.ModeCharDevice) != 0
}

func promptText(reader *bufio.Reader, out io.Writer, label, defaultValue string) (string, error) {
	if defaultValue == "" {
		fmt.Fprintf(out, "%s: ", label)
	} else {
		fmt.Fprintf(out, "%s (default: %s): ", label, defaultValue)
	}

	line, err := reader.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	value := strings.TrimSpace(line)
	if value == "" {
		value = defaultValue
	}
	return value, nil
}

func promptYesNo(reader *bufio.Reader, out io.Writer, label string, defaultYes bool) (bool, error) {
	suffix := "y/N"
	if defaultYes {
		suffix = "Y/n"
	}
	fmt.Fprintf(out, "%s [%s]: ", label, suffix)
	line, err := reader.ReadString('\n')
	if err != nil && err != io.EOF {
		return false, err
	}
	value := strings.TrimSpace(strings.ToLower(line))
	if value == "" {
		return defaultYes, nil
	}
	return value == "y" || value == "yes", nil
}

func promptSelect(reader *bufio.Reader, out io.Writer, label string, options []string, defaultIndex int) (string, error) {
	if len(options) == 0 {
		return "", fmt.Errorf("prompt %q has no options", label)
	}
	if defaultIndex < 0 || defaultIndex >= len(options) {
		defaultIndex = 0
	}

	fmt.Fprintf(out, "%s:\n", label)
	for i, option := range options {
		prefix := "  "
		if i == defaultIndex {
			prefix = "  * "
		}
		fmt.Fprintf(out, "%s%d) %s\n", prefix, i+1, option)
	}
	fmt.Fprintf(out, "Select [default: %d]: ", defaultIndex+1)

	line, err := reader.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	value := strings.TrimSpace(line)
	if value == "" {
		return options[defaultIndex], nil
	}

	// Numeric selection.
	for i := range options {
		if value == fmt.Sprintf("%d", i+1) {
			return options[i], nil
		}
	}

	// Text selection.
	for _, option := range options {
		if strings.EqualFold(value, option) {
			return option, nil
		}
	}

	return "", fmt.Errorf("invalid selection %q", value)
}

func validateEntityName(kind, name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("%s name is required", kind)
	}
	if !validNamePattern.MatchString(name) {
		return fmt.Errorf("%s name %q is invalid (allowed: letters, digits, '-', '_' and must start with alphanumeric)", kind, name)
	}
	return nil
}

func parseCllamaChoice(value string, defaultValue string) (string, error) {
	v := strings.TrimSpace(strings.ToLower(value))
	switch v {
	case "":
		return defaultValue, nil
	case "yes", "true", "1", defaultCllamaType:
		return defaultCllamaType, nil
	case "no", "none", "false", "0":
		return "", nil
	case "inherit":
		return "inherit", nil
	default:
		return "", fmt.Errorf("invalid cllama choice %q (allowed: yes/no/inherit/%s)", value, defaultCllamaType)
	}
}

func parsePlatform(value string) (string, error) {
	v := strings.ToLower(strings.TrimSpace(value))
	if v == "" {
		return "", fmt.Errorf("platform is required")
	}
	switch v {
	case "discord", "slack", "telegram", "none":
		return v, nil
	default:
		return "", fmt.Errorf("invalid platform %q (allowed: discord, slack, telegram, none)", value)
	}
}

func parseClawType(value string) (string, error) {
	v := strings.ToLower(strings.TrimSpace(value))
	switch v {
	case "":
		return "", fmt.Errorf("claw type is required")
	case "openclaw", "generic":
		return v, nil
	default:
		return "", fmt.Errorf("invalid claw type %q (allowed: openclaw, generic)", value)
	}
}

func parseVolumeSpec(spec string) (name string, mode string, err error) {
	raw := strings.TrimSpace(spec)
	if raw == "" {
		return "", "", nil
	}

	parts := strings.Split(raw, ":")
	if len(parts) > 2 {
		return "", "", fmt.Errorf("invalid volume spec %q (expected <name> or <name>:<mode>)", spec)
	}
	name = strings.TrimSpace(parts[0])
	if name == "" {
		return "", "", fmt.Errorf("invalid volume spec %q: missing volume name", spec)
	}
	if !validNamePattern.MatchString(name) {
		return "", "", fmt.Errorf("invalid volume name %q", name)
	}

	mode = "read-write"
	if len(parts) == 2 {
		mode = strings.TrimSpace(parts[1])
	}
	switch mode {
	case "read-only", "read-write":
		return name, mode, nil
	default:
		return "", "", fmt.Errorf("invalid volume mode %q (allowed: read-only, read-write)", mode)
	}
}

func envPrefixFromName(name string) string {
	var b strings.Builder
	lastUnderscore := false
	for _, r := range strings.TrimSpace(name) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(unicode.ToUpper(r))
			lastUnderscore = false
		default:
			if !lastUnderscore {
				b.WriteRune('_')
				lastUnderscore = true
			}
		}
	}
	prefix := strings.Trim(b.String(), "_")
	if prefix == "" {
		return "AGENT"
	}
	return prefix
}

func platformTokenKey(platform string) string {
	switch platform {
	case "discord":
		return "DISCORD_BOT_TOKEN"
	case "slack":
		return "SLACK_BOT_TOKEN"
	case "telegram":
		return "TELEGRAM_BOT_TOKEN"
	default:
		return ""
	}
}

func platformIDKey(platform string) string {
	switch platform {
	case "discord":
		return "DISCORD_BOT_ID"
	case "slack":
		return "SLACK_BOT_ID"
	case "telegram":
		return "TELEGRAM_BOT_ID"
	default:
		return ""
	}
}

func normalizeContractPath(path string) string {
	p := filepath.ToSlash(strings.TrimSpace(path))
	if p == "" {
		return p
	}
	if strings.HasPrefix(p, "./") {
		return p
	}
	return "./" + p
}

func appendMissingGitignoreEntries(path string, entries []string) (added []string, err error) {
	content := ""
	if data, readErr := os.ReadFile(path); readErr == nil {
		content = string(data)
	} else if !os.IsNotExist(readErr) {
		return nil, fmt.Errorf("read %s: %w", path, readErr)
	}

	lines := splitLines(content)
	existing := make(map[string]struct{}, len(lines))
	for _, line := range lines {
		existing[strings.TrimSpace(line)] = struct{}{}
	}

	for _, entry := range entries {
		if _, ok := existing[entry]; ok {
			continue
		}
		lines = append(lines, entry)
		added = append(added, entry)
		existing[entry] = struct{}{}
	}

	if len(added) == 0 {
		return nil, nil
	}

	output := strings.Join(lines, "\n")
	if !strings.HasSuffix(output, "\n") {
		output += "\n"
	}
	if err := os.WriteFile(path, []byte(output), 0o644); err != nil {
		return nil, fmt.Errorf("write %s: %w", path, err)
	}
	return added, nil
}

func appendMissingEnvExampleVars(path string, vars []string) (added []string, err error) {
	content := ""
	if data, readErr := os.ReadFile(path); readErr == nil {
		content = string(data)
	} else if !os.IsNotExist(readErr) {
		return nil, fmt.Errorf("read %s: %w", path, readErr)
	}

	lines := splitLines(content)
	existing := make(map[string]struct{}, len(lines))
	for _, line := range lines {
		key := parseEnvExampleKey(line)
		if key != "" {
			existing[key] = struct{}{}
		}
	}

	for _, key := range vars {
		if _, ok := existing[key]; ok {
			continue
		}
		lines = append(lines, key+"=")
		added = append(added, key)
		existing[key] = struct{}{}
	}

	if len(added) == 0 {
		return nil, nil
	}

	output := strings.Join(lines, "\n")
	if !strings.HasSuffix(output, "\n") {
		output += "\n"
	}
	if err := os.WriteFile(path, []byte(output), 0o644); err != nil {
		return nil, fmt.Errorf("write %s: %w", path, err)
	}
	return added, nil
}

func parseEnvExampleKey(line string) string {
	s := strings.TrimSpace(line)
	if s == "" || strings.HasPrefix(s, "#") {
		return ""
	}
	if idx := strings.Index(s, "="); idx > 0 {
		return strings.TrimSpace(s[:idx])
	}
	return ""
}

func splitLines(content string) []string {
	if content == "" {
		return []string{}
	}
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func uniqueSorted(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		v := strings.TrimSpace(value)
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	slices.Sort(out)
	return out
}
