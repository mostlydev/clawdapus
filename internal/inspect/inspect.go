package inspect

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/docker/docker/client"
	"github.com/moby/buildkit/frontend/dockerfile/parser"
)

// InspectInvocation is an invocation entry parsed from a claw.invoke.N image label.
type InspectInvocation struct {
	Schedule string
	Command  string
}

type ClawInfo struct {
	ClawType     string
	Agent        string
	Models       map[string]string
	Cllama       []string
	Persona      string
	Handles      []string
	Surfaces     []string
	Skills       []string
	Privileges   map[string]string
	Configures   []string
	Invocations  []InspectInvocation
	SkillEmit    string // claw.skill.emit label: path to skill file inside image
	DescribePath string // claw.describe label: path to a structured service descriptor inside image
	RunnerDriver string
	RunnerBuilt  string
	RunnerImage  string
	RunnerRecipe string
}

func ParseLabels(labels map[string]string) *ClawInfo {
	info := &ClawInfo{
		Models:     make(map[string]string),
		Handles:    make([]string, 0),
		Surfaces:   make([]string, 0),
		Skills:     make([]string, 0),
		Privileges: make(map[string]string),
		Configures: make([]string, 0),
	}

	type indexedEntry struct {
		Index int
		Key   string
		Value string
	}

	handles := make([]string, 0)
	surfaces := make([]indexedEntry, 0)
	skills := make([]indexedEntry, 0)
	configures := make([]indexedEntry, 0)
	invokeEntries := make([]indexedEntry, 0)
	cllamaByIndex := make(map[int]string)

	for key, value := range labels {
		if !strings.HasPrefix(key, "claw.") {
			continue
		}

		switch {
		case key == "claw.type":
			info.ClawType = value
		case key == "claw.runner.driver":
			info.RunnerDriver = value
		case key == "claw.runner.built-against":
			info.RunnerBuilt = value
		case key == "claw.runner.image-id":
			info.RunnerImage = value
		case key == "claw.runner.recipe-sha":
			info.RunnerRecipe = value
		case key == "claw.agent.file":
			info.Agent = value
		case strings.HasPrefix(key, "claw.model."):
			slot := strings.TrimPrefix(key, "claw.model.")
			info.Models[slot] = value
		case key == "claw.cllama.default":
			if _, exists := cllamaByIndex[0]; !exists {
				cllamaByIndex[0] = value
			}
		case strings.HasPrefix(key, "claw.cllama."):
			suffix := strings.TrimPrefix(key, "claw.cllama.")
			if parsed, err := strconv.Atoi(suffix); err == nil {
				cllamaByIndex[parsed] = value
			}
		case key == "claw.persona.default":
			info.Persona = value
		case strings.HasPrefix(key, "claw.handle."):
			if value == "true" || value == "1" {
				platform := strings.TrimPrefix(key, "claw.handle.")
				handles = append(handles, platform)
			}
		case strings.HasPrefix(key, "claw.privilege."):
			mode := strings.TrimPrefix(key, "claw.privilege.")
			info.Privileges[mode] = value
		case strings.HasPrefix(key, "claw.surface."):
			index := maxInt()
			suffix := strings.TrimPrefix(key, "claw.surface.")
			if parsed, err := strconv.Atoi(suffix); err == nil {
				index = parsed
			}
			surfaces = append(surfaces, indexedEntry{
				Index: index,
				Key:   key,
				Value: value,
			})
		case key == "claw.skill.emit":
			info.SkillEmit = value
		case key == "claw.describe":
			info.DescribePath = value
		case strings.HasPrefix(key, "claw.skill."):
			index := maxInt()
			suffix := strings.TrimPrefix(key, "claw.skill.")
			if parsed, err := strconv.Atoi(suffix); err == nil {
				index = parsed
			}
			skills = append(skills, indexedEntry{
				Index: index,
				Key:   key,
				Value: value,
			})
		case strings.HasPrefix(key, "claw.configure."):
			index := maxInt()
			suffix := strings.TrimPrefix(key, "claw.configure.")
			if parsed, err := strconv.Atoi(suffix); err == nil {
				index = parsed
			}
			configures = append(configures, indexedEntry{
				Index: index,
				Key:   key,
				Value: value,
			})
		case strings.HasPrefix(key, "claw.invoke."):
			index := maxInt()
			suffix := strings.TrimPrefix(key, "claw.invoke.")
			if parsed, err := strconv.Atoi(suffix); err == nil {
				index = parsed
			}
			invokeEntries = append(invokeEntries, indexedEntry{
				Index: index,
				Key:   key,
				Value: value,
			})
		}
	}

	sort.Strings(handles)
	info.Handles = handles

	sort.Slice(surfaces, func(i int, j int) bool {
		if surfaces[i].Index == surfaces[j].Index {
			return surfaces[i].Key < surfaces[j].Key
		}
		return surfaces[i].Index < surfaces[j].Index
	})

	for _, surface := range surfaces {
		info.Surfaces = append(info.Surfaces, surface.Value)
	}

	sort.Slice(skills, func(i int, j int) bool {
		if skills[i].Index == skills[j].Index {
			return skills[i].Key < skills[j].Key
		}
		return skills[i].Index < skills[j].Index
	})

	for _, skill := range skills {
		info.Skills = append(info.Skills, skill.Value)
	}

	sort.Slice(configures, func(i int, j int) bool {
		if configures[i].Index == configures[j].Index {
			return configures[i].Key < configures[j].Key
		}
		return configures[i].Index < configures[j].Index
	})

	for _, configure := range configures {
		info.Configures = append(info.Configures, configure.Value)
	}

	sort.Slice(invokeEntries, func(i, j int) bool {
		if invokeEntries[i].Index == invokeEntries[j].Index {
			return invokeEntries[i].Key < invokeEntries[j].Key
		}
		return invokeEntries[i].Index < invokeEntries[j].Index
	})
	for _, e := range invokeEntries {
		tab := strings.IndexByte(e.Value, '\t')
		if tab > 0 {
			info.Invocations = append(info.Invocations, InspectInvocation{
				Schedule: e.Value[:tab],
				Command:  e.Value[tab+1:],
			})
		}
	}

	if len(cllamaByIndex) > 0 {
		indices := make([]int, 0, len(cllamaByIndex))
		for idx := range cllamaByIndex {
			indices = append(indices, idx)
		}
		sort.Ints(indices)
		for _, idx := range indices {
			info.Cllama = append(info.Cllama, cllamaByIndex[idx])
		}
	}

	return info
}

