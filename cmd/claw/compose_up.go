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
	"github.com/mostlydev/clawdapus/internal/clawapi"
	"github.com/mostlydev/clawdapus/internal/cllama"
	"github.com/mostlydev/clawdapus/internal/describe"
	"github.com/mostlydev/clawdapus/internal/driver"
	"github.com/mostlydev/clawdapus/internal/driver/shared"
	"github.com/mostlydev/clawdapus/internal/inspect"
	"github.com/mostlydev/clawdapus/internal/persona"
	"github.com/mostlydev/clawdapus/internal/pod"
	"github.com/mostlydev/clawdapus/internal/runtime"
)

var composeUpDetach bool

var runtimePlaceholderPattern = regexp.MustCompile(`\$\{([^}]+)\}`)

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
	loadDescriptorFromImage      = describe.LoadFromImage
	loadDescriptorFromBuildCtx   = describe.LoadFromBuildContext
	resolveBuildContextFile      = describe.ResolveBuildContextFile
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
	// Governance dir is separate from runtimeDir so it survives claw up resets.
	governanceDir := filepath.Join(podDir, ".claw-governance")
	if err := os.MkdirAll(governanceDir, 0o777); err != nil {
		return fmt.Errorf("create governance dir: %w", err)
	}
	if p.Master != "" {
		if _, exists := p.Services["claw-api"]; exists {
			return fmt.Errorf("service name %q is reserved when x-claw.master is set", "claw-api")
		}
		p.ClawAPI = &pod.ClawAPIConfig{
			Image:              "ghcr.io/mostlydev/claw-api:latest",
			Addr:               envOrDefault("CLAW_API_ADDR", ":8080"),
			ManifestHostPath:   filepath.Join(runtimeDir, "pod-manifest.json"),
			PrincipalsHostPath: filepath.Join(runtimeDir, "claw-api", "principals.json"),
			DockerSockHostPath: "/var/run/docker.sock",
			GovernanceHostPath: governanceDir,
			PodName:            p.Name,
			Environment:        collectClawAlertEnv(),
		}
	}

	results := make(map[string]*driver.MaterializeResult)
	drivers := make(map[string]driver.Driver)
	resolvedClaws := make(map[string]*driver.ResolvedClaw)
	serviceRuntimeDirs := make(map[string]string)
	serviceImageRefs := make(map[string]string)
	serviceInfos := make(map[string]*inspect.ClawInfo)
	serviceDescriptors := make(map[string]*describe.ServiceDescriptor)

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
		serviceImageRefs[name] = imageRef
		serviceInfos[name] = info

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
				} else if p.ClawAPI != nil && surfaces[i].Target == "claw-api" {
					surfaces[i].Ports = []string{clawAPIInternalPort(p.ClawAPI.Addr)}
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
		surfaces, generatedSkills, err := resolveServiceSurfaceSkills(podDir, svcRuntimeDir, p, surfaces, serviceImageRefs, serviceInfos, serviceDescriptors)
		if err != nil {
			return fmt.Errorf("service %q: resolve service surface skills: %w", name, err)
		}
		if len(includeSkills) > 0 {
			generatedSkills = mergeResolvedSkills(generatedSkills, includeSkills)
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
			Persona:       personaRef,
			Models:        info.Models,
			Handles:       svc.Claw.Handles,
			PeerHandles:   peerHandles,
			Includes:      resolvedIncludes,
			Feeds:         cloneResolvedFeeds(svc.Claw.Feeds),
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

	if err := collectServiceDescriptors(podDir, p, serviceImageRefs, serviceInfos, serviceDescriptors); err != nil {
		return err
	}
	feedRegistry, err := describe.BuildFeedRegistry(serviceDescriptors)
	if err != nil {
		return fmt.Errorf("build feed registry: %w", err)
	}
	if err := resolveFeedSubscriptions(p, feedRegistry); err != nil {
		return err
	}
	for name, rc := range resolvedClaws {
		svc := p.Services[name]
		if svc == nil || svc.Claw == nil {
			continue
		}
		rc.Feeds = cloneResolvedFeeds(svc.Claw.Feeds)
	}

	cllamaEnabled, cllamaAgents := detectCllama(resolvedClaws)
	clawAPIAuth, err := prepareClawAPIRuntime(runtimeDir, p, resolvedClaws)
	if err != nil {
		return err
	}
	runtimeEnv, err := loadRuntimeEnv(podDir)
	if err != nil {
		return err
	}
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
			if err := validateCllamaEnvFiles(podDir, name, svc); err != nil {
				return err
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
					ordinalAuth := lookupServiceAuth(clawAPIAuth, ordinalName)
					feeds, err := buildFeedManifestEntries(p, serviceDescriptors, runtimeEnv, name, ordinalAuth)
					if err != nil {
						return fmt.Errorf("service %q: build feed manifest: %w", ordinalName, err)
					}
					md := shared.GenerateClawdapusMD(&ordinalRC, p.Name)
					contextInputs = append(contextInputs, cllama.AgentContextInput{
						AgentID:     ordinalName,
						AgentsMD:    string(agentContent),
						ClawdapusMD: md,
						Feeds:       feeds,
						ServiceAuth: ordinalAuth,
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

			svcAuth := lookupServiceAuth(clawAPIAuth, name)
			feeds, err := buildFeedManifestEntries(p, serviceDescriptors, runtimeEnv, name, svcAuth)
			if err != nil {
				return fmt.Errorf("service %q: build feed manifest: %w", name, err)
			}
			md := shared.GenerateClawdapusMD(rc, p.Name)
			contextInputs = append(contextInputs, cllama.AgentContextInput{
				AgentID:     name,
				AgentsMD:    string(agentContent),
				ClawdapusMD: md,
				Feeds:       feeds,
				ServiceAuth: svcAuth,
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

	if err := ensureInfraImages(cllamaEnabled, proxies, p.ClawAPI, p.Clawdash); err != nil {
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

	runtimeConsumers := runtimeConsumerServices(resolvedClaws, proxies, p.ClawAPI, p.Clawdash)
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

func runtimeConsumerServices(resolvedClaws map[string]*driver.ResolvedClaw, proxies []pod.CllamaProxyConfig, api *pod.ClawAPIConfig, dash *pod.ClawdashConfig) []string {
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

	if api != nil {
		if _, ok := seen["claw-api"]; !ok {
			names = append(names, "claw-api")
		}
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
	var expandErr error
	expand := func(value string) string {
		if expandErr != nil {
			return value
		}
		expanded, err := expandRuntimeValue(value, env)
		if err != nil {
			expandErr = err
			return value
		}
		return expanded
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
		for i := range svc.Claw.Feeds {
			svc.Claw.Feeds[i].Name = expand(svc.Claw.Feeds[i].Name)
			svc.Claw.Feeds[i].Source = expand(svc.Claw.Feeds[i].Source)
			svc.Claw.Feeds[i].Path = expand(svc.Claw.Feeds[i].Path)
			svc.Claw.Feeds[i].Description = expand(svc.Claw.Feeds[i].Description)
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
					if err := expandDiscordChannelAdmission(p, cc, expand); err != nil {
						return err
					}
				}
			}
		}
	}
	if expandErr != nil {
		return expandErr
	}
	return nil
}

func expandDiscordChannelAdmission(p *pod.Pod, cc *driver.ChannelConfig, expand func(string) string) error {
	if cc == nil || (!cc.AllowFromHandles && len(cc.AllowFromServices) == 0) {
		return nil
	}

	derived := make([]string, 0)
	if cc.AllowFromHandles {
		derived = append(derived, discordHandleIDsFromPod(p)...)
	}

	serviceIDs, err := discordServiceUserIDs(p, cc.AllowFromServices, expand)
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

func buildFeedManifestEntries(p *pod.Pod, descriptors map[string]*describe.ServiceDescriptor, runtimeEnv map[string]string, serviceName string, serviceAuth []cllama.ServiceAuthEntry) ([]cllama.FeedManifestEntry, error) {
	svc := p.Services[serviceName]
	if svc == nil || svc.Claw == nil || len(svc.Claw.Feeds) == 0 {
		return nil, nil
	}

	// Index service auth by service name for feed auth lookup
	authByService := make(map[string]string)
	for _, entry := range serviceAuth {
		if entry.AuthType == "bearer" && entry.Token != "" {
			authByService[entry.Service] = entry.Token
		}
	}

	entries := make([]cllama.FeedManifestEntry, 0, len(svc.Claw.Feeds))
	for _, feed := range svc.Claw.Feeds {
		if feed.Unresolved {
			return nil, fmt.Errorf("feed %q is still unresolved", feed.Name)
		}
		feedPath := strings.ReplaceAll(feed.Path, "{claw_id}", serviceName)
		feedName := strings.ReplaceAll(feed.Name, "{claw_id}", serviceName)
		url, err := resolveFeedURL(p, feed.Source, feedPath)
		if err != nil {
			return nil, fmt.Errorf("feed %q: %w", feedName, err)
		}
		entry := cllama.FeedManifestEntry{
			Name:   feedName,
			Source: feed.Source,
			Path:   feedPath,
			TTL:    feed.TTL,
			URL:    url,
		}
		if token, ok := authByService[feed.Source]; ok {
			entry.Auth = token
		} else if descriptor := descriptors[feed.Source]; descriptor != nil {
			token, err := resolveFeedAuthFromServiceEnv(svc.Environment, descriptor, runtimeEnv)
			if err != nil {
				return nil, fmt.Errorf("feed %q: %w", feedName, err)
			}
			entry.Auth = token
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func resolveFeedAuthFromServiceEnv(env map[string]string, descriptor *describe.ServiceDescriptor, runtimeEnv map[string]string) (string, error) {
	if descriptor == nil || descriptor.Auth == nil || descriptor.Auth.Type != "bearer" || descriptor.Auth.Env == "" {
		return "", nil
	}
	raw := strings.TrimSpace(env[descriptor.Auth.Env])
	if raw == "" {
		return "", fmt.Errorf("feed auth: service env has no value for %q (required by descriptor auth.env for bearer auth)", descriptor.Auth.Env)
	}
	if !strings.Contains(raw, "${") {
		return raw, nil
	}

	expanded, err := expandRuntimeValue(raw, runtimeEnv)
	if err != nil {
		return "", fmt.Errorf("resolve %s from service environment: %w", descriptor.Auth.Env, err)
	}
	return expanded, nil
}

func resolveFeedURL(p *pod.Pod, source string, feedPath string) (string, error) {
	if p.ClawAPI != nil && source == "claw-api" {
		return buildFeedURL(source, clawAPIInternalPort(p.ClawAPI.Addr), feedPath), nil
	}
	svc, ok := p.Services[source]
	if !ok {
		return "", fmt.Errorf("targets unknown service %q", source)
	}
	port := "80"
	if svc != nil {
		ports := mergedPorts(svc.Expose, svc.Ports)
		if len(ports) > 0 && strings.TrimSpace(ports[0]) != "" {
			port = strings.TrimSpace(ports[0])
		}
	}
	return buildFeedURL(source, port, feedPath), nil
}

func buildFeedURL(source, port, feedPath string) string {
	return fmt.Sprintf("http://%s:%s%s", source, port, feedPath)
}

func clawAPIInternalPort(addr string) string {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return "8080"
	}
	if strings.HasPrefix(addr, ":") {
		return strings.TrimPrefix(addr, ":")
	}
	if idx := strings.LastIndex(addr, ":"); idx >= 0 && idx < len(addr)-1 {
		return addr[idx+1:]
	}
	return "8080"
}

func prepareClawAPIRuntime(runtimeDir string, p *pod.Pod, resolvedClaws map[string]*driver.ResolvedClaw) (map[string]cllama.ServiceAuthEntry, error) {
	if p == nil || p.ClawAPI == nil || strings.TrimSpace(p.Master) == "" {
		return nil, nil
	}

	rc := resolvedClaws[p.Master]
	if rc == nil {
		return nil, fmt.Errorf("master service %q did not resolve to a claw", p.Master)
	}
	masterSvc := p.Services[p.Master]
	if masterSvc == nil {
		return nil, fmt.Errorf("master service %q not found in pod services", p.Master)
	}

	clawAPIURL := fmt.Sprintf("http://claw-api:%s", clawAPIInternalPort(p.ClawAPI.Addr))

	// 1. Build master principal (full access, pod-wide).
	masterPrincipal, err := clawapi.BuildMasterPrincipal(p.Name, p.Master)
	if err != nil {
		return nil, err
	}
	auto := []clawapi.Principal{masterPrincipal}

	// 2. Build self principals for services declaring claw-api: self.
	for name, svc := range p.Services {
		if name == p.Master || svc.Claw == nil || svc.Claw.ClawAPIMode != "self" {
			continue
		}
		sp, err := clawapi.BuildSelfPrincipal(p.Name, name)
		if err != nil {
			return nil, fmt.Errorf("service %q: build self principal: %w", name, err)
		}
		auto = append(auto, sp)
	}

	// 3. Merge with explicit pod-level principals.
	merged, err := mergePrincipals(auto, p.Principals, p.Name)
	if err != nil {
		return nil, fmt.Errorf("merge principals: %w", err)
	}

	// 4. Write principals.json.
	principals := make([]clawapi.Principal, len(merged))
	for i, m := range merged {
		principals[i] = m.Principal
	}
	store := clawapi.Store{Principals: principals}
	principalsDir := filepath.Dir(p.ClawAPI.PrincipalsHostPath)
	if err := os.MkdirAll(principalsDir, 0700); err != nil {
		return nil, fmt.Errorf("create claw-api runtime dir under %q: %w", runtimeDir, err)
	}
	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal claw-api principals: %w", err)
	}
	if err := writeRuntimeFile(p.ClawAPI.PrincipalsHostPath, append(data, '\n'), 0600); err != nil {
		return nil, fmt.Errorf("write claw-api principals: %w", err)
	}

	// 5. Inject tokens into services.
	injectClawAPIEnv := func(svc *pod.Service, token string) {
		if svc.Environment == nil {
			svc.Environment = make(map[string]string)
		}
		svc.Environment["CLAW_API_URL"] = clawAPIURL
		svc.Environment["CLAW_API_TOKEN"] = token
	}

	// Master always gets its token.
	injectClawAPIEnv(masterSvc, masterPrincipal.Token)

	// Self principals and explicit inject-into targets.
	for _, m := range merged {
		if m.Principal.Name == p.Master {
			continue // already handled
		}
		// Self principal: inject into the service that declared claw-api: self.
		if svc, ok := p.Services[m.Principal.Name]; ok && svc.Claw != nil && svc.Claw.ClawAPIMode == "self" {
			injectClawAPIEnv(svc, m.Principal.Token)
		}
		// Explicit inject-into.
		if m.InjectInto != "" {
			if svc, ok := p.Services[m.InjectInto]; ok {
				injectClawAPIEnv(svc, m.Principal.Token)
			}
		}
	}

	// 6. Build cllama service auth entries for the master (feed fetching).
	authEntry := cllama.ServiceAuthEntry{
		Service:   "claw-api",
		AuthType:  "bearer",
		Token:     masterPrincipal.Token,
		Principal: masterPrincipal.Name,
	}
	serviceAuth := make(map[string]cllama.ServiceAuthEntry)
	count := 1
	if rc.Count > 0 {
		count = rc.Count
	}
	for _, agentID := range expandedServiceNames(p.Master, count) {
		serviceAuth[agentID] = authEntry
	}
	return serviceAuth, nil
}

func lookupServiceAuth(entries map[string]cllama.ServiceAuthEntry, agentID string) []cllama.ServiceAuthEntry {
	if len(entries) == 0 {
		return nil
	}
	entry, ok := entries[agentID]
	if !ok {
		return nil
	}
	return []cllama.ServiceAuthEntry{entry}
}

func discordServiceUserIDs(p *pod.Pod, serviceNames []string, expand func(string) string) ([]string, error) {
	ids := make([]string, 0, len(serviceNames))
	for _, name := range serviceNames {
		svc, ok := p.Services[name]
		if !ok {
			return nil, fmt.Errorf("channel://discord allow_from_services references unknown service %q", name)
		}
		id := discordUserIDFromService(svc, expand)
		if id == "" {
			return nil, fmt.Errorf("channel://discord allow_from_services service %q has no Discord bot identity; expected DISCORD_BOT_TOKEN or DISCORD_TRADING_API_BOT_TOKEN", name)
		}
		ids = append(ids, id)
	}
	return uniqueSortedStrings(ids), nil
}

func discordUserIDFromService(svc *pod.Service, expand func(string) string) string {
	if svc == nil {
		return ""
	}
	for _, key := range []string{"DISCORD_BOT_TOKEN", "DISCORD_TRADING_API_BOT_TOKEN"} {
		if token := strings.TrimSpace(expand(svc.Environment[key])); token != "" {
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

func expandRuntimeValue(value string, env map[string]string) (string, error) {
	if value == "" {
		return "", nil
	}

	var expandErr error
	expanded := runtimePlaceholderPattern.ReplaceAllStringFunc(value, func(match string) string {
		if expandErr != nil {
			return match
		}
		resolved, err := resolveRuntimePlaceholder(match, env)
		if err != nil {
			expandErr = err
			return match
		}
		return resolved
	})
	if expandErr != nil {
		return "", expandErr
	}
	if unresolved := runtimePlaceholderPattern.FindString(expanded); unresolved != "" {
		return "", fmt.Errorf("unresolved x-claw placeholder %q", unresolved)
	}
	return expanded, nil
}

func resolveRuntimePlaceholder(match string, env map[string]string) (string, error) {
	expr := strings.TrimSpace(match[2 : len(match)-1])
	name, op, operand, err := parseRuntimePlaceholderExpr(expr)
	if err != nil {
		return "", err
	}

	value, isSet := env[name]
	nonEmpty := strings.TrimSpace(value) != ""

	resolveOperand := func() (string, error) {
		return expandRuntimeValue(operand, env)
	}

	switch op {
	case "":
		if !isSet {
			return "", fmt.Errorf("unresolved x-claw placeholder %q", match)
		}
		return value, nil
	case ":-":
		if !isSet || !nonEmpty {
			return resolveOperand()
		}
		return value, nil
	case "-":
		if !isSet {
			return resolveOperand()
		}
		return value, nil
	case ":?":
		if !isSet || !nonEmpty {
			msg := strings.TrimSpace(operand)
			if msg == "" {
				msg = fmt.Sprintf("%s is required", name)
			}
			return "", fmt.Errorf("%s", msg)
		}
		return value, nil
	case "?":
		if !isSet {
			msg := strings.TrimSpace(operand)
			if msg == "" {
				msg = fmt.Sprintf("%s is required", name)
			}
			return "", fmt.Errorf("%s", msg)
		}
		return value, nil
	case ":+":
		if isSet && nonEmpty {
			return resolveOperand()
		}
		return "", nil
	case "+":
		if isSet {
			return resolveOperand()
		}
		return "", nil
	default:
		return "", fmt.Errorf("unsupported x-claw placeholder operator %q", op)
	}
}

func parseRuntimePlaceholderExpr(expr string) (name, op, operand string, err error) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return "", "", "", fmt.Errorf("empty x-claw placeholder")
	}

	for _, candidate := range []string{":-", ":?", ":+", "-", "?", "+"} {
		if idx := strings.Index(expr, candidate); idx > 0 {
			name = strings.TrimSpace(expr[:idx])
			op = candidate
			operand = expr[idx+len(candidate):]
			break
		}
	}
	if name == "" {
		name = expr
	}
	if !isRuntimeEnvName(name) {
		return "", "", "", fmt.Errorf("invalid x-claw placeholder %q", expr)
	}
	return name, op, operand, nil
}

func isRuntimeEnvName(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if i == 0 && !(r == '_' || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z')) {
			return false
		}
		if !(r == '_' || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')) {
			return false
		}
	}
	return true
}

func readDotEnvFile(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	out := make(map[string]string)
	scanner := bufio.NewScanner(f)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		key, value, ok, err := parseDotEnvLine(scanner.Text())
		if err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, lineNo, err)
		}
		if !ok {
			continue
		}
		out[key] = value
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func parseDotEnvLine(line string) (key, value string, ok bool, err error) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return "", "", false, nil
	}
	if strings.HasPrefix(line, "export ") {
		line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
	}

	eq := strings.IndexByte(line, '=')
	if eq < 0 {
		key = strings.TrimSpace(line)
		if key == "" {
			return "", "", false, fmt.Errorf("dotenv key must not be empty")
		}
		return key, "", true, nil
	}

	key = strings.TrimSpace(line[:eq])
	if key == "" {
		return "", "", false, fmt.Errorf("dotenv key must not be empty")
	}
	value, err = parseDotEnvValue(line[eq+1:])
	if err != nil {
		return "", "", false, err
	}
	return key, value, true, nil
}

func parseDotEnvValue(raw string) (string, error) {
	raw = strings.TrimLeft(raw, " \t")
	if raw == "" {
		return "", nil
	}

	switch raw[0] {
	case '\'':
		return parseSingleQuotedDotEnvValue(raw)
	case '"':
		return parseDoubleQuotedDotEnvValue(raw)
	default:
		return trimDotEnvComment(raw), nil
	}
}

func parseSingleQuotedDotEnvValue(raw string) (string, error) {
	end := strings.Index(raw[1:], "'")
	if end < 0 {
		return "", fmt.Errorf("unterminated single-quoted dotenv value")
	}
	end++
	value := raw[1:end]
	rest := strings.TrimSpace(raw[end+1:])
	if rest != "" && !strings.HasPrefix(rest, "#") {
		return "", fmt.Errorf("invalid trailing content after single-quoted dotenv value")
	}
	return value, nil
}

func parseDoubleQuotedDotEnvValue(raw string) (string, error) {
	var b strings.Builder
	escaped := false
	for i := 1; i < len(raw); i++ {
		ch := raw[i]
		if escaped {
			switch ch {
			case 'n':
				b.WriteByte('\n')
			case 'r':
				b.WriteByte('\r')
			case 't':
				b.WriteByte('\t')
			case '\\', '"', '$':
				b.WriteByte(ch)
			default:
				b.WriteByte(ch)
			}
			escaped = false
			continue
		}
		if ch == '\\' {
			escaped = true
			continue
		}
		if ch == '"' {
			rest := strings.TrimSpace(raw[i+1:])
			if rest != "" && !strings.HasPrefix(rest, "#") {
				return "", fmt.Errorf("invalid trailing content after double-quoted dotenv value")
			}
			return b.String(), nil
		}
		b.WriteByte(ch)
	}
	return "", fmt.Errorf("unterminated double-quoted dotenv value")
}

func trimDotEnvComment(raw string) string {
	for i := 0; i < len(raw); i++ {
		if raw[i] != '#' {
			continue
		}
		if i == 0 || raw[i-1] == ' ' || raw[i-1] == '\t' {
			return strings.TrimSpace(raw[:i])
		}
	}
	return strings.TrimSpace(raw)
}

type envFileRef struct {
	Path     string
	Required bool
}

func validateCllamaEnvFiles(podDir, serviceName string, svc *pod.Service) error {
	if svc == nil {
		return nil
	}

	refs, err := resolveEnvFileRefs(podDir, svc.Compose["env_file"])
	if err != nil {
		return fmt.Errorf("service %q: %w", serviceName, err)
	}
	for _, ref := range refs {
		env, err := readDotEnvFile(ref.Path)
		if err != nil {
			if os.IsNotExist(err) && !ref.Required {
				continue
			}
			return fmt.Errorf("service %q: inspect env_file %q for credential starvation: %w", serviceName, ref.Path, err)
		}
		for key := range env {
			if isProviderKey(key) {
				return fmt.Errorf("service %q: provider key %q found in env_file %q; cllama requires credential starvation", serviceName, key, ref.Path)
			}
		}
	}
	return nil
}

func resolveEnvFileRefs(baseDir string, raw interface{}) ([]envFileRef, error) {
	if raw == nil {
		return nil, nil
	}

	switch v := raw.(type) {
	case string:
		ref, err := parseEnvFileRef(baseDir, v)
		if err != nil {
			return nil, err
		}
		return []envFileRef{ref}, nil
	case []string:
		refs := make([]envFileRef, 0, len(v))
		for i, item := range v {
			ref, err := parseEnvFileRef(baseDir, item)
			if err != nil {
				return nil, fmt.Errorf("env_file[%d]: %w", i, err)
			}
			refs = append(refs, ref)
		}
		return refs, nil
	case []interface{}:
		refs := make([]envFileRef, 0, len(v))
		for i, item := range v {
			ref, err := parseEnvFileRef(baseDir, item)
			if err != nil {
				return nil, fmt.Errorf("env_file[%d]: %w", i, err)
			}
			refs = append(refs, ref)
		}
		return refs, nil
	default:
		return nil, fmt.Errorf("unsupported env_file type %T", raw)
	}
}

func parseEnvFileRef(baseDir string, raw interface{}) (envFileRef, error) {
	ref := envFileRef{Required: true}

	switch v := raw.(type) {
	case string:
		ref.Path = strings.TrimSpace(v)
	case map[string]interface{}:
		path, ok := v["path"].(string)
		if !ok || strings.TrimSpace(path) == "" {
			return envFileRef{}, fmt.Errorf("env_file map entries must include a non-empty path")
		}
		ref.Path = strings.TrimSpace(path)
		if required, ok := v["required"].(bool); ok {
			ref.Required = required
		}
	default:
		return envFileRef{}, fmt.Errorf("unsupported env_file entry type %T", raw)
	}

	if ref.Path == "" {
		return envFileRef{}, fmt.Errorf("env_file path must not be empty")
	}
	if !filepath.IsAbs(ref.Path) {
		ref.Path = filepath.Join(baseDir, ref.Path)
	}
	return ref, nil
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

func materializeImageSkill(serviceName, runtimeDir, imageRef, skillPath string) (*driver.ResolvedSkill, error) {
	name := filepath.Base(skillPath)
	if name == "." || name == "/" || strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("service %q: emitted skill path %q has invalid filename", serviceName, skillPath)
	}

	content, err := extractServiceSkillFromImage(imageRef, skillPath)
	if err != nil {
		return nil, fmt.Errorf("extract skill %q from %q: %w", skillPath, imageRef, err)
	}

	hostPath := filepath.Join(runtimeDir, "skills", name)
	if err := os.MkdirAll(filepath.Dir(hostPath), 0700); err != nil {
		return nil, fmt.Errorf("service %q: create skill dir: %w", serviceName, err)
	}
	if err := writeRuntimeFile(hostPath, content, 0644); err != nil {
		return nil, fmt.Errorf("write emitted skill %q: %w", skillPath, err)
	}

	return &driver.ResolvedSkill{Name: name, HostPath: hostPath}, nil
}

func resolveServiceSurfaceSkills(podDir, runtimeDir string, p *pod.Pod, surfaces []driver.ResolvedSurface, imageRefs map[string]string, infos map[string]*inspect.ClawInfo, descriptors map[string]*describe.ServiceDescriptor) ([]driver.ResolvedSurface, []driver.ResolvedSkill, error) {
	resolvedSurfaces := append([]driver.ResolvedSurface(nil), surfaces...)
	generated := make([]driver.ResolvedSkill, 0)
	seen := make(map[string]struct{}, len(surfaces))

	for i, surface := range resolvedSurfaces {
		if surface.Scheme != "service" {
			continue
		}

		if surface.Target == "" {
			return nil, nil, fmt.Errorf("invalid service target for generated surface: %q", surface.Target)
		}

		if surface.Target == "claw-api" && p.ClawAPI != nil {
			resolvedSurfaces[i].ServiceInfo = buildServiceSurfaceInfo(builtinClawAPIDescriptor())
			name := surfaceFallbackSkillName(surface.Target)
			resolvedSurfaces[i].SkillName = name
			if _, exists := seen[name]; !exists {
				seen[name] = struct{}{}
				skillPath := filepath.Join(runtimeDir, "skills", name)
				if err := os.MkdirAll(filepath.Dir(skillPath), 0700); err != nil {
					return nil, nil, fmt.Errorf("create generated skill dir: %w", err)
				}
				content := clawapi.GenerateServiceSkill(clawAPIInternalPort(p.ClawAPI.Addr))
				if err := writeRuntimeFile(skillPath, []byte(content), 0644); err != nil {
					return nil, nil, fmt.Errorf("write generated claw-api skill %q: %w", name, err)
				}
				generated = append(generated, driver.ResolvedSkill{Name: name, HostPath: skillPath})
			}
			continue
		}

		if targetSvc, ok := p.Services[surface.Target]; ok {
			imageRef, info, descriptor, err := resolveServiceMetadata(podDir, p, surface.Target, targetSvc, imageRefs, infos, descriptors)
			if err != nil {
				return nil, nil, fmt.Errorf("inspect target service %q: %w", surface.Target, err)
			}
			resolvedSurfaces[i].ServiceInfo = buildServiceSurfaceInfo(descriptor)

			if descriptor != nil && strings.TrimSpace(descriptor.Skill) != "" {
				descriptorSkill, err := resolveDescriptorSkill(surface.Target, podDir, runtimeDir, imageRef, targetSvc, descriptor)
				if err != nil {
					return nil, nil, fmt.Errorf("resolve descriptor skill for target service %q: %w", surface.Target, err)
				}
				if descriptorSkill != nil {
					resolvedSurfaces[i].SkillName = descriptorSkill.Name
					if _, exists := seen[descriptorSkill.Name]; !exists {
						seen[descriptorSkill.Name] = struct{}{}
						generated = append(generated, *descriptorSkill)
					}
					continue
				}
			}
			if info != nil && strings.TrimSpace(info.SkillEmit) != "" && imageRef != "" {
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
				}
			}
		}
	}

	return resolvedSurfaces, generated, nil
}

func surfaceFallbackSkillName(target string) string {
	return fmt.Sprintf("surface-%s.md", strings.TrimSpace(strings.ReplaceAll(target, "/", "-")))
}

func resolveServiceMetadata(podDir string, p *pod.Pod, serviceName string, svc *pod.Service, imageRefs map[string]string, infos map[string]*inspect.ClawInfo, descriptors map[string]*describe.ServiceDescriptor) (string, *inspect.ClawInfo, *describe.ServiceDescriptor, error) {
	if descriptor, ok := descriptors[serviceName]; ok {
		return imageRefs[serviceName], infos[serviceName], descriptor, nil
	}

	if serviceName == "claw-api" && p != nil && p.ClawAPI != nil {
		descriptor := builtinClawAPIDescriptor()
		descriptors[serviceName] = descriptor
		return "", nil, descriptor, nil
	}

	imageRef, info, err := inspectServiceMetadata(podDir, p, serviceName, svc, imageRefs, infos)
	if err != nil {
		return "", nil, nil, err
	}

	var descriptor *describe.ServiceDescriptor
	if info != nil && strings.TrimSpace(info.DescribePath) != "" && imageRef != "" {
		if imageExistsLocally(imageRef) {
			descriptor, err = loadDescriptorFromImage(imageRef, info.DescribePath)
			if err != nil {
				return "", nil, nil, fmt.Errorf("load descriptor from image: %w", err)
			}
		}
	}
	if descriptor == nil && svc != nil {
		descriptorPath := ""
		if info != nil {
			descriptorPath = info.DescribePath
		}
		descriptor, _, err = loadDescriptorFromBuildCtx(podDir, svc.Compose["build"], descriptorPath)
		if err != nil {
			return "", nil, nil, fmt.Errorf("load descriptor from build context: %w", err)
		}
	}

	descriptors[serviceName] = descriptor
	return imageRef, info, descriptor, nil
}

func inspectServiceMetadata(podDir string, p *pod.Pod, serviceName string, svc *pod.Service, imageRefs map[string]string, infos map[string]*inspect.ClawInfo) (string, *inspect.ClawInfo, error) {
	if imageRef, ok := imageRefs[serviceName]; ok {
		return imageRef, infos[serviceName], nil
	}

	if svc == nil {
		return "", nil, nil
	}

	imageRef := strings.TrimSpace(svc.Image)
	if svc.Claw != nil {
		var err error
		imageRef, err = resolveManagedServiceImage(podDir, p, serviceName, svc)
		if err != nil {
			return "", nil, err
		}
	}

	var info *inspect.ClawInfo
	if imageRef != "" && imageExistsLocally(imageRef) {
		var err error
		info, err = inspectClawImage(imageRef)
		if err != nil {
			return "", nil, err
		}
	}
	imageRefs[serviceName] = imageRef
	infos[serviceName] = info
	return imageRef, info, nil
}

func collectServiceDescriptors(podDir string, p *pod.Pod, imageRefs map[string]string, infos map[string]*inspect.ClawInfo, descriptors map[string]*describe.ServiceDescriptor) error {
	if p == nil {
		return nil
	}
	if p.ClawAPI != nil {
		descriptors["claw-api"] = builtinClawAPIDescriptor()
	}

	serviceNames := make([]string, 0, len(p.Services))
	for name := range p.Services {
		serviceNames = append(serviceNames, name)
	}
	sort.Strings(serviceNames)

	for _, name := range serviceNames {
		svc := p.Services[name]
		if _, _, _, err := resolveServiceMetadata(podDir, p, name, svc, imageRefs, infos, descriptors); err != nil {
			return fmt.Errorf("service %q: resolve descriptor: %w", name, err)
		}
	}
	return nil
}

func resolveFeedSubscriptions(p *pod.Pod, registry map[string]describe.FeedSpec) error {
	for serviceName, svc := range p.Services {
		if svc == nil || svc.Claw == nil {
			continue
		}
		for i := range svc.Claw.Feeds {
			feed := &svc.Claw.Feeds[i]
			if !feed.Unresolved {
				continue
			}
			spec, ok := registry[feed.Name]
			if !ok {
				return fmt.Errorf("service %q: feed %q was not found in the descriptor registry", serviceName, feed.Name)
			}
			feed.Source = spec.Source
			feed.Path = spec.Path
			feed.TTL = spec.TTL
			feed.Description = spec.Description
			feed.Unresolved = false
		}
	}
	return nil
}

func cloneResolvedFeeds(feeds []pod.FeedEntry) []driver.ResolvedFeed {
	if len(feeds) == 0 {
		return nil
	}
	out := make([]driver.ResolvedFeed, 0, len(feeds))
	for _, feed := range feeds {
		out = append(out, driver.ResolvedFeed{
			Name:        feed.Name,
			Source:      feed.Source,
			Path:        feed.Path,
			TTL:         feed.TTL,
			Description: feed.Description,
		})
	}
	return out
}

func resolveDescriptorSkill(serviceName, podDir, runtimeDir, imageRef string, svc *pod.Service, descriptor *describe.ServiceDescriptor) (*driver.ResolvedSkill, error) {
	if descriptor == nil || strings.TrimSpace(descriptor.Skill) == "" {
		return nil, nil
	}
	if imageRef != "" && imageExistsLocally(imageRef) {
		return materializeImageSkill(serviceName, runtimeDir, imageRef, descriptor.Skill)
	}
	if svc == nil {
		return nil, fmt.Errorf("descriptor skill %q requires either a local image or build context", descriptor.Skill)
	}

	hostPath, err := resolveBuildContextFile(podDir, svc.Compose["build"], descriptor.Skill)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(hostPath) == "" {
		return nil, fmt.Errorf("descriptor skill %q was not found in the build context", descriptor.Skill)
	}
	return &driver.ResolvedSkill{
		Name:     filepath.Base(hostPath),
		HostPath: hostPath,
	}, nil
}

func buildServiceSurfaceInfo(descriptor *describe.ServiceDescriptor) *driver.ServiceSurfaceInfo {
	if descriptor == nil {
		return nil
	}
	info := &driver.ServiceSurfaceInfo{
		Description: descriptor.Description,
	}
	if descriptor.Auth != nil {
		info.AuthType = descriptor.Auth.Type
		info.AuthEnv = descriptor.Auth.Env
	}
	if len(descriptor.Endpoints) > 0 {
		info.Endpoints = make([]driver.ServiceEndpoint, 0, len(descriptor.Endpoints))
		for _, endpoint := range descriptor.Endpoints {
			info.Endpoints = append(info.Endpoints, driver.ServiceEndpoint{
				Method:      endpoint.Method,
				Path:        endpoint.Path,
				Description: endpoint.Description,
			})
		}
	}
	return info
}

func builtinClawAPIDescriptor() *describe.ServiceDescriptor {
	return &describe.ServiceDescriptor{
		Version:     1,
		Description: "Read-only governance API for fleet telemetry, health, logs, metrics, and alerts.",
		Feeds: []describe.FeedDescriptor{{
			Name:        "fleet-alerts",
			Path:        "/fleet/alerts",
			TTL:         30,
			Description: "Threshold-based fleet alert summaries for the pod.",
		}},
		Endpoints: []describe.EndpointDescriptor{
			{Method: "GET", Path: "/fleet/status", Description: "Scoped service health and uptime."},
			{Method: "GET", Path: "/fleet/logs", Description: "Recent logs for one in-scope service."},
			{Method: "GET", Path: "/fleet/metrics", Description: "Normalized telemetry for one claw."},
			{Method: "GET", Path: "/fleet/alerts", Description: "Threshold-based anomaly summaries across the fleet."},
		},
		Auth: &describe.AuthDescriptor{
			Type: "bearer",
			Env:  "CLAW_API_TOKEN",
		},
	}
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

func collectClawAlertEnv() map[string]string {
	env := make(map[string]string)
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "CLAW_ALERT_") {
			parts := strings.SplitN(kv, "=", 2)
			if len(parts) == 2 {
				env[parts[0]] = parts[1]
			}
		}
	}
	if len(env) == 0 {
		return nil
	}
	return env
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
	Args       map[string]buildArgValue
	Target     string
}

type buildArgValue struct {
	Value       string
	Passthrough bool
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

func parseBuildArgs(raw interface{}) (map[string]buildArgValue, error) {
	if raw == nil {
		return nil, nil
	}

	switch v := raw.(type) {
	case map[string]string:
		out := make(map[string]buildArgValue, len(v))
		for k, value := range v {
			out[k] = buildArgValue{Value: value}
		}
		return out, nil
	case map[string]interface{}:
		out := make(map[string]buildArgValue, len(v))
		for k, value := range v {
			if value == nil {
				out[k] = buildArgValue{Passthrough: true}
				continue
			}
			s, err := buildScalarString(value)
			if err != nil {
				return nil, fmt.Errorf("key %q: %w", k, err)
			}
			out[k] = buildArgValue{Value: s}
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

func parseBuildArgList(items []string) (map[string]buildArgValue, error) {
	out := make(map[string]buildArgValue, len(items))
	for i, item := range items {
		key := item
		value := buildArgValue{Passthrough: true}
		if idx := strings.Index(item, "="); idx >= 0 {
			key = item[:idx]
			value = buildArgValue{Value: item[idx+1:]}
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
		if err := buildGeneratedImage(generatedPath, imageRef, contextDir); err != nil {
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

func dockerBuildTaggedImageDefault(imageRef, dockerfilePath, contextDir string, args map[string]buildArgValue, target string) error {
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
		arg := args[key]
		if arg.Passthrough {
			cmdArgs = append(cmdArgs, "--build-arg", key)
			continue
		}
		cmdArgs = append(cmdArgs, "--build-arg", key+"="+arg.Value)
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

// ensureInfraImages checks that cllama proxy, claw-api, and clawdash images exist locally,
// building them from source when missing.
func ensureInfraImages(cllamaEnabled bool, proxies []pod.CllamaProxyConfig, api *pod.ClawAPIConfig, dash *pod.ClawdashConfig) error {
	if cllamaEnabled {
		for _, proxy := range proxies {
			if err := ensureImage(proxy.Image, "cllama", "cllama/Dockerfile", "cllama"); err != nil {
				return err
			}
		}
	}
	if api != nil {
		if err := ensureImage(api.Image, "claw-api", "dockerfiles/claw-api/Dockerfile", "."); err != nil {
			return err
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
