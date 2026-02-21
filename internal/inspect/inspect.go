package inspect

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/docker/docker/client"
)

type ClawInfo struct {
	ClawType   string
	Agent      string
	Models     map[string]string
	Cllama     string
	Persona    string
	Surfaces   []string
	Skills     []string
	Privileges map[string]string
	Configures []string
	SkillEmit  string // claw.skill.emit label: path to skill file inside image
}

func ParseLabels(labels map[string]string) *ClawInfo {
	info := &ClawInfo{
		Models:     make(map[string]string),
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

	surfaces := make([]indexedEntry, 0)
	skills := make([]indexedEntry, 0)
	configures := make([]indexedEntry, 0)

	for key, value := range labels {
		if !strings.HasPrefix(key, "claw.") {
			continue
		}

		switch {
		case key == "claw.type":
			info.ClawType = value
		case key == "claw.agent.file":
			info.Agent = value
		case strings.HasPrefix(key, "claw.model."):
			slot := strings.TrimPrefix(key, "claw.model.")
			info.Models[slot] = value
		case key == "claw.cllama.default":
			info.Cllama = value
		case key == "claw.persona.default":
			info.Persona = value
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
		}
	}

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

	return info
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