func LoadFromDockerfile(path string) (*ClawInfo, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open dockerfile %q: %w", path, err)
	}
	defer f.Close()

	parsed, err := parser.Parse(f)
	if err != nil {
		return nil, fmt.Errorf("parse dockerfile %q: %w", path, err)
	}

	labels := make(map[string]string)
	for _, node := range parsed.AST.Children {
		if !strings.EqualFold(strings.TrimSpace(node.Value), "label") {
			continue
		}
		if err := collectLabelNode(labels, node); err != nil {
			return nil, fmt.Errorf("parse LABEL in %q at line %d: %w", path, node.StartLine, err)
		}
	}
	if len(labels) == 0 {
		return nil, nil
	}

	info := ParseLabels(labels)
	if info.ClawType == "" &&
		info.RunnerDriver == "" &&
		info.RunnerBuilt == "" &&
		info.RunnerImage == "" &&
		info.RunnerRecipe == "" &&
		info.SkillEmit == "" &&
		info.DescribePath == "" &&
		len(info.Surfaces) == 0 &&
		len(info.Skills) == 0 &&
		len(info.Configures) == 0 &&
		len(info.Models) == 0 &&
		len(info.Handles) == 0 &&
		len(info.Privileges) == 0 {
		return nil, nil
	}
	return info, nil
}

func collectLabelNode(labels map[string]string, node *parser.Node) error {
	args := make([]string, 0)
	for current := node.Next; current != nil; current = current.Next {
		args = append(args, current.Value)
	}
	if len(args) == 0 {
		return nil
	}

	for i := 0; i < len(args); {
		if key, value, ok := strings.Cut(args[i], "="); ok && key != "" {
			labels[key] = trimDockerfileLabelValue(value)
			i++
			continue
		}
		if i+1 >= len(args) {
			return fmt.Errorf("odd LABEL argument count")
		}
		labels[args[i]] = trimDockerfileLabelValue(args[i+1])
		i += 2
	}
	return nil
}

func trimDockerfileLabelValue(value string) string {
	if len(value) >= 2 {
		if (value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'') {
			if unquoted, err := strconv.Unquote(value); err == nil {
				return unquoted
			}
		}
	}
	return value
}

func Inspect(imageRef string) (*ClawInfo, error) {
	docker, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("create docker client: %w", err)
	}
	defer docker.Close()

	inspect, _, err := docker.ImageInspectWithRaw(context.Background(), imageRef)
	if err != nil {
		return nil, fmt.Errorf("inspect image %q: %w", imageRef, err)
	}

	labels := map[string]string{}
	if inspect.Config != nil && inspect.Config.Labels != nil {
		labels = inspect.Config.Labels
	}

	return ParseLabels(labels), nil
}

func maxInt() int {
	return int(^uint(0) >> 1)
}
