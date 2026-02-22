package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mostlydev/clawdapus/internal/driver"
	"github.com/mostlydev/clawdapus/internal/driver/openclaw"
	"github.com/mostlydev/clawdapus/internal/inspect"
	"github.com/mostlydev/clawdapus/internal/pod"
	"github.com/mostlydev/clawdapus/internal/runtime"
)

var composeUpDetach bool

var (
	extractServiceSkillFromImage = runtime.ExtractServiceSkill
	writeRuntimeFile             = os.WriteFile
)

var composeUpCmd = &cobra.Command{
	Use:   "up [pod-file]",
	Short: "Launch a Claw pod from claw-pod.yml",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if composePodFile != "" && len(args) > 0 {
			return fmt.Errorf("pod file specified twice: use either '--file %s' or positional arg '%s', not both", composePodFile, args[0])
		}

		podFile := composePodFile
		if podFile == "" && len(args) > 0 {
			podFile = args[0]
		}
		if podFile == "" {
			podFile = "claw-pod.yml"
		}
		return runComposeUp(podFile)
	},
}

func runComposeUp(podFile string) error {
	f, err := os.Open(podFile)
	if err != nil {
		return fmt.Errorf("open pod file: %w", err)
	}
	defer f.Close()

	p, err := pod.Parse(f)
	if err != nil {
		return err
	}

	podDir, err := filepath.Abs(filepath.Dir(podFile))
	if err != nil {
		return fmt.Errorf("resolve pod directory: %w", err)
	}
	runtimeDir := filepath.Join(podDir, ".claw-runtime")
	if err := os.MkdirAll(runtimeDir, 0700); err != nil {
		return fmt.Errorf("create runtime dir: %w", err)
	}

	results := make(map[string]*driver.MaterializeResult)
	drivers := make(map[string]driver.Driver)
	resolvedClaws := make(map[string]*driver.ResolvedClaw)

	// Pre-collect all pod handles so each service can reference its peers.
	// This is a cheap pass over the already-parsed pod YAML — no image inspection needed.
	podHandles := make(map[string]map[string]*driver.HandleInfo) // service → platform → HandleInfo
	for name, svc := range p.Services {
		if svc.Claw != nil && len(svc.Claw.Handles) > 0 {
			podHandles[name] = svc.Claw.Handles
		}
	}

	for name, svc := range p.Services {
		if svc.Claw == nil {
			continue
		}

		info, err := inspect.Inspect(svc.Image)
		if err != nil {
			return fmt.Errorf("inspect image %q for service %q: %w", svc.Image, name, err)
		}

		if info.ClawType == "" {
			return fmt.Errorf("service %q: image %q has no claw.type label", name, svc.Image)
		}

		// Resolve agent contract
		agentHostPath := ""
		agentFile := info.Agent
		if svc.Claw.Agent != "" {
			contract, err := runtime.ResolveContract(podDir, svc.Claw.Agent)
			if err != nil {
				return fmt.Errorf("service %q: %w", name, err)
			}
			agentHostPath = contract.HostPath
			// Use the basename from the pod-level agent path
			agentFile = filepath.Base(svc.Claw.Agent)
		} else if agentFile != "" {
			contract, err := runtime.ResolveContract(podDir, agentFile)
			if err != nil {
				return fmt.Errorf("service %q: %w", name, err)
			}
			agentHostPath = contract.HostPath
		}

		// Surfaces are already parsed by pod.Parse() — use them directly.
		var surfaces []driver.ResolvedSurface
		if svc.Claw != nil {
			surfaces = svc.Claw.Surfaces
		}

		// Enrich service surfaces with port info from pod service definitions.
		// Merge expose: and ports: — both describe reachable container ports.
		for i := range surfaces {
			if surfaces[i].Scheme == "service" {
				if targetSvc, ok := p.Services[surfaces[i].Target]; ok {
					surfaces[i].Ports = mergedPorts(targetSvc.Expose, targetSvc.Ports)
				}
			}
		}

		svcRuntimeDir := filepath.Join(runtimeDir, name)
		if err := os.MkdirAll(svcRuntimeDir, 0700); err != nil {
			return fmt.Errorf("service %q: create service runtime dir: %w", name, err)
		}

		// Merge skills: image-level (from labels) + pod-level (from x-claw)
		imageSkills, err := runtime.ResolveSkills(podDir, info.Skills)
		if err != nil {
			return fmt.Errorf("service %q: %w", name, err)
		}
		if info.SkillEmit != "" {
			emitSkill, err := resolveSkillEmit(name, svcRuntimeDir, svc.Image, info.SkillEmit)
			if err != nil {
				return fmt.Errorf("service %q: resolve emitted skill: %w", name, err)
			}
			if emitSkill != nil {
				imageSkills = append(imageSkills, *emitSkill)
			}
		}
		generatedSkills, err := resolveServiceGeneratedSkills(svcRuntimeDir, surfaces)
		if err != nil {
			return fmt.Errorf("service %q: resolve generated service skills: %w", name, err)
		}
		handleSkills, err := resolveHandleSkills(svcRuntimeDir, svc.Claw.Handles)
		if err != nil {
			return fmt.Errorf("service %q: resolve handle skills: %w", name, err)
		}
		if len(handleSkills) > 0 {
			generatedSkills = mergeResolvedSkills(generatedSkills, handleSkills)
		}
		podSkills := make([]driver.ResolvedSkill, 0)
		if svc.Claw != nil {
			podSkills, err = runtime.ResolveSkills(podDir, svc.Claw.Skills)
			if err != nil {
				return fmt.Errorf("service %q: %w", name, err)
			}
		}
		skills := mergeResolvedSkills(imageSkills, podSkills)
		if len(generatedSkills) > 0 {
			// Pod and image skills override generated defaults.
			skills = mergeResolvedSkills(generatedSkills, skills)
		}

		// Build peer handles: all other claw services' handles, keyed by service name.
		peerHandles := make(map[string]map[string]*driver.HandleInfo)
		for peerName, peerH := range podHandles {
			if peerName != name {
				peerHandles[peerName] = peerH
			}
		}

		rc := &driver.ResolvedClaw{
			ServiceName:   name,
			ImageRef:      svc.Image,
			ClawType:      info.ClawType,
			Agent:         agentFile,
			AgentHostPath: agentHostPath,
			Models:        info.Models,
			Handles:       svc.Claw.Handles,
			PeerHandles:   peerHandles,
			Configures:    info.Configures,
			Privileges:    info.Privileges,
			Count:         svc.Claw.Count,
			Environment:   svc.Environment,
			Surfaces:      surfaces,
			Skills:        skills,
		}

		// Merge image-level invocations (from Clawfile INVOKE labels via inspect)
		for _, imgInv := range info.Invocations {
			rc.Invocations = append(rc.Invocations, driver.Invocation{
				Schedule: imgInv.Schedule,
				Message:  imgInv.Command,
			})
		}

		// Merge pod-level invocations (x-claw.invoke), resolving channel name → ID
		for _, podInv := range svc.Claw.Invoke {
			inv := driver.Invocation{
				Schedule: podInv.Schedule,
				Message:  podInv.Message,
				Name:     podInv.Name,
			}
			if podInv.To != "" {
				inv.To = resolveChannelID(svc.Claw.Handles, podInv.To)
				if inv.To == "" {
					fmt.Printf("[claw] warning: service %q: invoke channel %q not found in handles; delivery will use last channel\n", name, podInv.To)
				}
			}
			rc.Invocations = append(rc.Invocations, inv)
		}

		d, err := driver.Lookup(rc.ClawType)
		if err != nil {
			return fmt.Errorf("service %q: %w", name, err)
		}

		if err := d.Validate(rc); err != nil {
			return fmt.Errorf("service %q: validation failed: %w", name, err)
		}

		result, err := d.Materialize(rc, driver.MaterializeOpts{RuntimeDir: svcRuntimeDir, PodName: p.Name})
		if err != nil {
			return fmt.Errorf("service %q: materialization failed: %w", name, err)
		}

		// Mount individual skill files into the driver's skill directory
		if result.SkillDir != "" && len(rc.Skills) > 0 {
			for _, sk := range rc.Skills {
				result.Mounts = append(result.Mounts, driver.Mount{
					HostPath:      sk.HostPath,
					ContainerPath: filepath.Join(result.SkillDir, sk.Name),
					ReadOnly:      true,
				})
			}
		}

		results[name] = result
		drivers[name] = d
		resolvedClaws[name] = rc
		fmt.Printf("[claw] %s: validated and materialized (%s driver)\n", name, rc.ClawType)
	}

	output, err := pod.EmitCompose(p, results)
	if err != nil {
		return err
	}

	generatedPath := filepath.Join(podDir, "compose.generated.yml")
	if err := os.WriteFile(generatedPath, []byte(output), 0644); err != nil {
		return fmt.Errorf("write compose.generated.yml: %w", err)
	}
	fmt.Printf("[claw] wrote %s\n", generatedPath)

	if len(drivers) == 0 {
		fmt.Println("[claw] warning: no x-claw services found; running plain docker compose lifecycle")
	}

	if len(drivers) > 0 && !composeUpDetach {
		return fmt.Errorf("claw-managed services require detached mode for fail-closed post-apply verification; rerun with 'claw compose up -d %s'", podFile)
	}

	composeArgs := []string{"compose", "-f", generatedPath, "up"}
	if composeUpDetach {
		composeArgs = append(composeArgs, "-d")
	}

	dockerCmd := exec.Command("docker", composeArgs...)
	dockerCmd.Stdout = os.Stdout
	dockerCmd.Stderr = os.Stderr
	if err := dockerCmd.Run(); err != nil {
		return fmt.Errorf("docker compose up failed: %w", err)
	}

	// PostApply: verify every generated service container.
	for name, d := range drivers {
		rc := resolvedClaws[name]
		for _, generatedService := range expandedServiceNames(name, rc.Count) {
			containerIDs, err := resolveContainerIDs(generatedPath, generatedService)
			if err != nil {
				return fmt.Errorf("service %q: failed to resolve container ID(s): %w", generatedService, err)
			}
			for _, containerID := range containerIDs {
				if err := d.PostApply(rc, driver.PostApplyOpts{ContainerID: containerID}); err != nil {
					return fmt.Errorf("service %q: post-apply verification failed: %w", generatedService, err)
				}
				fmt.Printf("[claw] %s (%s): post-apply verified\n", generatedService, shortContainerIDForPostApply(containerID))
			}
		}
	}

	fmt.Println("[claw] pod is up")
	return nil
}

