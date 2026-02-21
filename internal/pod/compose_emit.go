package pod

import (
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/mostlydev/clawdapus/internal/driver"
	"gopkg.in/yaml.v3"
)

// composeFile is the YAML serialization target for compose.generated.yml.
type composeFile struct {
	Services map[string]*composeService `yaml:"services"`
	Volumes  map[string]interface{}     `yaml:"volumes,omitempty"`
	Networks map[string]interface{}     `yaml:"networks,omitempty"`
}

type composeService struct {
	Image       string              `yaml:"image"`
	ReadOnly    bool                `yaml:"read_only,omitempty"`
	Tmpfs       []string            `yaml:"tmpfs,omitempty"`
	Volumes     []string            `yaml:"volumes,omitempty"`
	Environment map[string]string   `yaml:"environment,omitempty"`
	Restart     string              `yaml:"restart,omitempty"`
	Healthcheck *composeHealthcheck `yaml:"healthcheck,omitempty"`
	Labels      map[string]string   `yaml:"labels,omitempty"`
}

type composeHealthcheck struct {
	Test     []string `yaml:"test"`
	Interval string   `yaml:"interval"`
	Timeout  string   `yaml:"timeout"`
	Retries  int      `yaml:"retries"`
}

// EmitCompose generates a compose.generated.yml string from pod definition and
// driver materialization results. Output is deterministic (sorted service names).
func EmitCompose(p *Pod, results map[string]*driver.MaterializeResult) (string, error) {
	cf := &composeFile{
		Services: make(map[string]*composeService),
		Volumes:  make(map[string]interface{}),
	}

	// Sort service names for deterministic output
	serviceNames := sortedServiceNames(p.Services)

	for _, name := range serviceNames {
		svc := p.Services[name]
		result := results[name]
		if result == nil {
			// Fail-closed: safe defaults when driver result is absent
			result = &driver.MaterializeResult{
				ReadOnly: true,
				Restart:  "on-failure",
			}
		}

		count := 1
		if svc.Claw != nil && svc.Claw.Count > 0 {
			count = svc.Claw.Count
		}

		// Collect volume surfaces for this service
		var volumeMounts []string
		if svc.Claw != nil {
			for _, raw := range svc.Claw.Surfaces {
				parts := strings.Fields(raw)
				if len(parts) == 0 {
					continue
				}
				parsed, err := url.Parse(parts[0])
				if err != nil || parsed.Scheme != "volume" {
					continue
				}
				volName := parsed.Host
				if volName == "" {
					volName = parsed.Opaque
				}
				accessMode := "rw"
				if len(parts) > 1 && strings.Contains(parts[1], "read-only") {
					accessMode = "ro"
				}
				cf.Volumes[volName] = nil // top-level volume declaration
				volumeMounts = append(volumeMounts, fmt.Sprintf("%s:/mnt/%s:%s", volName, volName, accessMode))
			}
		}

		// Expand count into ordinal-named services
		for ordinal := 0; ordinal < count; ordinal++ {
			serviceName := name
			if count > 1 {
				serviceName = fmt.Sprintf("%s-%d", name, ordinal)
			}

			cs := &composeService{
				Image:    svc.Image,
				ReadOnly: result.ReadOnly,
				Restart:  result.Restart,
				Labels: map[string]string{
					"claw.pod":     p.Name,
					"claw.service": name,
				},
			}

			if count > 1 {
				cs.Labels["claw.ordinal"] = fmt.Sprintf("%d", ordinal)
			}

			// Tmpfs
			if len(result.Tmpfs) > 0 {
				cs.Tmpfs = make([]string, len(result.Tmpfs))
				copy(cs.Tmpfs, result.Tmpfs)
			}

			// Mounts from driver
			var mounts []string
			for _, m := range result.Mounts {
				mode := "rw"
				if m.ReadOnly {
					mode = "ro"
				}
				mounts = append(mounts, fmt.Sprintf("%s:%s:%s", m.HostPath, m.ContainerPath, mode))
			}
			mounts = append(mounts, volumeMounts...)
			if len(mounts) > 0 {
				cs.Volumes = mounts
			}

			// Environment: merge pod env + driver env (driver wins on conflict)
			env := make(map[string]string)
			for k, v := range svc.Environment {
				env[k] = v
			}
			for k, v := range result.Environment {
				env[k] = v
			}
			if len(env) > 0 {
				cs.Environment = env
			}

			// Healthcheck
			if result.Healthcheck != nil {
				cs.Healthcheck = &composeHealthcheck{
					Test:     result.Healthcheck.Test,
					Interval: result.Healthcheck.Interval,
					Timeout:  result.Healthcheck.Timeout,
					Retries:  result.Healthcheck.Retries,
				}
			}

			cf.Services[serviceName] = cs
		}
	}

	// Remove empty volumes map
	if len(cf.Volumes) == 0 {
		cf.Volumes = nil
	}

	data, err := yaml.Marshal(cf)
	if err != nil {
		return "", fmt.Errorf("emit compose: %w", err)
	}

	return string(data), nil
}

func sortedServiceNames(services map[string]*Service) []string {
	names := make([]string, 0, len(services))
	for name := range services {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
