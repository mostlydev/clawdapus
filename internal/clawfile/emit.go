package clawfile

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/moby/buildkit/frontend/dockerfile/parser"
)

type RunnerProvenance struct {
	DriverName string
	Alias      string
	ImageRef   string
	BuiltRef   string
	VersionTag string
	ImageID    string
	RecipeSHA  string
}

func Emit(result *ParseResult, runner *RunnerProvenance) (string, error) {
	if result == nil || result.Config == nil {
		return "", fmt.Errorf("emit: parse result is nil")
	}

	var b strings.Builder
	generated := buildGeneratedLines(result.Config, runner)

	for _, node := range result.DockerNodes {
		original := rewriteDockerNode(strings.TrimSuffix(node.Original, "\n"), node, runner)
		if original != "" {
			b.WriteString(original)
			b.WriteString("\n")
		}
	}

	if len(generated) > 0 {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		for _, line := range generated {
			b.WriteString(line)
			b.WriteString("\n")
		}
	}

	return b.String(), nil
}

func rewriteDockerNode(original string, node *parser.Node, runner *RunnerProvenance) string {
	if runner == nil || node == nil || strings.TrimSpace(runner.ImageRef) == "" || strings.TrimSpace(runner.BuiltRef) == "" {
		return original
	}
	if !strings.EqualFold(strings.TrimSpace(node.Value), "from") {
		return original
	}
	fields := strings.Fields(original)
	if len(fields) < 2 {
		return original
	}
	if fields[1] != runner.ImageRef {
		return original
	}
	return strings.Replace(original, fields[1], runner.BuiltRef, 1)
}

func buildGeneratedLines(config *ClawConfig, runner *RunnerProvenance) []string {
	lines := make([]string, 0)
	lines = append(lines, buildLabelLines(config)...)
	lines = append(lines, buildRunnerLabelLines(runner)...)
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
	for i, c := range config.Cllama {
		lines = append(lines, formatLabel(fmt.Sprintf("claw.cllama.%d", i), c))
	}
	if config.Persona != "" {
		lines = append(lines, formatLabel("claw.persona.default", config.Persona))
	}

	modelSlots := sortedMapKeys(config.Models)
	for _, slot := range modelSlots {
		lines = append(lines, formatLabel("claw.model."+slot, config.Models[slot]))
	}

	for _, platform := range config.Handles {
		lines = append(lines, formatLabel("claw.handle."+platform, "true"))
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

	for i, skill := range config.Skills {
		lines = append(lines, formatLabel(fmt.Sprintf("claw.skill.%d", i), skill))
	}

	for i, configure := range config.Configures {
		lines = append(lines, formatLabel(fmt.Sprintf("claw.configure.%d", i), configure))
	}

	for i, inv := range config.Invocations {
		// Encode as "<schedule>\t<command>" — tab separates the two fields.
		// Schedule is exactly 5 space-separated fields; command may contain spaces.
		encoded := inv.Schedule + "\t" + inv.Command
		lines = append(lines, formatLabel(fmt.Sprintf("claw.invoke.%d", i), encoded))
	}

	return lines
}

func buildInfraLines(_ *ClawConfig) []string {
	return nil
}

func buildRunnerLabelLines(runner *RunnerProvenance) []string {
	if runner == nil {
		return nil
	}

	lines := make([]string, 0, 4)
	if runner.DriverName != "" {
		lines = append(lines, formatLabel("claw.runner.driver", runner.DriverName))
	}
	if runner.BuiltRef != "" {
		lines = append(lines, formatLabel("claw.runner.built-against", runner.BuiltRef))
	}
	if runner.ImageID != "" {
		lines = append(lines, formatLabel("claw.runner.image-id", runner.ImageID))
	}
	if runner.RecipeSHA != "" {
		lines = append(lines, formatLabel("claw.runner.recipe-sha", runner.RecipeSHA))
	}
	return lines
}

func formatLabel(key string, value string) string {
	return "LABEL " + key + "=" + strconv.Quote(value)
}

func sortedMapKeys(in map[string]string) []string {
	keys := make([]string, 0, len(in))
	for key := range in {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