func mergeResolvedSkills(imageSkills, podSkills []driver.ResolvedSkill) []driver.ResolvedSkill {
	merged := make([]driver.ResolvedSkill, 0, len(imageSkills)+len(podSkills))
	byName := make(map[string]int, len(imageSkills))

	for _, skill := range imageSkills {
		byName[skill.Name] = len(merged)
		merged = append(merged, skill)
	}

	for _, skill := range podSkills {
		if idx, ok := byName[skill.Name]; ok {
			merged[idx] = skill
			continue
		}
		byName[skill.Name] = len(merged)
		merged = append(merged, skill)
	}

	return merged
}

func resolveSkillEmit(serviceName, runtimeDir, imageRef, emitPath string) (*driver.ResolvedSkill, error) {
	if emitPath == "" {
		return nil, nil
	}

	name := filepath.Base(emitPath)
	if name == "." || name == "/" || strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("service %q: emitted skill path %q has invalid filename", serviceName, emitPath)
	}

	emitHostPath := filepath.Join(runtimeDir, "skills", name)
	if err := os.MkdirAll(filepath.Dir(emitHostPath), 0700); err != nil {
		return nil, fmt.Errorf("service %q: create emitted-skill dir: %w", serviceName, err)
	}

	content, err := extractServiceSkillFromImage(imageRef, emitPath)
	if err != nil {
		// Extraction failure is non-fatal: warn and fall back to the generated stub skill.
		// The pod can still start; the agent gets a generic skill rather than a custom one.
		fmt.Printf("[claw] warning: service %q: could not extract emitted skill %q from %q: %v (using fallback)\n",
			serviceName, emitPath, imageRef, err)
		return nil, nil
	}
	if err := writeRuntimeFile(emitHostPath, content, 0644); err != nil {
		return nil, fmt.Errorf("write emitted skill %q: %w", emitPath, err)
	}

	return &driver.ResolvedSkill{
		Name:     name,
		HostPath: emitHostPath,
	}, nil
}

