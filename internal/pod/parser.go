package pod

import (
	"fmt"
	"io"

	"gopkg.in/yaml.v3"
)

// rawPod is the YAML deserialization target.
type rawPod struct {
	XClaw    rawPodClaw            `yaml:"x-claw"`
	Services map[string]rawService `yaml:"services"`
}

type rawPodClaw struct {
	Pod    string `yaml:"pod"`
	Master string `yaml:"master"`
}

type rawService struct {
	Image       string            `yaml:"image"`
	XClaw       *rawClawBlock     `yaml:"x-claw"`
	Environment map[string]string `yaml:"environment"`
	Expose      []string          `yaml:"expose"`
}

type rawClawBlock struct {
	Agent    string   `yaml:"agent"`
	Persona  string   `yaml:"persona"`
	Cllama   string   `yaml:"cllama"`
	Count    int      `yaml:"count"`
	Surfaces []string `yaml:"surfaces"`
	Skills   []string `yaml:"skills"`
}

// Parse reads a claw-pod.yml from the given reader.
func Parse(r io.Reader) (*Pod, error) {
	var raw rawPod
	decoder := yaml.NewDecoder(r)
	if err := decoder.Decode(&raw); err != nil {
		return nil, fmt.Errorf("parse claw-pod.yml: %w", err)
	}

	pod := &Pod{
		Name:     raw.XClaw.Pod,
		Services: make(map[string]*Service, len(raw.Services)),
	}

	for name, svc := range raw.Services {
		expose := svc.Expose
		if expose == nil {
			expose = make([]string, 0)
		}
		service := &Service{
			Image:       svc.Image,
			Environment: svc.Environment,
			Expose:      expose,
		}
		if svc.XClaw != nil {
			count := svc.XClaw.Count
			if count < 1 {
				count = 1
			}
			surfaces := svc.XClaw.Surfaces
			if surfaces == nil {
				surfaces = make([]string, 0)
			}
			skills := svc.XClaw.Skills
			if skills == nil {
				skills = make([]string, 0)
			}
			service.Claw = &ClawBlock{
				Agent:    svc.XClaw.Agent,
				Persona:  svc.XClaw.Persona,
				Cllama:   svc.XClaw.Cllama,
				Count:    count,
				Surfaces: surfaces,
				Skills:   skills,
			}
		}
		pod.Services[name] = service
	}

	return pod, nil
}
