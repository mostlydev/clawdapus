package main

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mostlydev/clawdapus/internal/build"
	"github.com/mostlydev/clawdapus/internal/cllama"
	"github.com/mostlydev/clawdapus/internal/driver"
	"github.com/mostlydev/clawdapus/internal/driver/shared"
	"github.com/mostlydev/clawdapus/internal/inspect"
	"github.com/mostlydev/clawdapus/internal/persona"
	"github.com/mostlydev/clawdapus/internal/pod"
	"github.com/mostlydev/clawdapus/internal/runtime"
)

var composeUpDetach bool

var envVarPattern = regexp.MustCompile(`\$\{([A-Z_][A-Z0-9_]*)\}`)

var (
	extractServiceSkillFromImage = runtime.ExtractServiceSkill
	writeRuntimeFile             = os.WriteFile
	inspectClawImage             = inspect.Inspect
	imageExistsLocally           = build.ImageExistsLocally
	generateClawDockerfile       = build.Generate
	buildGeneratedImage          = build.BuildFromGenerated
	dockerBuildTaggedImage       = dockerBuildTaggedImageDefault
	findClawdapusRepoRoot        = findRepoRoot
	runInfraDockerCommand        = runInfraDockerCommandDefault
	runComposeDockerCommand      = runComposeDockerCommandDefault
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
	if err := resolveRuntimePlaceholders(podDir, p); err != nil {
		return fmt.Errorf("resolve x-claw runtime placeholders: %w", err)
	}
	runtimeDir := filepath.Join(podDir, ".claw-runtime")
	if err := resetRuntimeDir(runtimeDir); err != nil {
		return fmt.Errorf("reset runtime dir: %w", err)
	}

	results := make(map[string]*driver.MaterializeResult)
	drivers := make(map[string]driver.Driver)
	resolvedClaws := make(map[string]*driver.ResolvedClaw)
	serviceRuntimeDirs := make(map[string]string)
	serviceImageRefs := make(map[string]string)
	serviceInfos := make(map[string]*inspect.ClawInfo)

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

		imageRef, err := resolveManagedServiceImage(podDir, p, name, svc)
		if err != nil {
			return err
		}

		info, err := inspectClawImage(imageRef)
		if err != nil {
			return fmt.Errorf("inspect image %q for service %q: %w", imageRef, name, err)
		}

		if info.ClawType == "" {
			return fmt.Errorf("service %q: image %q has no claw.type label", name, imageRef)
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

		resolvedIncludes, includeSkills, err := materializeContractIncludes(podDir, svcRuntimeDir, agentHostPath, svc.Claw.Include)
		if err != nil {
			return fmt.Errorf("service %q: materialize contract includes: %w", name, err)
		}
		if len(resolvedIncludes) > 0 {
			agentHostPath = filepath.Join(svcRuntimeDir, "AGENTS.generated.md")
		}
		personaRef := firstNonEmpty(svc.Claw.Persona, info.Persona)
		resolvedPersona, err := persona.Materialize(podDir, svcRuntimeDir, personaRef)
		if err != nil {
			return fmt.Errorf("service %q: materialize persona: %w", name, err)
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
		surfaces, generatedSkills, err := resolveServiceSurfaceSkills(podDir, svcRuntimeDir, p, surfaces, serviceImageRefs, serviceInfos)
		if err != nil {
			return fmt.Errorf("service %q: resolve service surface skills: %w", name, err)
		}
		// Add channel surface skills (surface-discord.md etc.)
		channelSkills, err := resolveChannelGeneratedSkills(svcRuntimeDir, surfaces)
		if err != nil {
			return fmt.Errorf("service %q: resolve generated channel skills: %w", name, err)
		}
		if len(channelSkills) > 0 {
			generatedSkills = mergeResolvedSkills(generatedSkills, channelSkills)
		}
		if len(includeSkills) > 0 {
			generatedSkills = mergeResolvedSkills(generatedSkills, includeSkills)
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
		agentHostPath, err = materializeServiceSurfaceGuides(svcRuntimeDir, agentHostPath, surfaces, skills)
		if err != nil {
			return fmt.Errorf("service %q: materialize service surface guides: %w", name, err)
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
			Persona:       personaRef,
			Models:        info.Models,
			Handles:       svc.Claw.Handles,
			PeerHandles:   peerHandles,
			Includes:      resolvedIncludes,
			Configures:    info.Configures,
			Privileges:    info.Privileges,
			Count:         svc.Claw.Count,
			Environment:   svc.Environment,
			Surfaces:      surfaces,
			Skills:        skills,
			Cllama:        resolveCllama(info.Cllama, svc.Claw.Cllama),
		}
		if resolvedPersona != nil {
			rc.PersonaHostPath = resolvedPersona.HostPath
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

	if err := runComposeDockerCommand(composeArgs...); err != nil {
		return fmt.Errorf("docker compose up failed: %w", err)
	}

	runtimeConsumers := runtimeConsumerServices(resolvedClaws, proxies, p.Clawdash)
	if composeUpDetach && len(runtimeConsumers) > 0 {
		recreateArgs := append([]string{"compose", "-f", generatedPath, "up", "-d", "--force-recreate"}, runtimeConsumers...)
		if err := runComposeDockerCommand(recreateArgs...); err != nil {
			return fmt.Errorf("docker compose force-recreate failed: %w", err)
		}
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

func resetRuntimeDir(path string) error {
	if err := os.RemoveAll(path); err != nil {
		return err
	}
	return os.MkdirAll(path, 0o700)
}

func runtimeConsumerServices(resolvedClaws map[string]*driver.ResolvedClaw, proxies []pod.CllamaProxyConfig, dash *pod.ClawdashConfig) []string {
	seen := make(map[string]struct{})
	names := make([]string, 0, len(resolvedClaws)+len(proxies)+1)

	for name, rc := range resolvedClaws {
		count := 1
		if rc != nil && rc.Count > 0 {
			count = rc.Count
		}
		for _, generated := range expandedServiceNames(name, count) {
			if _, ok := seen[generated]; ok {
				continue
			}
			seen[generated] = struct{}{}
			names = append(names, generated)
		}
	}

	for _, proxy := range proxies {
		serviceName := cllama.ProxyServiceName(proxy.ProxyType)
		if _, ok := seen[serviceName]; ok {
			continue
		}
		seen[serviceName] = struct{}{}
		names = append(names, serviceName)
	}

	if dash != nil {
		if _, ok := seen["clawdash"]; !ok {
			names = append(names, "clawdash")
		}
	}

	sort.Strings(names)
	return names
}

func resolveRuntimePlaceholders(podDir string, p *pod.Pod) error {
	env, err := loadRuntimeEnv(podDir)
	if err != nil {
		return err
	}
	expand := func(value string) string {
		return envVarPattern.ReplaceAllStringFunc(value, func(match string) string {
			key := match[2 : len(match)-1]
			if v, ok := env[key]; ok {
				return v
			}
			return match
		})
	}

	for _, svc := range p.Services {
		if svc == nil || svc.Claw == nil {
			continue
		}
		svc.Claw.Agent = expand(svc.Claw.Agent)
		svc.Claw.Persona = expand(svc.Claw.Persona)
		for i, value := range svc.Claw.Cllama {
			svc.Claw.Cllama[i] = expand(value)
		}
		for key, value := range svc.Claw.CllamaEnv {
			svc.Claw.CllamaEnv[key] = expand(value)
		}
		for i, value := range svc.Claw.Skills {
			svc.Claw.Skills[i] = expand(value)
		}
		for i := range svc.Claw.Include {
			svc.Claw.Include[i].ID = expand(svc.Claw.Include[i].ID)
			svc.Claw.Include[i].File = expand(svc.Claw.Include[i].File)
			svc.Claw.Include[i].Mode = expand(svc.Claw.Include[i].Mode)
			svc.Claw.Include[i].Description = expand(svc.Claw.Include[i].Description)
		}
		for i := range svc.Claw.Invoke {
			svc.Claw.Invoke[i].Schedule = expand(svc.Claw.Invoke[i].Schedule)
			svc.Claw.Invoke[i].Message = expand(svc.Claw.Invoke[i].Message)
			svc.Claw.Invoke[i].Name = expand(svc.Claw.Invoke[i].Name)
			svc.Claw.Invoke[i].To = expand(svc.Claw.Invoke[i].To)
		}
		for _, handle := range svc.Claw.Handles {
			if handle == nil {
				continue
			}
			handle.ID = expand(handle.ID)
			handle.Username = expand(handle.Username)
			for gi := range handle.Guilds {
				handle.Guilds[gi].ID = expand(handle.Guilds[gi].ID)
				handle.Guilds[gi].Name = expand(handle.Guilds[gi].Name)
				for ci := range handle.Guilds[gi].Channels {
					handle.Guilds[gi].Channels[ci].ID = expand(handle.Guilds[gi].Channels[ci].ID)
					handle.Guilds[gi].Channels[ci].Name = expand(handle.Guilds[gi].Channels[ci].Name)
				}
			}
		}
		for i := range svc.Claw.Surfaces {
			svc.Claw.Surfaces[i].Target = expand(svc.Claw.Surfaces[i].Target)
			svc.Claw.Surfaces[i].AccessMode = expand(svc.Claw.Surfaces[i].AccessMode)
			if cc := svc.Claw.Surfaces[i].ChannelConfig; cc != nil {
				services := make([]string, len(cc.AllowFromServices))
				for j, value := range cc.AllowFromServices {
					services[j] = expand(value)
				}
				cc.AllowFromServices = services
				cc.DM.Policy = expand(cc.DM.Policy)
				for j, value := range cc.DM.AllowFrom {
					cc.DM.AllowFrom[j] = expand(value)
				}
				expandedGuilds := make(map[string]driver.ChannelGuildConfig, len(cc.Guilds))
				for guildID, guildCfg := range cc.Guilds {
					guildCfg.Policy = expand(guildCfg.Policy)
					users := make([]string, len(guildCfg.Users))
					for j, value := range guildCfg.Users {
						users[j] = expand(value)
					}
					guildCfg.Users = users
					expandedGuilds[expand(guildID)] = guildCfg
				}
				cc.Guilds = expandedGuilds
				if svc.Claw.Surfaces[i].Target == "discord" {
					if err := expandDiscordChannelAdmission(p, cc); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

func expandDiscordChannelAdmission(p *pod.Pod, cc *driver.ChannelConfig) error {
	if cc == nil || (!cc.AllowFromHandles && len(cc.AllowFromServices) == 0) {
		return nil
	}

	derived := make([]string, 0)
	if cc.AllowFromHandles {
		derived = append(derived, discordHandleIDsFromPod(p)...)
	}

	serviceIDs, err := discordServiceUserIDs(p, cc.AllowFromServices)
	if err != nil {
		return err
	}
	derived = append(derived, serviceIDs...)
	derived = uniqueSortedStrings(derived)
	if len(derived) == 0 {
		return nil
	}

	for guildID, guildCfg := range cc.Guilds {
		guildCfg.Users = mergeUniqueStrings(guildCfg.Users, derived)
		cc.Guilds[guildID] = guildCfg
	}
	return nil
}

func discordHandleIDsFromPod(p *pod.Pod) []string {
	ids := make([]string, 0)
	for _, svc := range p.Services {
		if svc.Claw == nil {
			continue
		}
		handle := svc.Claw.Handles["discord"]
		if handle == nil || handle.ID == "" {
			continue
		}
		ids = append(ids, handle.ID)
	}
	return uniqueSortedStrings(ids)
}

func discordServiceUserIDs(p *pod.Pod, serviceNames []string) ([]string, error) {
	ids := make([]string, 0, len(serviceNames))
	for _, name := range serviceNames {
		svc, ok := p.Services[name]
		if !ok {
			return nil, fmt.Errorf("channel://discord allow_from_services references unknown service %q", name)
		}
		id := discordUserIDFromService(svc)
		if id == "" {
			return nil, fmt.Errorf("channel://discord allow_from_services service %q has no Discord bot identity; expected DISCORD_BOT_TOKEN or DISCORD_TRADING_API_BOT_TOKEN", name)
		}
		ids = append(ids, id)
	}
	return uniqueSortedStrings(ids), nil
}

func discordUserIDFromService(svc *pod.Service) string {
	if svc == nil {
		return ""
	}
	for _, key := range []string{"DISCORD_BOT_TOKEN", "DISCORD_TRADING_API_BOT_TOKEN"} {
		if token := strings.TrimSpace(svc.Environment[key]); token != "" {
			if id := discordIDFromToken(token); id != "" {
				return id
			}
		}
	}
	return ""
}

func discordIDFromToken(token string) string {
	parts := strings.SplitN(strings.TrimSpace(token), ".", 2)
	if len(parts) == 0 || parts[0] == "" {
		return ""
	}
	segment := parts[0]
	data, err := base64.RawURLEncoding.DecodeString(segment)
	if err != nil {
		return ""
	}
	id := strings.TrimSpace(string(data))
	if id == "" {
		return ""
	}
	for _, r := range id {
		if r < '0' || r > '9' {
			return ""
		}
	}
	return id
}

func uniqueSortedStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func mergeUniqueStrings(base []string, extra []string) []string {
	if len(extra) == 0 {
		return base
	}
	seen := make(map[string]struct{}, len(base)+len(extra))
	out := make([]string, 0, len(base)+len(extra))
	for _, value := range base {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	for _, value := range extra {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func loadRuntimeEnv(podDir string) (map[string]string, error) {
	env := make(map[string]string)
	dotEnvPath := filepath.Join(podDir, ".env")
	if fileEnv, err := readDotEnvFile(dotEnvPath); err == nil {
		for key, value := range fileEnv {
			env[key] = value
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	for _, entry := range os.Environ() {
		eq := strings.IndexByte(entry, '=')
		if eq < 0 {
			continue
		}
		env[entry[:eq]] = entry[eq+1:]
	}
	return env, nil
}

func readDotEnvFile(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	out := make(map[string]string)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		eq := strings.IndexByte(line, '=')
		if eq < 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		value := strings.TrimSpace(line[eq+1:])
		value = strings.Trim(value, `"'`)
		out[key] = value
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return out, nil
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

func materializeContractIncludes(baseDir, runtimeDir, agentHostPath string, includes []pod.IncludeEntry) ([]driver.ResolvedInclude, []driver.ResolvedSkill, error) {
	if len(includes) == 0 {
		return nil, nil, nil
	}
	if agentHostPath == "" {
		return nil, nil, fmt.Errorf("x-claw.include requires a base agent contract")
	}

	baseContract, err := os.ReadFile(agentHostPath)
	if err != nil {
		return nil, nil, fmt.Errorf("read base agent contract: %w", err)
	}

	resolved := make([]driver.ResolvedInclude, 0, len(includes))
	skills := make([]driver.ResolvedSkill, 0)

	var compiled strings.Builder
	compiled.WriteString(strings.TrimRight(string(baseContract), "\n"))

	for _, include := range includes {
		hostPath, err := resolveRuntimeScopedFile(baseDir, include.File)
		if err != nil {
			return nil, nil, fmt.Errorf("include %q: %w", include.ID, err)
		}
		content, err := os.ReadFile(hostPath)
		if err != nil {
			return nil, nil, fmt.Errorf("include %q: read file: %w", include.ID, err)
		}

		ri := driver.ResolvedInclude{
			ID:          include.ID,
			Mode:        include.Mode,
			Description: include.Description,
			HostPath:    hostPath,
		}

		switch include.Mode {
		case "enforce", "guide":
			compiled.WriteString("\n\n")
			compiled.WriteString(fmt.Sprintf("--- BEGIN: %s (%s) ---\n\n", include.ID, include.Mode))
			compiled.WriteString(strings.TrimRight(string(content), "\n"))
			compiled.WriteString("\n\n")
			compiled.WriteString(fmt.Sprintf("--- END: %s (%s) ---", include.ID, include.Mode))
		case "reference":
			skillName := includeSkillName(include.ID, hostPath)
			skillPath := filepath.Join(runtimeDir, "skills", skillName)
			if err := os.MkdirAll(filepath.Dir(skillPath), 0700); err != nil {
				return nil, nil, fmt.Errorf("include %q: create skill dir: %w", include.ID, err)
			}
			if err := writeRuntimeFile(skillPath, content, 0644); err != nil {
				return nil, nil, fmt.Errorf("include %q: write reference skill: %w", include.ID, err)
			}
			ri.SkillName = skillName
			skills = append(skills, driver.ResolvedSkill{Name: skillName, HostPath: skillPath})
		default:
			return nil, nil, fmt.Errorf("include %q: unsupported mode %q", include.ID, include.Mode)
		}

		resolved = append(resolved, ri)
	}

	generatedPath := filepath.Join(runtimeDir, "AGENTS.generated.md")
	if err := writeRuntimeFile(generatedPath, []byte(compiled.String()+"\n"), 0644); err != nil {
		return nil, nil, fmt.Errorf("write generated AGENTS.md: %w", err)
	}

	return resolved, skills, nil
}

func materializeServiceSurfaceGuides(runtimeDir, agentHostPath string, surfaces []driver.ResolvedSurface, skills []driver.ResolvedSkill) (string, error) {
	if agentHostPath == "" || len(surfaces) == 0 || len(skills) == 0 {
		return agentHostPath, nil
	}

	skillPaths := make(map[string]string, len(skills))
	for _, skill := range skills {
		if strings.TrimSpace(skill.Name) == "" || strings.TrimSpace(skill.HostPath) == "" {
			continue
		}
		skillPaths[skill.Name] = skill.HostPath
	}

	type serviceGuide struct {
		target    string
		skillName string
		hostPath  string
	}

	guides := make([]serviceGuide, 0)
	seen := make(map[string]struct{})
	for _, surface := range surfaces {
		if surface.Scheme != "service" {
			continue
		}

		skillName := strings.TrimSpace(surface.SkillName)
		if skillName == "" || skillName == surfaceFallbackSkillName(surface.Target) {
			continue
		}
		if _, exists := seen[skillName]; exists {
			continue
		}

		hostPath, ok := skillPaths[skillName]
		if !ok {
			return "", fmt.Errorf("service surface %q references skill %q but no resolved skill was found", surface.Target, skillName)
		}

		guides = append(guides, serviceGuide{
			target:    surface.Target,
			skillName: skillName,
			hostPath:  hostPath,
		})
		seen[skillName] = struct{}{}
	}

	if len(guides) == 0 {
		return agentHostPath, nil
	}

	baseContract, err := os.ReadFile(agentHostPath)
	if err != nil {
		return "", fmt.Errorf("read agent contract: %w", err)
	}

	var compiled strings.Builder
	compiled.WriteString(strings.TrimRight(string(baseContract), "\n"))

	for _, guide := range guides {
		content, err := os.ReadFile(guide.hostPath)
		if err != nil {
			return "", fmt.Errorf("read service guide %q: %w", guide.skillName, err)
		}

		compiled.WriteString("\n\n")
		compiled.WriteString(fmt.Sprintf("--- BEGIN: service_manual %s (guide) ---\n\n", guide.target))
		compiled.WriteString(fmt.Sprintf("This service manual was injected automatically because you declared `service://%s`.\n", guide.target))
		compiled.WriteString("Treat it as the authoritative workflow for acting through that service.\n\n")
		compiled.WriteString(strings.TrimRight(stripSkillFrontmatter(string(content)), "\n"))
		compiled.WriteString("\n\n")
		compiled.WriteString(fmt.Sprintf("--- END: service_manual %s (guide) ---", guide.target))
	}

	generatedPath := filepath.Join(runtimeDir, "AGENTS.generated.md")
	if err := writeRuntimeFile(generatedPath, []byte(compiled.String()+"\n"), 0644); err != nil {
		return "", fmt.Errorf("write generated AGENTS.md: %w", err)
	}

	return generatedPath, nil
}

func stripSkillFrontmatter(content string) string {
	if !strings.HasPrefix(content, "---\n") {
		return content
	}

	rest := strings.TrimPrefix(content, "---\n")
	end := strings.Index(rest, "\n---\n")
	if end < 0 {
		return content
	}

	return strings.TrimLeft(rest[end+5:], "\n")
}

func resolveRuntimeScopedFile(baseDir, relPath string) (string, error) {
	absBase, err := filepath.Abs(baseDir)
	if err != nil {
		return "", fmt.Errorf("resolve base dir %q: %w", baseDir, err)
	}
	realBase, err := filepath.EvalSymlinks(absBase)
	if err != nil {
		return "", fmt.Errorf("resolve real base dir %q: %w", baseDir, err)
	}

	hostPath, err := filepath.Abs(filepath.Join(baseDir, relPath))
	if err != nil {
		return "", fmt.Errorf("resolve path %q: %w", relPath, err)
	}
	if !strings.HasPrefix(hostPath, absBase+string(filepath.Separator)) && hostPath != absBase {
		return "", fmt.Errorf("path %q escapes base directory %q", relPath, baseDir)
	}

	info, err := os.Stat(hostPath)
	if err != nil {
		return "", fmt.Errorf("file %q not found: %w", hostPath, err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("%q is not a regular file", relPath)
	}

	realHostPath, err := filepath.EvalSymlinks(hostPath)
	if err != nil {
		return "", fmt.Errorf("resolve real path for %q: %w", relPath, err)
	}
	if !strings.HasPrefix(realHostPath, realBase+string(filepath.Separator)) && realHostPath != realBase {
		return "", fmt.Errorf("path %q escapes base directory %q", relPath, baseDir)
	}

	return realHostPath, nil
}

func includeSkillName(id, hostPath string) string {
	safeID := make([]rune, 0, len(id))
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			safeID = append(safeID, r)
			continue
		}
		safeID = append(safeID, '_')
	}
	if len(safeID) == 0 {
		safeID = []rune("include")
	}

	ext := filepath.Ext(hostPath)
	if ext == "" {
		ext = ".md"
	}
	return "include-" + string(safeID) + ext
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
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

func resolveServiceSurfaceSkills(podDir, runtimeDir string, p *pod.Pod, surfaces []driver.ResolvedSurface, imageRefs map[string]string, infos map[string]*inspect.ClawInfo) ([]driver.ResolvedSurface, []driver.ResolvedSkill, error) {
	surfaceSkillsDir := filepath.Join(runtimeDir, "skills")
	resolvedSurfaces := append([]driver.ResolvedSurface(nil), surfaces...)
	generated := make([]driver.ResolvedSkill, 0)
	seen := make(map[string]struct{}, len(surfaces))

	for i, surface := range resolvedSurfaces {
		if surface.Scheme != "service" {
			continue
		}

		name := surfaceFallbackSkillName(surface.Target)
		if name == "surface-.md" {
			return nil, nil, fmt.Errorf("invalid service target for generated skill: %q", surface.Target)
		}

		if targetSvc, ok := p.Services[surface.Target]; ok {
			imageRef, info, err := resolveServiceInspection(podDir, p, surface.Target, targetSvc, imageRefs, infos)
			if err != nil {
				return nil, nil, fmt.Errorf("inspect target service %q: %w", surface.Target, err)
			}
			if info != nil && strings.TrimSpace(info.SkillEmit) != "" {
				emitSkill, err := resolveSkillEmit(surface.Target, runtimeDir, imageRef, info.SkillEmit)
				if err != nil {
					return nil, nil, fmt.Errorf("extract emitted skill for target service %q: %w", surface.Target, err)
				}
				if emitSkill != nil {
					resolvedSurfaces[i].SkillName = emitSkill.Name
					if _, exists := seen[emitSkill.Name]; !exists {
						seen[emitSkill.Name] = struct{}{}
						generated = append(generated, *emitSkill)
					}
					continue
				}
			}
		}

		resolvedSurfaces[i].SkillName = name
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}

		skillPath := filepath.Join(surfaceSkillsDir, name)
		if err := os.MkdirAll(filepath.Dir(skillPath), 0700); err != nil {
			return nil, nil, fmt.Errorf("create generated skill dir: %w", err)
		}
		content := runtime.GenerateServiceSkillFallback(surface.Target, surface.Ports)
		if err := writeRuntimeFile(skillPath, []byte(content), 0644); err != nil {
			return nil, nil, fmt.Errorf("write generated service skill %q: %w", name, err)
		}
		generated = append(generated, driver.ResolvedSkill{
			Name:     name,
			HostPath: skillPath,
		})
	}

	return resolvedSurfaces, generated, nil
}

func surfaceFallbackSkillName(target string) string {
	return fmt.Sprintf("surface-%s.md", strings.TrimSpace(strings.ReplaceAll(target, "/", "-")))
}

func resolveServiceInspection(podDir string, p *pod.Pod, serviceName string, svc *pod.Service, imageRefs map[string]string, infos map[string]*inspect.ClawInfo) (string, *inspect.ClawInfo, error) {
	if imageRef, ok := imageRefs[serviceName]; ok {
		return imageRef, infos[serviceName], nil
	}

	imageRef, err := resolveManagedServiceImage(podDir, p, serviceName, svc)
	if err != nil {
		return "", nil, err
	}
	info, err := inspectClawImage(imageRef)
	if err != nil {
		return "", nil, err
	}
	imageRefs[serviceName] = imageRef
	infos[serviceName] = info
	return imageRef, info, nil
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

type serviceBuildConfig struct {
	Context    string
	Dockerfile string
	Args       map[string]string
	Target     string
}

func resolveManagedServiceImage(podDir string, p *pod.Pod, serviceName string, svc *pod.Service) (string, error) {
	imageRef := strings.TrimSpace(svc.Image)
	if imageRef != "" && imageExistsLocally(imageRef) {
		return imageRef, nil
	}

	cfg, err := parseServiceBuildConfig(svc.Compose["build"])
	if err != nil {
		return "", fmt.Errorf("service %q: parse build: %w", serviceName, err)
	}

	if cfg == nil {
		if imageRef == "" {
			return "", fmt.Errorf("service %q: claw-managed services require image: or build:", serviceName)
		}
		return "", fmt.Errorf("service %q: image %q not found locally and no build config declared", serviceName, imageRef)
	}

	if imageRef == "" {
		imageRef = managedServiceImageRef(p.Name, serviceName)
		svc.Image = imageRef
		if svc.Compose == nil {
			svc.Compose = make(map[string]interface{})
		}
		svc.Compose["image"] = imageRef
	}

	if imageExistsLocally(imageRef) {
		return imageRef, nil
	}

	fmt.Printf("[claw] %s: building image %s for inspection\n", serviceName, imageRef)
	if err := buildManagedServiceImage(podDir, imageRef, cfg); err != nil {
		return "", fmt.Errorf("service %q: %w", serviceName, err)
	}

	svc.Image = imageRef
	if svc.Compose == nil {
		svc.Compose = make(map[string]interface{})
	}
	svc.Compose["image"] = imageRef
	return imageRef, nil
}

func parseServiceBuildConfig(raw interface{}) (*serviceBuildConfig, error) {
	if raw == nil {
		return nil, nil
	}

	cfg := &serviceBuildConfig{}
	switch v := raw.(type) {
	case string:
		cfg.Context = strings.TrimSpace(v)
	case map[string]interface{}:
		for key, value := range v {
			switch key {
			case "context":
				s, err := buildScalarString(value)
				if err != nil {
					return nil, fmt.Errorf("context: %w", err)
				}
				cfg.Context = strings.TrimSpace(s)
			case "dockerfile":
				s, err := buildScalarString(value)
				if err != nil {
					return nil, fmt.Errorf("dockerfile: %w", err)
				}
				cfg.Dockerfile = strings.TrimSpace(s)
			case "target":
				s, err := buildScalarString(value)
				if err != nil {
					return nil, fmt.Errorf("target: %w", err)
				}
				cfg.Target = strings.TrimSpace(s)
			case "args":
				args, err := parseBuildArgs(value)
				if err != nil {
					return nil, fmt.Errorf("args: %w", err)
				}
				cfg.Args = args
			}
		}
	default:
		return nil, fmt.Errorf("unsupported build value type %T", raw)
	}

	if cfg.Context == "" {
		cfg.Context = "."
	}
	return cfg, nil
}

func parseBuildArgs(raw interface{}) (map[string]string, error) {
	if raw == nil {
		return nil, nil
	}

	switch v := raw.(type) {
	case map[string]string:
		out := make(map[string]string, len(v))
		for k, value := range v {
			out[k] = value
		}
		return out, nil
	case map[string]interface{}:
		out := make(map[string]string, len(v))
		for k, value := range v {
			s, err := buildScalarString(value)
			if err != nil {
				return nil, fmt.Errorf("key %q: %w", k, err)
			}
			out[k] = s
		}
		return out, nil
	case []string:
		return parseBuildArgList(v)
	case []interface{}:
		items := make([]string, 0, len(v))
		for i, item := range v {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("entry %d: expected string, got %T", i, item)
			}
			items = append(items, s)
		}
		return parseBuildArgList(items)
	default:
		return nil, fmt.Errorf("unsupported build args type %T", raw)
	}
}

func parseBuildArgList(items []string) (map[string]string, error) {
	out := make(map[string]string, len(items))
	for i, item := range items {
		key := item
		value := ""
		if idx := strings.Index(item, "="); idx >= 0 {
			key = item[:idx]
			value = item[idx+1:]
		}
		key = strings.TrimSpace(key)
		if key == "" {
			return nil, fmt.Errorf("entry %d: build arg key must not be empty", i)
		}
		out[key] = value
	}
	return out, nil
}

func buildManagedServiceImage(podDir, imageRef string, cfg *serviceBuildConfig) error {
	contextDir := cfg.Context
	if !filepath.IsAbs(contextDir) {
		contextDir = filepath.Join(podDir, contextDir)
	}
	contextDir, err := filepath.Abs(contextDir)
	if err != nil {
		return fmt.Errorf("resolve build context %q: %w", cfg.Context, err)
	}

	dockerfilePath, err := resolveBuildDockerfilePath(contextDir, cfg.Dockerfile)
	if err != nil {
		return err
	}

	if isClawBuildFile(dockerfilePath) {
		generatedPath, err := generateClawDockerfile(dockerfilePath)
		if err != nil {
			return fmt.Errorf("generate Dockerfile from %q: %w", dockerfilePath, err)
		}
		if err := buildGeneratedImage(generatedPath, imageRef); err != nil {
			return fmt.Errorf("build image %q from %q: %w", imageRef, generatedPath, err)
		}
		return nil
	}

	if err := dockerBuildTaggedImage(imageRef, dockerfilePath, contextDir, cfg.Args, cfg.Target); err != nil {
		return fmt.Errorf("docker build %q: %w", imageRef, err)
	}
	return nil
}

func resolveBuildDockerfilePath(contextDir, dockerfile string) (string, error) {
	if strings.TrimSpace(dockerfile) != "" {
		path := strings.TrimSpace(dockerfile)
		if !filepath.IsAbs(path) {
			path = filepath.Join(contextDir, path)
		}
		if _, err := os.Stat(path); err != nil {
			return "", fmt.Errorf("build dockerfile %q: %w", path, err)
		}
		return path, nil
	}

	clawfile := filepath.Join(contextDir, "Clawfile")
	if _, err := os.Stat(clawfile); err == nil {
		return clawfile, nil
	}

	dockerfilePath := filepath.Join(contextDir, "Dockerfile")
	if _, err := os.Stat(dockerfilePath); err != nil {
		return "", fmt.Errorf("build dockerfile %q: %w", dockerfilePath, err)
	}
	return dockerfilePath, nil
}

func isClawBuildFile(path string) bool {
	base := filepath.Base(path)
	if strings.HasPrefix(base, "Clawfile") {
		return true
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	src := string(data)
	return strings.Contains(src, "CLAW_TYPE ") || strings.Contains(src, "\nCLAW_TYPE ") || strings.Contains(src, "\nAGENT ")
}

func managedServiceImageRef(podName, serviceName string) string {
	podPart := sanitizeImageComponent(podName)
	if podPart == "" {
		podPart = "pod"
	}
	servicePart := sanitizeImageComponent(serviceName)
	if servicePart == "" {
		servicePart = "service"
	}
	return fmt.Sprintf("claw-local/%s-%s:latest", podPart, servicePart)
}

func sanitizeImageComponent(in string) string {
	in = strings.ToLower(strings.TrimSpace(in))
	if in == "" {
		return ""
	}
	var b strings.Builder
	lastDash := false
	for _, r := range in {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		case r == '.', r == '_', r == '-':
			b.WriteRune(r)
			lastDash = r == '-'
		default:
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-._")
}

func buildScalarString(v interface{}) (string, error) {
	switch tv := v.(type) {
	case nil:
		return "", nil
	case string:
		return tv, nil
	case int:
		return strconv.Itoa(tv), nil
	case int64:
		return strconv.FormatInt(tv, 10), nil
	case uint64:
		return strconv.FormatUint(tv, 10), nil
	case float64:
		return strconv.FormatFloat(tv, 'f', -1, 64), nil
	case bool:
		if tv {
			return "true", nil
		}
		return "false", nil
	default:
		return "", fmt.Errorf("unsupported scalar type %T", v)
	}
}

func dockerBuildTaggedImageDefault(imageRef, dockerfilePath, contextDir string, args map[string]string, target string) error {
	cmdArgs := []string{"build", "-t", imageRef, "-f", dockerfilePath}
	if strings.TrimSpace(target) != "" {
		cmdArgs = append(cmdArgs, "--target", strings.TrimSpace(target))
	}
	keys := make([]string, 0, len(args))
	for k := range args {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		cmdArgs = append(cmdArgs, "--build-arg", key+"="+args[key])
	}
	cmdArgs = append(cmdArgs, contextDir)

	cmd := exec.Command("docker", cmdArgs...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return err
	}
	return nil
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
// It tries: local image, docker pull, local build from repo source, git URL build,
// then errors with explicit manual-build guidance.
func ensureImage(imageRef, name, dockerfilePath, contextDir string) error {
	if imageExistsLocally(imageRef) {
		return nil
	}

	fmt.Printf("[claw] building %s image (first time only)\n", name)

	if err := runInfraDockerCommand("pull", imageRef); err == nil {
		return nil
	}

	repoRoot, found := findClawdapusRepoRoot()
	if found {
		df := filepath.Join(repoRoot, dockerfilePath)
		ctx := filepath.Join(repoRoot, contextDir)
		if _, err := os.Stat(df); err == nil {
			if err := runInfraDockerCommand("build", "-t", imageRef, "-f", df, ctx); err != nil {
				return fmt.Errorf("build %s image from local source: %w", name, err)
			}
			return nil
		}
	}

	// Fallback: build from git URL.
	gitURL := fmt.Sprintf("https://github.com/mostlydev/clawdapus.git#master:%s", contextDir)
	if err := runInfraDockerCommand("build", "-t", imageRef, gitURL); err != nil {
		return fmt.Errorf("could not build %s image; run 'docker build -t %s -f %s %s' from the repo root", name, imageRef, dockerfilePath, contextDir)
	}
	return nil
}

func runInfraDockerCommandDefault(args ...string) error {
	cmd := exec.Command("docker", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func runComposeDockerCommandDefault(args ...string) error {
	cmd := exec.Command("docker", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
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