func resolveServiceGeneratedSkills(runtimeDir string, surfaces []driver.ResolvedSurface) ([]driver.ResolvedSkill, error) {
	surfaceSkillsDir := filepath.Join(runtimeDir, "skills")
	generated := make([]driver.ResolvedSkill, 0)
	seen := make(map[string]struct{}, len(surfaces))

	for _, surface := range surfaces {
		if surface.Scheme != "service" {
			continue
		}

		name := fmt.Sprintf("surface-%s.md", strings.TrimSpace(strings.ReplaceAll(surface.Target, "/", "-")))
		if name == "surface-.md" {
			return nil, fmt.Errorf("invalid service target for generated skill: %q", surface.Target)
		}
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}

		skillPath := filepath.Join(surfaceSkillsDir, name)
		if err := os.MkdirAll(filepath.Dir(skillPath), 0700); err != nil {
			return nil, fmt.Errorf("create generated skill dir: %w", err)
		}
		content := runtime.GenerateServiceSkillFallback(surface.Target, surface.Ports)
		if err := writeRuntimeFile(skillPath, []byte(content), 0644); err != nil {
			return nil, fmt.Errorf("write generated service skill %q: %w", name, err)
		}
		generated = append(generated, driver.ResolvedSkill{
			Name:     name,
			HostPath: skillPath,
		})
	}

	return generated, nil
}

