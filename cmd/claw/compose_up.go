package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mostlydev/clawdapus/internal/build"
	"github.com/mostlydev/clawdapus/internal/cllama"
	"github.com/mostlydev/clawdapus/internal/driver"
	"github.com/mostlydev/clawdapus/internal/driver/shared"
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
	serviceRuntimeDirs := make(map[string]string)

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
		// Add channel surface skills (surface-discord.md etc.)
		channelSkills, err := resolveChannelGeneratedSkills(svcRuntimeDir, surfaces)
		if err != nil {
			return fmt.Errorf("service %q: resolve generated channel skills: %w", name, err)
		}
		if len(channelSkills) > 0 {
			generatedSkills = mergeResolvedSkills(generatedSkills, channelSkills)
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
			Cllama:        resolveCllama(info.Cllama, svc.Claw.Cllama),
		}

		// Merge image-level invocations (from Clawfile INVOKE labels via inspect)
		for _, imgInv := range info.Invocations {
			rc.Invocations = append(rc.Invocations, driver.Invocation{
				Schedule: imgInv.Schedule,
				Message:  imgInv.Command,
			})
		}

		// Merge pod-level invocations (x-claw.invoke), resolving platform/name targets to IDs when possible.
		for _, podInv := range svc.Claw.Invoke {
			inv := driver.Invocation{
				Schedule: podInv.Schedule,
				Message:  podInv.Message,
				Name:     podInv.Name,
			}
			if strings.TrimSpace(podInv.To) != "" {
				resolved := resolveInvocationTarget(svc.Claw.Handles, podInv.To)
				inv.To = resolved.To
				if resolved.Warning != "" {
					fmt.Printf("[claw] warning: service %q: %s\n", name, resolved.Warning)
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

		drivers[name] = d
		resolvedClaws[name] = rc
		serviceRuntimeDirs[name] = svcRuntimeDir
		fmt.Printf("[claw] %s: validated (%s driver)\n", name, rc.ClawType)
	}

	cllamaEnabled, cllamaAgents := detectCllama(resolvedClaws)
	proxies := make([]pod.CllamaProxyConfig, 0)
	cllamaDashboardPort := envOrDefault("CLLAMA_UI_PORT", "8181")
	if cllamaEnabled {
		proxyTypes := collectProxyTypes(resolvedClaws)
		if len(proxyTypes) > 1 {
			return fmt.Errorf("multi-proxy chaining not yet supported: found proxy types %v; Phase 4 supports one proxy type per pod", proxyTypes)
		}

		tokens := make(map[string]string)
		for _, name := range cllamaAgents {
			rc := resolvedClaws[name]
			if rc.Count > 1 {
				for i := 0; i < rc.Count; i++ {
					ordinalName := fmt.Sprintf("%s-%d", name, i)
					tokens[ordinalName] = cllama.GenerateToken(ordinalName)
				}
				rc.CllamaToken = tokens[fmt.Sprintf("%s-0", name)]
			} else {
				tokens[name] = cllama.GenerateToken(name)
				rc.CllamaToken = tokens[name]
			}

			if svc := p.Services[name]; svc != nil && svc.Claw != nil {
				if svc.Claw.CllamaTokens == nil {
					svc.Claw.CllamaTokens = make(map[string]string)
				}
				if rc.Count > 1 {
					for i := 0; i < rc.Count; i++ {
						ordinalName := fmt.Sprintf("%s-%d", name, i)
						svc.Claw.CllamaTokens[ordinalName] = tokens[ordinalName]
					}
				} else {
					svc.Claw.CllamaTokens[name] = tokens[name]
				}
			}
		}

		imageEnvCache := make(map[string]map[string]string)
		for _, name := range cllamaAgents {
			svc := p.Services[name]
			if svc == nil {
				continue
			}
			for k := range svc.Environment {
				if isProviderKey(k) {
					return fmt.Errorf("service %q: provider key %q found in pod env; cllama requires credential starvation (move provider keys to x-claw.cllama-env)", name, k)
				}
			}

			imageEnv, ok := imageEnvCache[svc.Image]
			if !ok {
				imageEnv, err = inspectImageEnv(svc.Image)
				if err != nil {
					return fmt.Errorf("service %q: inspect image env for credential starvation: %w", name, err)
				}
				imageEnvCache[svc.Image] = imageEnv
			}
			for k := range imageEnv {
				if isProviderKey(k) {
					return fmt.Errorf("service %q: provider key %q found in image-baked env; cllama requires credential starvation", name, k)
				}
			}
		}

		for _, name := range cllamaAgents {
			stripLLMKeys(resolvedClaws[name].Environment)
		}

		proxyEnv := map[string]string{
			"CLAW_POD": p.Name,
		}
		for _, name := range cllamaAgents {
			svc := p.Services[name]
			if svc == nil || svc.Claw == nil {
				continue
			}
			for k, v := range svc.Claw.CllamaEnv {
				if _, exists := proxyEnv[k]; !exists {
					proxyEnv[k] = v
				}
			}
		}

		contextInputs := make([]cllama.AgentContextInput, 0)
		for _, name := range cllamaAgents {
			rc := resolvedClaws[name]
			if rc.AgentHostPath == "" {
				return fmt.Errorf("service %q: no agent host path available for cllama context generation", name)
			}
			agentContent, err := os.ReadFile(rc.AgentHostPath)
			if err != nil {
				return fmt.Errorf("service %q: read AGENTS.md for cllama context: %w", name, err)
			}

			if rc.Count > 1 {
				for i := 0; i < rc.Count; i++ {
					ordinalName := fmt.Sprintf("%s-%d", name, i)
					ordinalRC := *rc
					ordinalRC.ServiceName = ordinalName
					md := shared.GenerateClawdapusMD(&ordinalRC, p.Name)
					contextInputs = append(contextInputs, cllama.AgentContextInput{
						AgentID:     ordinalName,
						AgentsMD:    string(agentContent),
						ClawdapusMD: md,
						Metadata: map[string]interface{}{
							"service": name,
							"ordinal": i,
							"pod":     p.Name,
							"type":    rc.ClawType,
							"token":   tokens[ordinalName],
						},
					})
				}
				continue
			}

			md := shared.GenerateClawdapusMD(rc, p.Name)
			contextInputs = append(contextInputs, cllama.AgentContextInput{
				AgentID:     name,
				AgentsMD:    string(agentContent),
				ClawdapusMD: md,
				Metadata: map[string]interface{}{
					"service": name,
					"pod":     p.Name,
					"type":    rc.ClawType,
					"token":   tokens[name],
				},
			})
		}
		if err := cllama.GenerateContextDir(runtimeDir, contextInputs); err != nil {
			return fmt.Errorf("generate cllama context dir: %w", err)
		}

		authDir := filepath.Join(runtimeDir, "proxy-auth")
		if err := os.MkdirAll(authDir, 0700); err != nil {
			return fmt.Errorf("create cllama auth dir: %w", err)
		}

		for _, proxyType := range proxyTypes {
			proxies = append(proxies, pod.CllamaProxyConfig{
				ProxyType:      proxyType,
				Image:          cllama.ProxyImageRef(proxyType),
				ContextHostDir: filepath.Join(runtimeDir, "context"),
				AuthHostDir:    authDir,
				DashboardPort:  cllamaDashboardPort,
				Environment:    proxyEnv,
				PodName:        p.Name,
			})
		}
		fmt.Printf("[claw] cllama proxies enabled: %s (agents: %s)\n",
			strings.Join(proxyTypes, ", "), strings.Join(cllamaAgents, ", "))
	}

	manifestPath, err := writePodManifest(runtimeDir, p, resolvedClaws, proxies)
	if err != nil {
		return err
	}
	fmt.Printf("[claw] wrote %s\n", manifestPath)

	p.Clawdash = &pod.ClawdashConfig{
		Image:              "ghcr.io/mostlydev/clawdash:latest",
		Addr:               envOrDefault("CLAWDASH_ADDR", ":8082"),
		ManifestHostPath:   manifestPath,
		DockerSockHostPath: "/var/run/docker.sock",
		CllamaCostsURL:     firstIf(cllamaEnabled, fmt.Sprintf("http://localhost:%s", cllamaDashboardPort)),
		PodName:            p.Name,
	}

	// Pass 2: materialize after cllama tokens/context are resolved.
	for _, name := range sortedResolvedClawNames(resolvedClaws) {
		rc := resolvedClaws[name]
		d := drivers[name]
		svcRuntimeDir := serviceRuntimeDirs[name]

		result, err := d.Materialize(rc, driver.MaterializeOpts{RuntimeDir: svcRuntimeDir, PodName: p.Name})
		if err != nil {
			return fmt.Errorf("service %q: materialization failed: %w", name, err)
		}

		if rc.CllamaToken != "" {
			if result.Environment == nil {
				result.Environment = make(map[string]string)
			}
			if _, exists := result.Environment["CLLAMA_TOKEN"]; !exists {
				result.Environment["CLLAMA_TOKEN"] = rc.CllamaToken
			}
		}

		// Mount individual skill files into the driver's skill directory
		if result.SkillDir != "" && len(rc.Skills) > 0 {
			for _, sk := range rc.Skills {
				containerPath := filepath.Join(result.SkillDir, sk.Name)
				if result.SkillLayout == "directory" {
					// Claude Code format: skills/name/SKILL.md
					stem := strings.TrimSuffix(sk.Name, filepath.Ext(sk.Name))
					containerPath = filepath.Join(result.SkillDir, stem, "SKILL.md")
				}
				result.Mounts = append(result.Mounts, driver.Mount{
					HostPath:      sk.HostPath,
					ContainerPath: containerPath,
					ReadOnly:      true,
				})
			}
		}

		results[name] = result
		fmt.Printf("[claw] %s: materialized (%s driver)\n", name, rc.ClawType)
	}

	output, err := pod.EmitCompose(p, results, proxies...)
	if err != nil {
		return err
	}

	generatedPath := filepath.Join(podDir, "compose.generated.yml")
	if err := os.WriteFile(generatedPath, []byte(output), 0644); err != nil {
		return fmt.Errorf("write compose.generated.yml: %w", err)
	}
	fmt.Printf("[claw] wrote %s\n", generatedPath)

	if err := ensureInfraImages(cllamaEnabled, proxies, p.Clawdash); err != nil {
		return err
	}

	if len(drivers) == 0 {
		fmt.Println("[claw] warning: no x-claw services found; running plain docker compose lifecycle")
	}

	if len(drivers) > 0 && !composeUpDetach {
		return fmt.Errorf("claw-managed services require detached mode for fail-closed post-apply verification; rerun with 'claw up -d %s'", podFile)
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

func resolveCllama(imageLevel, podLevel []string) []string {
	if len(podLevel) > 0 {
		return podLevel
	}
	return imageLevel
}

func detectCllama(claws map[string]*driver.ResolvedClaw) (bool, []string) {
	agents := make([]string, 0)
	for name, rc := range claws {
		if len(rc.Cllama) > 0 {
			agents = append(agents, name)
		}
	}
	sort.Strings(agents)
	return len(agents) > 0, agents
}

func collectProxyTypes(claws map[string]*driver.ResolvedClaw) []string {
	seen := make(map[string]struct{})
	for _, rc := range claws {
		for _, proxyType := range rc.Cllama {
			if strings.TrimSpace(proxyType) == "" {
				continue
			}
			seen[proxyType] = struct{}{}
		}
	}
	types := make([]string, 0, len(seen))
	for proxyType := range seen {
		types = append(types, proxyType)
	}
	sort.Strings(types)
	return types
}

func sortedResolvedClawNames(claws map[string]*driver.ResolvedClaw) []string {
	names := make([]string, 0, len(claws))
	for name := range claws {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func isProviderKey(key string) bool {
	switch key {
	case "OPENAI_API_KEY", "ANTHROPIC_API_KEY", "OPENROUTER_API_KEY":
		return true
	}
	return strings.HasPrefix(key, "PROVIDER_API_KEY")
}

func stripLLMKeys(env map[string]string) {
	for key := range env {
		if isProviderKey(key) {
			delete(env, key)
		}
	}
}

func inspectImageEnv(imageRef string) (map[string]string, error) {
	cmd := exec.Command("docker", "image", "inspect", "--format", "{{json .Config.Env}}", imageRef)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("docker image inspect %q: %w", imageRef, err)
	}

	raw := strings.TrimSpace(string(out))
	if raw == "" || raw == "null" {
		return map[string]string{}, nil
	}

	var envList []string
	if err := json.Unmarshal(out, &envList); err != nil {
		return nil, fmt.Errorf("decode image env for %q: %w", imageRef, err)
	}

	env := make(map[string]string, len(envList))
	for _, item := range envList {
		if key, value, ok := strings.Cut(item, "="); ok {
			env[key] = value
			continue
		}
		env[item] = ""
	}
	return env, nil
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

// resolveChannelGeneratedSkills generates surface-<platform>.md skill files for
// each channel surface and returns them as ResolvedSkills.
func resolveChannelGeneratedSkills(runtimeDir string, surfaces []driver.ResolvedSurface) ([]driver.ResolvedSkill, error) {
	surfaceSkillsDir := filepath.Join(runtimeDir, "skills")
	generated := make([]driver.ResolvedSkill, 0)
	seen := make(map[string]struct{})

	for _, surface := range surfaces {
		if surface.Scheme != "channel" {
			continue
		}
		name := fmt.Sprintf("surface-%s.md", strings.TrimSpace(surface.Target))
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}

		skillPath := filepath.Join(surfaceSkillsDir, name)
		if err := os.MkdirAll(filepath.Dir(skillPath), 0700); err != nil {
			return nil, fmt.Errorf("create channel skill dir: %w", err)
		}
		content := shared.GenerateChannelSkill(surface)
		if err := writeRuntimeFile(skillPath, []byte(content), 0644); err != nil {
			return nil, fmt.Errorf("write channel skill %q: %w", name, err)
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
		content := shared.GenerateHandleSkill(platform, info)
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

func envOrDefault(key, fallback string) string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	return v
}

func firstIf(ok bool, value string) string {
	if ok {
		return value
	}
	return ""
}

type invokeTargetResolution struct {
	To      string
	Warning string
}

type invokeChannelMatch struct {
	Platform string
	Name     string
	ID       string
}

// resolveInvocationTarget resolves x-claw.invoke[].to in a platform-aware way.
//
// Supported forms:
//   - "target"           (infer across all platforms)
//   - "platform:target"  (explicit platform)
//
// Resolution rules:
//   - If target matches a known channel ID, keep that ID.
//   - If target matches a unique channel name, rewrite to that channel ID.
//   - If target is unknown, preserve it verbatim (safe raw-ID/name fallback).
//   - If name lookup is ambiguous, preserve the raw target and emit a warning.
func resolveInvocationTarget(handles map[string]*driver.HandleInfo, raw string) invokeTargetResolution {
	target := strings.TrimSpace(raw)
	if target == "" {
		return invokeTargetResolution{}
	}

	platform, scopedTarget, explicitPlatform := splitInvocationTarget(target)
	if explicitPlatform {
		return resolveInvocationTargetForPlatform(handles, platform, scopedTarget)
	}

	if idMatches := findInvokeChannelMatches(handles, "", target, true); len(idMatches) > 0 {
		return invokeTargetResolution{To: idMatches[0].ID}
	}

	nameMatches := findInvokeChannelMatches(handles, "", target, false)
	return finalizeInvocationNameResolution(target, nameMatches, false)
}

func resolveInvocationTargetForPlatform(handles map[string]*driver.HandleInfo, platform, target string) invokeTargetResolution {
	if idMatches := findInvokeChannelMatches(handles, platform, target, true); len(idMatches) > 0 {
		return invokeTargetResolution{To: idMatches[0].ID}
	}

	nameMatches := findInvokeChannelMatches(handles, platform, target, false)
	return finalizeInvocationNameResolution(target, nameMatches, true)
}

func finalizeInvocationNameResolution(rawTarget string, matches []invokeChannelMatch, platformScoped bool) invokeTargetResolution {
	if len(matches) == 0 {
		return invokeTargetResolution{To: rawTarget}
	}

	ids := make(map[string]struct{}, len(matches))
	for _, m := range matches {
		ids[m.ID] = struct{}{}
	}
	if len(ids) == 1 {
		for id := range ids {
			return invokeTargetResolution{To: id}
		}
	}

	hint := "use platform:target to disambiguate"
	if platformScoped {
		hint = "use a channel ID to disambiguate"
	}
	return invokeTargetResolution{
		To:      rawTarget,
		Warning: fmt.Sprintf("invoke target %q is ambiguous (%s); %s", rawTarget, formatInvokeChannelMatches(matches), hint),
	}
}

func splitInvocationTarget(target string) (platform string, scopedTarget string, ok bool) {
	idx := strings.Index(target, ":")
	if idx <= 0 || idx >= len(target)-1 {
		return "", target, false
	}
	platform = strings.ToLower(strings.TrimSpace(target[:idx]))
	scopedTarget = strings.TrimSpace(target[idx+1:])
	if platform == "" || scopedTarget == "" || strings.Contains(platform, " ") {
		return "", target, false
	}
	return platform, scopedTarget, true
}

func findInvokeChannelMatches(handles map[string]*driver.HandleInfo, platform, target string, byID bool) []invokeChannelMatch {
	target = strings.TrimSpace(target)
	if target == "" || len(handles) == 0 {
		return nil
	}

	platforms := make([]string, 0, len(handles))
	if platform != "" {
		p := strings.ToLower(strings.TrimSpace(platform))
		if _, ok := handles[p]; !ok {
			return nil
		}
		platforms = append(platforms, p)
	} else {
		for p := range handles {
			platforms = append(platforms, p)
		}
		sort.Strings(platforms)
	}

	matches := make([]invokeChannelMatch, 0)
	for _, p := range platforms {
		h := handles[p]
		if h == nil {
			continue
		}
		for _, g := range h.Guilds {
			for _, ch := range g.Channels {
				if byID && ch.ID == target {
					matches = append(matches, invokeChannelMatch{Platform: p, Name: ch.Name, ID: ch.ID})
					continue
				}
				if !byID && ch.Name == target {
					matches = append(matches, invokeChannelMatch{Platform: p, Name: ch.Name, ID: ch.ID})
				}
			}
		}
	}
	return matches
}

func formatInvokeChannelMatches(matches []invokeChannelMatch) string {
	formatted := make([]string, 0, len(matches))
	for _, m := range matches {
		label := fmt.Sprintf("%s:%s", m.Platform, m.Name)
		if m.Name == "" {
			label = fmt.Sprintf("%s:<unnamed>", m.Platform)
		}
		formatted = append(formatted, fmt.Sprintf("%s (%s)", label, m.ID))
	}
	sort.Strings(formatted)
	return strings.Join(formatted, ", ")
}

// ensureInfraImages checks that cllama proxy and clawdash images exist locally,
// building them from source when missing.
func ensureInfraImages(cllamaEnabled bool, proxies []pod.CllamaProxyConfig, dash *pod.ClawdashConfig) error {
	if cllamaEnabled {
		for _, proxy := range proxies {
			if err := ensureImage(proxy.Image, "cllama", "cllama/Dockerfile", "cllama"); err != nil {
				return err
			}
		}
	}
	if dash != nil {
		if err := ensureImage(dash.Image, "clawdash", "dockerfiles/clawdash/Dockerfile", "."); err != nil {
			return err
		}
	}
	return nil
}

// ensureImage builds a Docker image if it doesn't exist locally.
// It tries: local build from repo source, then git URL build, then errors.
func ensureImage(imageRef, name, dockerfilePath, contextDir string) error {
	if build.ImageExistsLocally(imageRef) {
		return nil
	}

	fmt.Printf("[claw] building %s image (first time only)\n", name)

	repoRoot, found := findRepoRoot()
	if found {
		df := filepath.Join(repoRoot, dockerfilePath)
		ctx := filepath.Join(repoRoot, contextDir)
		if _, err := os.Stat(df); err == nil {
			cmd := exec.Command("docker", "build", "-t", imageRef, "-f", df, ctx)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			if err := cmd.Run(); err != nil {
				return fmt.Errorf("build %s image from local source: %w", name, err)
			}
			return nil
		}
	}

	// Fallback: build from git URL.
	gitURL := fmt.Sprintf("https://github.com/mostlydev/clawdapus.git#master:%s", contextDir)
	cmd := exec.Command("docker", "build", "-t", imageRef, gitURL)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("could not build %s image; run 'docker build -t %s -f %s %s' from the repo root", name, imageRef, dockerfilePath, contextDir)
	}
	return nil
}

// findRepoRoot walks up from cwd looking for go.mod with the clawdapus module.
func findRepoRoot() (string, bool) {
	dir, err := os.Getwd()
	if err != nil {
		return "", false
	}
	for {
		modPath := filepath.Join(dir, "go.mod")
		if data, err := os.ReadFile(modPath); err == nil {
			if strings.Contains(string(data), "module github.com/mostlydev/clawdapus") {
				return dir, true
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

func init() {
	composeUpCmd.Flags().BoolVarP(&composeUpDetach, "detach", "d", false, "Run in background")
	rootCmd.AddCommand(composeUpCmd)
}
