package clawfile

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/moby/buildkit/frontend/dockerfile/parser"
)

func Emit(result *ParseResult) (string, error) {
	if result == nil || result.Config == nil {
		return "", fmt.Errorf("emit: parse result is nil")
	}

	var b strings.Builder
	lastFromIndex := findLastFrom(result.DockerNodes)
	generated := buildGeneratedLines(result.Config)

	for i, node := range result.DockerNodes {
		original := strings.TrimSuffix(node.Original, "\n")
		if original != "" {
			b.WriteString(original)
			b.WriteString("\n")
		}

		if i == lastFromIndex && len(generated) > 0 {
			b.WriteString("\n")
			for _, line := range generated {
				b.WriteString(line)
				b.WriteString("\n")
			}
		}
	}

	return b.String(), nil
}

func findLastFrom(nodes []*parser.Node) int {
	last := -1
	for i, node := range nodes {
		if strings.EqualFold(node.Value, "from") {
			last = i
		}
	}
	return last
}

func buildGeneratedLines(config *ClawConfig) []string {
	lines := make([]string, 0)
	lines = append(lines, buildLabelLines(config)...)
	lines = append(lines, buildInfraLines(config)...)
	return lines
}

func buildLabelLines(config *ClawConfig) []string {
	lines := make([]string, 0)

	if config.ClawType != "" {
		lines = append(lines, formatLabel("claw.type", config.ClawType))
	}
	if config.Agent != "" {
		lines = append(lines, formatLabel("claw.agent.file", config.Agent))
	}
	if config.Cllama != "" {
		lines = append(lines, formatLabel("claw.cllama.default", config.Cllama))
	}
	if config.Persona != "" {
		lines = append(lines, formatLabel("claw.persona.default", config.Persona))
	}

	modelSlots := sortedMapKeys(config.Models)
	for _, slot := range modelSlots {
		lines = append(lines, formatLabel("claw.model."+slot, config.Models[slot]))
	}

	for i, surface := range config.Surfaces {
		lines = append(lines, formatLabel(fmt.Sprintf("claw.surface.%d", i), surface.Raw))
	}

	privilegeModes := sortedMapKeys(config.Privileges)
	for _, mode := range privilegeModes {
		lines = append(lines, formatLabel("claw.privilege."+mode, config.Privileges[mode]))
	}

	for i, track := range config.Tracks {
		lines = append(lines, formatLabel(fmt.Sprintf("claw.track.%d", i), track))
	}

	return lines
}

func buildInfraLines(config *ClawConfig) []string {
	lines := make([]string, 0)

	if len(config.Invocations) > 0 {
		lines = append(lines, "RUN mkdir -p /etc/cron.d")
		cronLines := make([]string, 0, len(config.Invocations))
		for _, invocation := range config.Invocations {
			cronLines = append(cronLines, fmt.Sprintf("%s root %s", invocation.Schedule, invocation.Command))
		}
		lines = append(lines, fmt.Sprintf("RUN printf '%%s\\n' %s > /etc/cron.d/claw && chmod 0644 /etc/cron.d/claw", quoteShellArgs(cronLines)))
	}

	if len(config.Configures) > 0 {
		lines = append(lines, "RUN mkdir -p /claw")
		scriptLines := make([]string, 0, len(config.Configures)+2)
		scriptLines = append(scriptLines, "#!/bin/sh", "set -e")
		scriptLines = append(scriptLines, config.Configures...)
		lines = append(lines, fmt.Sprintf("RUN printf '%%s\\n' %s > /claw/configure.sh && chmod +x /claw/configure.sh", quoteShellArgs(scriptLines)))
	}

	return lines
}

func formatLabel(key string, value string) string {
	return "LABEL " + key + "=" + strconv.Quote(value)
}

func quoteShellArgs(lines []string) string {
	quoted := make([]string, 0, len(lines))
	for _, line := range lines {
		quoted = append(quoted, shellSingleQuote(line))
	}
	return strings.Join(quoted, " ")
}

func shellSingleQuote(in string) string {
	return "'" + strings.ReplaceAll(in, "'", `'"'"'`) + "'"
}

func sortedMapKeys(in map[string]string) []string {
	keys := make([]string, 0, len(in))
	for key := range in {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
