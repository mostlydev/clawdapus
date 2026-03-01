package pod

import (
	"encoding/json"
	"fmt"
	"net"
	"sort"
	"strconv"
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
	Ports       []string            `yaml:"ports,omitempty"`
	ReadOnly    bool                `yaml:"read_only,omitempty"`
	Tmpfs       []string            `yaml:"tmpfs,omitempty"`
	Volumes     []string            `yaml:"volumes,omitempty"`
	Environment map[string]string   `yaml:"environment,omitempty"`
	Restart     string              `yaml:"restart,omitempty"`
	Healthcheck *composeHealthcheck `yaml:"healthcheck,omitempty"`
	Labels      map[string]string   `yaml:"labels,omitempty"`
	Networks    []string            `yaml:"networks,omitempty"`
}

type composeHealthcheck struct {
	Test     []string `yaml:"test"`
	Interval string   `yaml:"interval"`
	Timeout  string   `yaml:"timeout"`
	Retries  int      `yaml:"retries"`
}

type CllamaProxyConfig struct {
	ProxyType      string            // e.g. "passthrough", "policy"
	Image          string            // e.g. ghcr.io/mostlydev/cllama:latest
	ContextHostDir string            // host path for shared context dir
	AuthHostDir    string            // host path for provider auth state
	Environment    map[string]string // proxy-only env (e.g. CLAW_POD, provider keys)
	PodName        string
}

type ClawctlConfig struct {
	Image              string // e.g. ghcr.io/mostlydev/clawctl:latest
	Addr               string // e.g. :8082
	ManifestHostPath   string // host path to pod-manifest.json
	DockerSockHostPath string // host path to docker socket
	PodName            string
}