// mergedPorts combines expose and ports slices, deduplicating by value.
func mergedPorts(expose, ports []string) []string {
	seen := make(map[string]struct{}, len(expose)+len(ports))
	out := make([]string, 0, len(expose)+len(ports))
	for _, p := range expose {
		if _, ok := seen[p]; !ok {
			seen[p] = struct{}{}
			out = append(out, p)
		}
	}
	for _, p := range ports {
		if _, ok := seen[p]; !ok {
			seen[p] = struct{}{}
			out = append(out, p)
		}
	}
	return out
}

func resolveHandleSkills(runtimeDir string, handles map[string]*driver.HandleInfo) ([]driver.ResolvedSkill, error) {
	if len(handles) == 0 {
		return nil, nil
	}
	skillsDir := filepath.Join(runtimeDir, "skills")
	if err := os.MkdirAll(skillsDir, 0700); err != nil {
		return nil, fmt.Errorf("create handle skill dir: %w", err)
	}
	generated := make([]driver.ResolvedSkill, 0, len(handles))
	for platform, info := range handles {
		if info == nil {
			continue
		}
		name := fmt.Sprintf("handle-%s.md", platform)
		skillPath := filepath.Join(skillsDir, name)
		content := openclaw.GenerateHandleSkill(platform, info)
		if err := writeRuntimeFile(skillPath, []byte(content), 0644); err != nil {
			return nil, fmt.Errorf("write handle skill %q: %w", name, err)
		}
		generated = append(generated, driver.ResolvedSkill{
			Name:     name,
			HostPath: skillPath,
		})
	}
	return generated, nil
}

func resolveContainerIDs(composePath, serviceName string) ([]string, error) {
	cmd := exec.Command("docker", "compose", "-f", composePath, "ps", "-q", serviceName)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("docker compose ps: %w", err)
	}
	ids := strings.Fields(string(out))
	if len(ids) == 0 {
		return nil, fmt.Errorf("no container found for service %q", serviceName)
	}
	return ids, nil
}

func expandedServiceNames(base string, count int) []string {
	if count < 1 {
		count = 1
	}
	if count == 1 {
		return []string{base}
	}
	names := make([]string, 0, count)
	for i := 0; i < count; i++ {
		names = append(names, fmt.Sprintf("%s-%d", base, i))
	}
	return names
}

func shortContainerIDForPostApply(id string) string {
	if len(id) <= 12 {
		return id
	}
	return id[:12]
}

// resolveChannelID looks up a channel by name in the discord handle's guild topology.
// Returns the channel ID if found, empty string otherwise.
// Searches all guilds in the discord handle.
func resolveChannelID(handles map[string]*driver.HandleInfo, channelName string) string {
	h, ok := handles["discord"]
	if !ok || h == nil {
		return ""
	}
	for _, g := range h.Guilds {
		for _, ch := range g.Channels {
			if ch.Name == channelName {
				return ch.ID
			}
		}
	}
	return ""
}

func init() {
	composeUpCmd.Flags().BoolVarP(&composeUpDetach, "detach", "d", false, "Run in background")
	composeCmd.AddCommand(composeUpCmd)
}
