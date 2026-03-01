package main

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mostlydev/clawdapus/internal/clawctl"
	"github.com/mostlydev/clawdapus/internal/driver"
	"github.com/mostlydev/clawdapus/internal/pod"
)

func writePodManifest(runtimeDir string, p *pod.Pod, resolved map[string]*driver.ResolvedClaw, proxies []pod.CllamaProxyConfig) (string, error) {
	manifest := buildPodManifest(p, resolved, proxies)
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode pod manifest: %w", err)
	}

	path := filepath.Join(runtimeDir, "pod-manifest.json")
	if err := writeRuntimeFile(path, append(data, '\n'), 0644); err != nil {
		return "", fmt.Errorf("write pod manifest %q: %w", path, err)
	}
	return path, nil
}

func buildPodManifest(p *pod.Pod, resolved map[string]*driver.ResolvedClaw, proxies []pod.CllamaProxyConfig) *clawctl.PodManifest {
	out := &clawctl.PodManifest{
		PodName:  p.Name,
		Services: make(map[string]clawctl.ServiceManifest, len(p.Services)),
	}

	names := make([]string, 0, len(p.Services))
	for name := range p.Services {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		svc := p.Services[name]
		manifest := clawctl.ServiceManifest{
			ImageRef: svc.Image,
			Count:    1,
		}
		if svc.Claw != nil && svc.Claw.Count > 0 {
			manifest.Count = svc.Claw.Count
		}

		if rc, ok := resolved[name]; ok && rc != nil {
			manifest.ClawType = rc.ClawType
			manifest.Agent = rc.Agent
			manifest.Models = cloneStringMap(rc.Models)
			manifest.Handles = rc.Handles
			manifest.PeerHandles = rc.PeerHandles
			manifest.Surfaces = toSurfaceManifest(rc.Surfaces)
			manifest.Skills = resolvedSkillNames(rc.Skills)
			manifest.Invocations = append([]driver.Invocation(nil), rc.Invocations...)
			manifest.Cllama = append([]string(nil), rc.Cllama...)
			if rc.Count > 0 {
				manifest.Count = rc.Count
			}
		} else if svc.Claw != nil {
			manifest.Handles = svc.Claw.Handles
			manifest.Surfaces = toSurfaceManifest(svc.Claw.Surfaces)
			manifest.Cllama = append([]string(nil), svc.Claw.Cllama...)
		}

		out.Services[name] = manifest
	}

	if len(proxies) > 0 {
		out.Proxies = make([]clawctl.ProxyManifest, 0, len(proxies))
		for _, proxy := range proxies {
			out.Proxies = append(out.Proxies, clawctl.ProxyManifest{
				ProxyType:   proxy.ProxyType,
				ServiceName: "cllama-" + strings.TrimSpace(proxy.ProxyType),
				Image:       proxy.Image,
			})
		}
		sort.Slice(out.Proxies, func(i, j int) bool {
			return out.Proxies[i].ServiceName < out.Proxies[j].ServiceName
		})
	}

	return out
}

func toSurfaceManifest(in []driver.ResolvedSurface) []clawctl.SurfaceManifest {
	if len(in) == 0 {
		return nil
	}
	out := make([]clawctl.SurfaceManifest, 0, len(in))
	for _, s := range in {
		out = append(out, clawctl.SurfaceManifest{
			Scheme:        s.Scheme,
			Target:        s.Target,
			AccessMode:    s.AccessMode,
			Ports:         append([]string(nil), s.Ports...),
			ChannelConfig: s.ChannelConfig,
		})
	}
	return out
}

func resolvedSkillNames(in []driver.ResolvedSkill) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, len(in))
	for _, sk := range in {
		name := strings.TrimSpace(sk.Name)
		if name == "" {
			continue
		}
		out = append(out, name)
	}
	return out
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