// EmitCompose generates a compose.generated.yml string from pod definition and
// driver materialization results. Output is deterministic (sorted service names).
func EmitCompose(p *Pod, results map[string]*driver.MaterializeResult, proxies ...CllamaProxyConfig) (string, error) {
	cf := &composeFile{
		Services: make(map[string]*composeService),
		Volumes:  make(map[string]interface{}),
	}

	// Track whether any claw service exists to conditionally add network
	hasClaw := false

	// Collect service surface targets — non-claw services that need claw-internal network
	serviceSurfaceTargets := make(map[string]struct{})
	for _, svc := range p.Services {
		if svc.Claw == nil {
			continue
		}
		for _, surface := range svc.Claw.Surfaces {
			if surface.Scheme == "service" {
				serviceSurfaceTargets[strings.TrimSpace(surface.Target)] = struct{}{}
			}
		}
	}

	// Compute pod-wide CLAW_HANDLE_* env vars from all claw service handles.
	// These are injected into every service (claw and non-claw) at lowest priority.
	handleEnvs := computeHandleEnvs(p.Services)

	// Sort service names for deterministic output
	serviceNames := sortedServiceNames(p.Services)

	for _, name := range serviceNames {
		svc := p.Services[name]
		isClaw := svc.Claw != nil
		result := results[name]
		if result == nil {
			// Fail-closed defaults apply only to Claw-managed services.
			if isClaw {
				result = &driver.MaterializeResult{
					ReadOnly: true,
					Restart:  "on-failure",
				}
			} else {
				result = &driver.MaterializeResult{}
			}
		}

		if isClaw {
			hasClaw = true
		}

		count := 1
		if svc.Claw != nil && svc.Claw.Count > 0 {
			count = svc.Claw.Count
		}

		// Collect volume surfaces for this service
		var volumeMounts []string
		if svc.Claw != nil {
			for _, surface := range svc.Claw.Surfaces {
				surfaceURI := surface.Scheme + "://" + surface.Target

				switch surface.Scheme {
				case "volume":
					accessMode, err := surfaceAccessMode(surface)
					if err != nil {
						return "", fmt.Errorf("service %q: %w", name, err)
					}
					volName := strings.TrimSpace(surface.Target)
					if volName == "" {
						return "", fmt.Errorf("service %q: volume surface %q is missing target", name, surfaceURI)
					}
					cf.Volumes[volName] = nil // top-level volume declaration
					volumeMounts = append(volumeMounts, fmt.Sprintf("%s:/mnt/%s:%s", volName, volName, accessMode))

				case "host":
					accessMode, err := surfaceAccessMode(surface)
					if err != nil {
						return "", fmt.Errorf("service %q: %w", name, err)
					}
					hostPath := strings.TrimSpace(surface.Target)
					if hostPath == "" {
						return "", fmt.Errorf("service %q: host surface %q is missing path", name, surfaceURI)
					}
					if !strings.HasPrefix(hostPath, "/") {
						return "", fmt.Errorf("service %q: host surface %q must use an absolute host path", name, surfaceURI)
					}
					volumeMounts = append(volumeMounts, fmt.Sprintf("%s:%s:%s", hostPath, hostPath, accessMode))

				case "service", "channel", "egress":
					if surface.Scheme == "service" {
						target := strings.TrimSpace(surface.Target)
						if target == "" {
							return "", fmt.Errorf("service %q: service surface %q has empty target", name, surfaceURI)
						}
						if _, ok := p.Services[target]; !ok {
							return "", fmt.Errorf("service %q: service surface %q targets unknown service %q", name, surfaceURI, target)
						}
					}

					if strings.TrimSpace(surface.AccessMode) != "" {
						return "", fmt.Errorf("service %q: surface %q does not support access mode %q", name, surfaceURI, surface.AccessMode)
					}
					// Topology only; no compose mounts.

				default:
					return "", fmt.Errorf("service %q: unsupported surface scheme %q in %q", name, surface.Scheme, surfaceURI)
				}
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

			if isClaw {
				cs.Networks = []string{"claw-internal"}
			} else if _, isTarget := serviceSurfaceTargets[name]; isTarget {
				cs.Networks = []string{"claw-internal"}
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

			// Environment: handle envs (lowest) < pod env < driver env (highest).
			env := make(map[string]string)
			for k, v := range handleEnvs {
				env[k] = v
			}
			for k, v := range svc.Environment {
				env[k] = v
			}
			for k, v := range result.Environment {
				env[k] = v
			}
			if isClaw && svc.Claw != nil && len(svc.Claw.CllamaTokens) > 0 {
				if tok, ok := svc.Claw.CllamaTokens[serviceName]; ok && strings.TrimSpace(tok) != "" {
					env["CLLAMA_TOKEN"] = tok
				} else if tok, ok := svc.Claw.CllamaTokens[name]; ok && strings.TrimSpace(tok) != "" {
					env["CLLAMA_TOKEN"] = tok
				}
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

	for _, proxy := range proxies {
		if strings.TrimSpace(proxy.ProxyType) == "" {
			return "", fmt.Errorf("proxy type must not be empty")
		}
		if strings.TrimSpace(proxy.Image) == "" {
			return "", fmt.Errorf("proxy image must not be empty")
		}
		if strings.TrimSpace(proxy.ContextHostDir) == "" {
			return "", fmt.Errorf("proxy %q context host dir must not be empty", proxy.ProxyType)
		}
		if strings.TrimSpace(proxy.AuthHostDir) == "" {
			return "", fmt.Errorf("proxy %q auth host dir must not be empty", proxy.ProxyType)
		}

		hasClaw = true
		serviceName := "cllama-" + proxy.ProxyType
		env := map[string]string{
			"CLAW_CONTEXT_ROOT": "/claw/context",
			"CLAW_AUTH_DIR":     "/claw/auth",
		}
		for k, v := range proxy.Environment {
			env[k] = v
		}

		cf.Services[serviceName] = &composeService{
			Image: proxy.Image,
			Ports: []string{"8081:8081"}, // operator dashboard
			Volumes: []string{
				fmt.Sprintf("%s:/claw/context:ro", proxy.ContextHostDir),
				fmt.Sprintf("%s:/claw/auth:rw", proxy.AuthHostDir),
			},
			Environment: env,
			Restart:     "on-failure",
			Healthcheck: &composeHealthcheck{
				Test:     []string{"CMD", fmt.Sprintf("/cllama-%s", proxy.ProxyType), "-healthcheck"},
				Interval: "15s",
				Timeout:  "5s",
				Retries:  3,
			},
			Labels: map[string]string{
				"claw.pod":        proxy.PodName,
				"claw.role":       "proxy",
				"claw.proxy.type": proxy.ProxyType,
				"claw.service":    serviceName,
			},
			Networks: []string{"claw-internal"},
		}
	}

	if hasClaw && p.Clawctl != nil {
		if strings.TrimSpace(p.Clawctl.Image) == "" {
			return "", fmt.Errorf("clawctl image must not be empty")
		}
		if strings.TrimSpace(p.Clawctl.ManifestHostPath) == "" {
			return "", fmt.Errorf("clawctl manifest host path must not be empty")
		}

		addr := strings.TrimSpace(p.Clawctl.Addr)
		if addr == "" {
			addr = ":8082"
		}
		port := clawctlPort(addr)
		socketPath := strings.TrimSpace(p.Clawctl.DockerSockHostPath)
		if socketPath == "" {
			socketPath = "/var/run/docker.sock"
		}

		cf.Services["clawctl"] = &composeService{
			Image:    p.Clawctl.Image,
			Ports:    []string{fmt.Sprintf("%s:%s", port, port)},
			ReadOnly: true,
			Tmpfs:    []string{"/tmp"},
			Volumes: []string{
				fmt.Sprintf("%s:/claw/pod-manifest.json:ro", p.Clawctl.ManifestHostPath),
				fmt.Sprintf("%s:/var/run/docker.sock:ro", socketPath),
			},
			Environment: map[string]string{
				"CLAWCTL_ADDR":     addr,
				"CLAWCTL_MANIFEST": "/claw/pod-manifest.json",
				"CLAW_POD":         p.Clawctl.PodName,
			},
			Restart: "on-failure",
			Healthcheck: &composeHealthcheck{
				Test:     []string{"CMD", "/clawctl", "-healthcheck"},
				Interval: "15s",
				Timeout:  "5s",
				Retries:  3,
			},
			Labels: map[string]string{
				"claw.pod":     p.Clawctl.PodName,
				"claw.role":    "dashboard",
				"claw.service": "clawctl",
			},
			Networks: []string{"claw-internal"},
		}
	}

	// Remove empty volumes map
	if len(cf.Volumes) == 0 {
		cf.Volumes = nil
	}

	// Add claw-internal network if any claw services exist.
	// Not internal: claw agents need internet access for LLM APIs, Discord, Slack, etc.
	// Service isolation is still achieved — only explicitly-attached containers can
	// communicate on this network.
	if hasClaw {
		cf.Networks = map[string]interface{}{
			"claw-internal": map[string]interface{}{},
		}
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

// computeHandleEnvs collects handles from all claw services and builds the
// pod-wide CLAW_HANDLE_<SERVICE>_<PLATFORM>_* env var map.
func computeHandleEnvs(services map[string]*Service) map[string]string {
	envs := make(map[string]string)

	// Sort service names for determinism
	names := make([]string, 0, len(services))
	for n := range services {
		names = append(names, n)
	}
	sort.Strings(names)

	for _, name := range names {
		svc := services[name]
		if svc.Claw == nil || len(svc.Claw.Handles) == 0 {
			continue
		}

		svcKey := strings.ToUpper(strings.ReplaceAll(name, "-", "_"))

		// Sort platforms for determinism
		platforms := make([]string, 0, len(svc.Claw.Handles))
		for p := range svc.Claw.Handles {
			platforms = append(platforms, p)
		}
		sort.Strings(platforms)

		for _, platform := range platforms {
			info := svc.Claw.Handles[platform]
			if info == nil {
				continue
			}
			pfx := "CLAW_HANDLE_" + svcKey + "_" + strings.ToUpper(platform)

			envs[pfx+"_ID"] = info.ID

			if info.Username != "" {
				envs[pfx+"_USERNAME"] = info.Username
			}

			if len(info.Guilds) > 0 {
				ids := make([]string, 0, len(info.Guilds))
				for _, g := range info.Guilds {
					ids = append(ids, g.ID)
				}
				envs[pfx+"_GUILDS"] = strings.Join(ids, ",")
			}

			if jsonBytes, err := json.Marshal(info); err == nil {
				envs[pfx+"_JSON"] = string(jsonBytes)
			}
		}
	}

	return envs
}

func surfaceAccessMode(surface driver.ResolvedSurface) (string, error) {
	mode := strings.ToLower(strings.TrimSpace(surface.AccessMode))
	switch mode {
	case "", "read-write", "rw":
		return "rw", nil
	case "read-only", "ro":
		return "ro", nil
	default:
		return "", fmt.Errorf("surface %s://%s has unsupported access mode %q", surface.Scheme, surface.Target, surface.AccessMode)
	}
}

func clawctlPort(addr string) string {
	port := strings.TrimSpace(addr)
	if strings.HasPrefix(addr, ":") {
		port = strings.TrimPrefix(addr, ":")
	}
	if strings.Count(addr, ":") > 0 {
		_, parsedPort, err := net.SplitHostPort(addr)
		if err == nil && strings.TrimSpace(parsedPort) != "" {
			port = parsedPort
		}
	}
	value, err := strconv.Atoi(strings.TrimSpace(port))
	if err != nil || value < 1 || value > 65535 {
		return "8082"
	}
	return strconv.Itoa(value)
}
