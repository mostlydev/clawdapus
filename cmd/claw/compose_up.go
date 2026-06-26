package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

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
	"github.com/mostlydev/clawdapus/internal/schedule"
)

var composeUpDetach bool
var composeUpFix bool
var composeUpDiscoverTools bool

var runtimePlaceholderPattern = regexp.MustCompile(`\$\{([^}]+)\}`)

const (
	conversationWallServiceName     = "claw-wall"
	conversationWallFeedName        = "channel-context"
	conversationWallAwarenessName   = "channel-awareness"
	conversationWallFeedTTL         = 30
	conversationWallFeedSince       = "24h"
	conversationWallFeedLimit       = 40
	conversationWallAwarenessLimit  = 60
	conversationWallFeedMaxChars    = 32 * 1024
	conversationWallBufferLimit     = 5000
	conversationWallPollInterval    = "30"
	conversationWallRetention       = "24h"
	conversationWallBackfillPages   = "25"
	conversationWallInternalPort    = "8080"
	conversationWallDockerfile      = "dockerfiles/claw-wall/Dockerfile"
	conversationWallDiscordBaseEnv  = "CLAW_WALL_DISCORD_BASE_URL"
	conversationWallToolTokenEnv    = "CLAW_WALL_TOOL_TOKEN"
	conversationWallAllowlistPath   = "/etc/claw-wall/agent-channels.json"
	conversationWallMemoryIngestEnv = "CLAW_WALL_CHANNEL_MEMORY_INGEST_URL"
	conversationWallMemoryDigestEnv = "CLAW_WALL_CHANNEL_MEMORY_DIGEST_URL"
	conversationWallMemorySearchEnv = "CLAW_WALL_CHANNEL_MEMORY_SEARCH_URL"
	conversationWallMemorySourceEnv = "CLAW_WALL_CHANNEL_MEMORY_SOURCE_URL"
	conversationWallMemoryTimeout   = "2s"
	clawInternalNetworkName         = "claw-internal"
	historyReplayAuthService        = "cllama-history"
	historyReplayBaseURL            = "http://cllama:8080/history"
)

var (
	extractServiceSkillFromImage = runtime.ExtractServiceSkill
	writeRuntimeFile             = os.WriteFile
	removePreviousRuntimeDir     = os.RemoveAll
	inspectClawImage             = inspect.Inspect
	imageExistsLocally           = build.ImageExistsLocally
	generateClawDockerfile       = build.Generate
	buildGeneratedImage          = build.BuildFromGenerated
	resolveLocalRunnerProvenance = build.ResolveLocalRunnerProvenance
	dockerBuildTaggedImage       = dockerBuildTaggedImageDefault
	findClawdapusRepoRoot        = findRepoRoot
	runInfraDockerCommand        = runInfraDockerCommandDefault
	runComposeDockerCommand      = runComposeDockerCommandDefault
	runRuntimeDescriptorCommand  = runRuntimeDescriptorCommandDefault
	refreshRuntimeDescriptor     = refreshRuntimeDescriptorDefault
	loadDescriptorFromFile       = describe.LoadFromFile
	loadDescriptorFromImage      = describe.LoadFromImage
	loadDescriptorFromBuildCtx   = describe.LoadFromBuildContext
	resolveBuildContextFile      = describe.ResolveBuildContextFile
	loadDockerfileMetadata       = inspect.LoadFromDockerfile
)

var (
	runtimeDescriptorRefreshTimeout      = 45 * time.Second
	runtimeDescriptorRefreshPollInterval = time.Second
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

func runComposeUp(podFile string) (err error) {
	p, podDir, err := loadPodDefinition(podFile)
	if err != nil {
		return err
	}

	servicePlans, err := planPodServiceImages(p)
	if err != nil {
		return err
	}
	if !composeUpFix {
		if plan := firstMissingPullPlan(servicePlans); plan != nil {
			return remediationErrorf("claw pull", "service %q image %q missing locally", plan.ServiceName, plan.ImageRef)
		}
		if plan := firstMissingBuildPlan(servicePlans); plan != nil {
			return remediationErrorf("claw build", "service %q image not built", plan.ServiceName)
		}
	} else {
		if firstMissingPullPlan(servicePlans) != nil {
			if err := pullRegistryServiceImages(servicePlans, true); err != nil {
				return err
			}
		}
		if firstMissingBuildPlan(servicePlans) != nil {
			if err := buildPlannedServiceImages(podFile, podDir, servicePlans, true); err != nil {
				return err
			}
		}
	}
	warnRunnerBaseDrift(podFile, servicePlans)
	if err := resolveRuntimePlaceholders(podDir, p); err != nil {
		return fmt.Errorf("resolve x-claw runtime placeholders: %w", err)
	}
	runtimeEnv, err := loadRuntimeEnv(podDir)
	if err != nil {
		return err
	}
	if composeUpDiscoverTools && podHasMCPStdioServices(p) {
		if _, err := discoverMCPStdioServices(context.Background(), podDir, p, nil, discoverSelectionMissingOrStale); err != nil {
			return fmt.Errorf("discover stdio MCP tools: %w", err)
		}
	}
	runtimeDir := filepath.Join(podDir, ".claw-runtime")
	memoryRoot, err := ensurePersistentCllamaDir(podDir, ".claw-memory")
	if err != nil {
		return err
	}
	skillRoot, err := ensurePersistentCllamaDir(podDir, ".claw-skills")
	if err != nil {
		return err
	}
	if err := preMigratePortableMemory(runtimeDir, memoryRoot, p); err != nil {
		return fmt.Errorf("migrate portable memory: %w", err)
	}
	runtimeStage, err := prepareRuntimeDir(runtimeDir)
	if err != nil {
		return fmt.Errorf("prepare runtime dir: %w", err)
	}
	composeApplied := false
	defer func() {
		if err == nil || runtimeStage == nil {
			return
		}
		if composeApplied {
			if runtimeStage.PreviousPath != "" {
				fmt.Printf("[claw] warning: preserving previous runtime dir at %s after failed apply\n", runtimeStage.PreviousPath)
			}
			return
		}
		if rollbackErr := runtimeStage.Rollback(); rollbackErr != nil {
			err = fmt.Errorf("%w (also failed to restore previous runtime dir: %v)", err, rollbackErr)
		}
	}()
	// Governance dir is separate from runtimeDir so it survives claw up resets.
	governanceDir := filepath.Join(podDir, ".claw-governance")
	if err := os.MkdirAll(governanceDir, 0o777); err != nil {
		return fmt.Errorf("create governance dir: %w", err)
	}
	// Reject claw-api: self and x-claw.principals without a master.
	if err := validateClawAPIDeclarations(p); err != nil {
		return err
	}

	needsScheduleAPI := hasPodInvokeEntries(p)
	if p.Master != "" || needsScheduleAPI {
		if _, exists := p.Services["claw-api"]; exists {
			return fmt.Errorf("service name %q is reserved when claw-api auto-injection is active", "claw-api")
		}
		p.ClawAPI = &pod.ClawAPIConfig{
			Image:              preferredInfraImageRef(infraComponentClawAPI),
			Addr:               envOrDefault("CLAW_API_ADDR", ":8080"),
			ManifestHostPath:   filepath.Join(runtimeDir, "pod-manifest.json"),
			ScheduleHostPath:   firstIf(needsScheduleAPI, filepath.Join(runtimeDir, "schedule.json")),
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
		if svc.IsAgentManaged() && len(svc.Claw.Handles) > 0 {
			podHandles[name] = svc.Claw.Handles
		}
	}

	for name, svc := range p.Services {
		if !svc.IsAgentManaged() {
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
			Models:        mergeModelSlots(info.Models, svc.Claw.Models),
			Handles:       svc.Claw.Handles,
			PeerHandles:   peerHandles,
			Includes:      resolvedIncludes,
			Feeds:         cloneResolvedFeeds(svc.Claw.Feeds),
			Configures:    info.Configures,
			Privileges:    info.Privileges,
			Count:         svc.Claw.Count,
			Environment:   svc.Environment,
			RuntimeEnv:    runtimeEnv,
			Surfaces:      surfaces,
			Skills:        skills,
			Cllama:        resolveCllama(info.Cllama, svc.Claw.Cllama),
			Hermes:        cloneHermesConfig(svc.Claw.Hermes),
		}
		if resolvedPersona != nil {
			rc.PersonaHostPath = resolvedPersona.HostPath
		}

		// Merge image-level invocations (from Clawfile INVOKE labels via inspect)
		for _, imgInv := range info.Invocations {
			rc.Invocations = append(rc.Invocations, driver.Invocation{
				ID:       driver.DeterministicInvocationID(name, driver.OriginImage, imgInv.Schedule, imgInv.Command),
				Schedule: imgInv.Schedule,
				Message:  imgInv.Command,
				Origin:   driver.OriginImage,
			})
		}

		// Merge pod-level invocations (x-claw.invoke), resolving platform/name targets to IDs when possible.
		for _, podInv := range svc.Claw.Invoke {
			inv := driver.Invocation{
				ID:       driver.DeterministicInvocationID(name, driver.OriginPod, podInv.Schedule, podInv.Message),
				Schedule: podInv.Schedule,
				Message:  podInv.Message,
				Name:     podInv.Name,
				Origin:   driver.OriginPod,
				When:     podInv.When.Clone(),
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
	if err := validateManagedCapabilityDeclarations(p, resolvedClaws); err != nil {
		return err
	}
	if err := injectConversationWall(p, resolvedClaws); err != nil {
		return err
	}

	capabilities, err := resolvePodCapabilities(podFile, podDir, p, serviceImageRefs, serviceInfos, serviceDescriptors)
	if err != nil {
		return err
	}
	resolvedTools := capabilities.Tools
	resolvedMemory := capabilities.Memory
	if err := attachCapabilityProvidersToInternalNetwork(p, resolvedTools, resolvedMemory); err != nil {
		return err
	}
	for name, rc := range resolvedClaws {
		svc := p.Services[name]
		if svc == nil || svc.Claw == nil {
			continue
		}
		rc.Feeds = cloneResolvedFeeds(svc.Claw.Feeds)
	}

	clawAPIAuth, err := prepareClawAPIRuntime(runtimeDir, p, resolvedClaws)
	if err != nil {
		return err
	}
	conversationWallAuth, conversationWallAllowlists, err := prepareConversationWallRuntime(runtimeDir, p, resolvedClaws)
	if err != nil {
		return err
	}
	for _, rc := range resolvedClaws {
		if rc == nil {
			continue
		}
		rc.Timezone = resolveAgentTimezone(rc.Environment, runtimeEnv)
	}
	proxies := make([]pod.CllamaProxyConfig, 0)
	cllamaDashboardPort := envOrDefault("CLLAMA_UI_PORT", "8181")
	if cllamaEnabled {
		historyReplayAuth, err := prepareHistoryReplayRuntime(p, resolvedClaws, resolvedMemory)
		if err != nil {
			return err
		}
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
					ordinalAuth := mergeServiceAuthEntries(
						lookupServiceAuth(clawAPIAuth, ordinalName),
						lookupServiceAuth(historyReplayAuth, ordinalName),
						lookupServiceAuth(conversationWallAuth, ordinalName),
					)
					feeds, err := buildFeedManifestEntries(p, serviceDescriptors, runtimeEnv, name, ordinalName, ordinalAuth)
					if err != nil {
						return fmt.Errorf("service %q: build feed manifest: %w", ordinalName, err)
					}
					tools, err := buildToolManifestEntries(p, serviceDescriptors, runtimeEnv, name, resolvedTools[name], ordinalAuth)
					if err != nil {
						return fmt.Errorf("service %q: build tool manifest: %w", ordinalName, err)
					}
					memory, err := buildMemoryManifestEntry(p, serviceDescriptors, runtimeEnv, name, resolvedMemory[name], ordinalAuth)
					if err != nil {
						return fmt.Errorf("service %q: build memory manifest: %w", ordinalName, err)
					}
					baseClawdapusMD := shared.GenerateClawdapusMD(&ordinalRC, p.Name)
					md := augmentClawdapusMD(baseClawdapusMD, tools, memory)
					effectiveAgentsMD := ""
					if ordinalRC.ClawType == "hermes" {
						effectiveAgentsMD = shared.ComposeEffectiveAgentsMD(string(agentContent), baseClawdapusMD)
					}
					contextInputs = append(contextInputs, cllama.AgentContextInput{
						AgentID:           ordinalName,
						AgentsMD:          string(agentContent),
						EffectiveAgentsMD: effectiveAgentsMD,
						ClawdapusMD:       md,
						Feeds:             feeds,
						Tools:             tools,
						ToolPolicy:        agentToolPolicy(p, name),
						Memory:            memory,
						RuntimeReminders:  agentRuntimeReminders(p, name),
						ServiceAuth:       ordinalAuth,
						ChannelAllowlist:  conversationWallAllowlists[ordinalName],
						Metadata: injectAgentBudget(cllama.InjectCompiledModelPolicy(map[string]any{
							"service":  name,
							"ordinal":  i,
							"pod":      p.Name,
							"type":     rc.ClawType,
							"token":    tokens[ordinalName],
							"timezone": rc.Timezone,
						}, rc.Models), agentBudgetPolicy(p, name)),
					})
				}
				continue
			}

			svcAuth := mergeServiceAuthEntries(
				lookupServiceAuth(clawAPIAuth, name),
				lookupServiceAuth(historyReplayAuth, name),
				lookupServiceAuth(conversationWallAuth, name),
			)
			feeds, err := buildFeedManifestEntries(p, serviceDescriptors, runtimeEnv, name, name, svcAuth)
			if err != nil {
				return fmt.Errorf("service %q: build feed manifest: %w", name, err)
			}
			tools, err := buildToolManifestEntries(p, serviceDescriptors, runtimeEnv, name, resolvedTools[name], svcAuth)
			if err != nil {
				return fmt.Errorf("service %q: build tool manifest: %w", name, err)
			}
			memory, err := buildMemoryManifestEntry(p, serviceDescriptors, runtimeEnv, name, resolvedMemory[name], svcAuth)
			if err != nil {
				return fmt.Errorf("service %q: build memory manifest: %w", name, err)
			}
			baseClawdapusMD := shared.GenerateClawdapusMD(rc, p.Name)
			md := augmentClawdapusMD(baseClawdapusMD, tools, memory)
			effectiveAgentsMD := ""
			if rc.ClawType == "hermes" {
				effectiveAgentsMD = shared.ComposeEffectiveAgentsMD(string(agentContent), baseClawdapusMD)
			}
			contextInputs = append(contextInputs, cllama.AgentContextInput{
				AgentID:           name,
				AgentsMD:          string(agentContent),
				EffectiveAgentsMD: effectiveAgentsMD,
				ClawdapusMD:       md,
				Feeds:             feeds,
				Tools:             tools,
				ToolPolicy:        agentToolPolicy(p, name),
				Memory:            memory,
				RuntimeReminders:  agentRuntimeReminders(p, name),
				ServiceAuth:       svcAuth,
				ChannelAllowlist:  conversationWallAllowlists[name],
				Metadata: injectAgentBudget(cllama.InjectCompiledModelPolicy(map[string]any{
					"service":  name,
					"pod":      p.Name,
					"type":     rc.ClawType,
					"token":    tokens[name],
					"timezone": rc.Timezone,
				}, rc.Models), agentBudgetPolicy(p, name)),
			})
		}
		if err := cllama.GenerateContextDir(runtimeDir, contextInputs); err != nil {
			return fmt.Errorf("generate cllama context dir: %w", err)
		}

		// .claw-auth and .claw-session-history are persistent siblings of
		// .claw-runtime — claw up never wipes them.
		authDir, err := ensurePersistentCllamaDir(podDir, ".claw-auth")
		if err != nil {
			return err
		}
		sessionHistoryDir, err := ensurePersistentCllamaDir(podDir, ".claw-session-history")
		if err != nil {
			return err
		}

		// Compile seed keys from all service CllamaEnv into providers.json.
		if err := mergeProviderSeeds(authDir, p, resolvedClaws); err != nil {
			return fmt.Errorf("write providers.json: %w", err)
		}

		// Load or generate CLLAMA_UI_TOKEN (persists across re-ups).
		uiToken, err := loadOrGenerateUIToken(authDir)
		if err != nil {
			return fmt.Errorf("cllama UI token: %w", err)
		}

		// Finalize proxyEnv: strip provider keys; add alert vars and UI token.
		// Provider keys now live in providers.json, not in the container env.
		finalProxyEnv := make(map[string]string, len(proxyEnv)+4)
		for k, v := range proxyEnv {
			if !isProviderKey(k) {
				finalProxyEnv[k] = v
			}
		}
		finalProxyEnv["CLLAMA_UI_TOKEN"] = uiToken
		if len(p.AlertWebhooks) > 0 {
			finalProxyEnv["CLLAMA_ALERT_WEBHOOKS"] = strings.Join(p.AlertWebhooks, ",")
		}
		if len(p.AlertMentions) > 0 {
			finalProxyEnv["CLLAMA_ALERT_MENTIONS"] = strings.Join(p.AlertMentions, ",")
		}

		for _, proxyType := range proxyTypes {
			proxies = append(proxies, pod.CllamaProxyConfig{
				ProxyType:             proxyType,
				Image:                 resolveProxyImageRef(proxyType),
				ContextHostDir:        filepath.Join(runtimeDir, "context"),
				AuthHostDir:           authDir,
				SessionHistoryHostDir: sessionHistoryDir,
				GovernanceHostDir:     governanceDir,
				DashboardPort:         cllamaDashboardPort,
				Environment:           finalProxyEnv,
				PodName:               p.Name,
			})
		}
		if p.ClawAPI != nil {
			p.ClawAPI.ContextHostDir = filepath.Join(runtimeDir, "context")
			p.ClawAPI.CllamaAPIURL = fmt.Sprintf("http://%s:8081", cllama.ProxyServiceName(proxyTypes[0]))
			p.ClawAPI.CllamaAPIToken = uiToken
		}
		fmt.Printf("[claw] cllama proxies enabled: %s (agents: %s)\n",
			strings.Join(proxyTypes, ", "), strings.Join(cllamaAgents, ", "))
	}

	if p.ClawAPI != nil && strings.TrimSpace(p.ClawAPI.ScheduleHostPath) != "" {
		schedulePath, err := writeScheduleManifest(runtimeDir, p, resolvedClaws)
		if err != nil {
			return err
		}
		p.ClawAPI.ScheduleHostPath = schedulePath
		fmt.Printf("[claw] wrote %s\n", schedulePath)
	}

	manifestPath, err := writePodManifest(runtimeDir, p, resolvedClaws, proxies)
	if err != nil {
		return err
	}
	fmt.Printf("[claw] wrote %s\n", manifestPath)

	p.Clawdash = &pod.ClawdashConfig{
		Image:              preferredInfraImageRef(infraComponentClawdash),
		Addr:               envOrDefault("CLAWDASH_ADDR", ":8082"),
		ManifestHostPath:   manifestPath,
		DockerSockHostPath: "/var/run/docker.sock",
		CllamaCostsURL:     firstIf(cllamaEnabled, fmt.Sprintf("http://localhost:%s", cllamaDashboardPort)),
		PodName:            p.Name,
	}
	if p.ClawAPI != nil {
		dashboardToken, err := lookupClawAPIPrincipalToken(p.ClawAPI.PrincipalsHostPath, "claw-dashboard")
		if err != nil {
			return err
		}
		p.Clawdash.Environment = map[string]string{
			"CLAW_API_URL":   fmt.Sprintf("http://claw-api:%s", clawAPIInternalPort(p.ClawAPI.Addr)),
			"CLAW_API_TOKEN": dashboardToken,
		}
	}

	// Pass 2: materialize after cllama tokens/context are resolved.
	for _, name := range sortedResolvedClawNames(resolvedClaws) {
		rc := resolvedClaws[name]
		d := drivers[name]
		svcRuntimeDir := serviceRuntimeDirs[name]

		result, err := d.Materialize(rc, driver.MaterializeOpts{
			RuntimeDir: svcRuntimeDir,
			StateDir:   filepath.Join(memoryRoot, name),
			PodName:    p.Name,
		})
		if err != nil {
			return fmt.Errorf("service %q: materialization failed: %w", name, err)
		}
		if err := appendPersistentSkillMount(result, skillRoot, rc); err != nil {
			return fmt.Errorf("service %q: prepare persistent skill dir: %w", name, err)
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

	requiredInfra := requiredInfraImageSpecs(p, cllamaEnabled, proxies, p.ClawAPI, p.Clawdash)
	if !composeUpFix {
		if err := ensureRequiredInfraImagesAvailable(requiredInfra); err != nil {
			return err
		}
	} else {
		if err := ensureInfraImages(p, cllamaEnabled, proxies, p.ClawAPI, p.Clawdash); err != nil {
			return err
		}
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

	composeApplied = true
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

	if err := runtimeStage.Commit(); err != nil {
		fmt.Printf("[claw] warning: could not remove previous runtime dir %s: %v\n", runtimeStage.PreviousPath, err)
	}

	fmt.Println("[claw] pod is up")
	if p.Clawdash != nil {
		fmt.Printf("[claw] dashboard:  http://localhost:%s\n", pod.ClawdashHostPort(p.Clawdash.Addr))
	}
	return nil
}

func hasPodInvokeEntries(p *pod.Pod) bool {
	if p == nil {
		return false
	}
	for _, svc := range p.Services {
		if svc != nil && svc.Claw != nil && len(svc.Claw.Invoke) > 0 {
			return true
		}
	}
	return false
}

// validateClawAPIDeclarations ensures that claw-api: self and x-claw.principals
// are only declared when a master claw is present. Scheduler-only claw-api
// injection is allowed without a master, but those declarations still require a
// token injection path and explicit governance authority.
func validateClawAPIDeclarations(p *pod.Pod) error {
	if p.Master != "" {
		return nil
	}
	if len(p.Principals) > 0 {
		return fmt.Errorf("x-claw.principals requires x-claw.master to be set")
	}
	for name, svc := range p.Services {
		if svc.Claw != nil && svc.Claw.ClawAPIMode == "self" {
			return fmt.Errorf("service %q: claw-api: self requires x-claw.master to be set", name)
		}
	}
	return nil
}

// validateManagedCapabilityDeclarations ensures x-claw.tools and x-claw.memory
// are only accepted on services that actually run behind cllama today. Without
// cllama, these declarations compile into no runtime artifacts, which makes
// them silent no-ops rather than an intentional native projection path.
func validateManagedCapabilityDeclarations(p *pod.Pod, resolvedClaws map[string]*driver.ResolvedClaw) error {
	if p == nil {
		return nil
	}
	for name, svc := range p.Services {
		if svc == nil || svc.Claw == nil {
			continue
		}

		capabilities := unsupportedManagedCapabilityList(svc.Claw)
		if len(capabilities) == 0 {
			continue
		}

		if rc := resolvedClaws[name]; rc != nil && len(rc.Cllama) > 0 {
			continue
		}

		return fmt.Errorf(
			"service %q: %s require x-claw.cllama on the consuming service; native projection for non-cllama services is not implemented",
			name,
			strings.Join(capabilities, " and "),
		)
	}
	return nil
}

func unsupportedManagedCapabilityList(claw *pod.ClawBlock) []string {
	if claw == nil {
		return nil
	}

	capabilities := make([]string, 0, 2)
	if len(claw.Tools) > 0 {
		capabilities = append(capabilities, "x-claw.tools")
	}
	if claw.Memory != nil {
		capabilities = append(capabilities, "x-claw.memory")
	}
	return capabilities
}

type runtimeDirStage struct {
	ActivePath   string
	PreviousPath string
}

func prepareRuntimeDir(path string) (*runtimeDirStage, error) {
	stage := &runtimeDirStage{ActivePath: path}

	info, err := os.Stat(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		// No prior runtime tree — create a fresh one below.
	case err != nil:
		return nil, err
	default:
		if !info.IsDir() {
			return nil, fmt.Errorf("runtime dir %q exists but is not a directory", path)
		}
		stage.PreviousPath = fmt.Sprintf("%s.previous-%d", path, time.Now().UnixNano())
		if err := os.Rename(path, stage.PreviousPath); err != nil {
			return nil, err
		}
	}

	if err := os.MkdirAll(path, 0o700); err != nil {
		if stage.PreviousPath != "" {
			_ = os.Rename(stage.PreviousPath, path)
		}
		return nil, err
	}
	if err := os.Chmod(path, 0o700); err != nil {
		if stage.PreviousPath != "" {
			_ = os.RemoveAll(path)
			_ = os.Rename(stage.PreviousPath, path)
		}
		return nil, err
	}
	return stage, nil
}

func (s *runtimeDirStage) Rollback() error {
	if s == nil || s.PreviousPath == "" {
		return nil
	}
	if err := os.RemoveAll(s.ActivePath); err != nil {
		return err
	}
	return os.Rename(s.PreviousPath, s.ActivePath)
}

func (s *runtimeDirStage) Commit() error {
	if s == nil || s.PreviousPath == "" {
		return nil
	}
	if err := removePreviousRuntimeDir(s.PreviousPath); err != nil {
		if os.IsPermission(err) {
			if helperErr := removeRuntimeDirWithDocker(s.PreviousPath); helperErr != nil {
				return fmt.Errorf("%w (docker helper cleanup failed: %v)", err, helperErr)
			}
			return nil
		}
		return err
	}
	return nil
}

func preMigratePortableMemory(runtimeDir, memoryRoot string, p *pod.Pod) error {
	if p == nil {
		return nil
	}
	for name := range p.Services {
		legacyRoot := filepath.Join(runtimeDir, name)
		stateDir := filepath.Join(memoryRoot, name)
		memoryDir := filepath.Join(stateDir, "memory")
		needsRepair, reason, err := portableMemoryNeedsRepair(memoryDir)
		if err != nil {
			return fmt.Errorf("service %q: inspect portable memory: %w", name, err)
		}
		if needsRepair {
			fmt.Printf("[claw] repairing portable memory for service %q (%s)\n", name, reason)
			if err := repairPortableMemoryWithDocker(memoryDir); err != nil {
				return fmt.Errorf("service %q: repair portable memory: %w", name, err)
			}
		}
		if _, err := shared.PreparePortableMemory(stateDir, legacyRoot); err != nil {
			return fmt.Errorf("service %q: %w", name, err)
		}
	}
	return nil
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
		if svc.Claw.MCPStdio != nil {
			svc.Claw.MCPStdio.Command = expand(svc.Claw.MCPStdio.Command)
			for i, value := range svc.Claw.MCPStdio.Args {
				svc.Claw.MCPStdio.Args[i] = expand(value)
			}
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
			if svc.Claw.Invoke[i].When != nil {
				svc.Claw.Invoke[i].When.Calendar = expand(svc.Claw.Invoke[i].When.Calendar)
				parsed, err := schedule.ParseSession(expand(string(svc.Claw.Invoke[i].When.Session)))
				if err != nil {
					return err
				}
				svc.Claw.Invoke[i].When.Session = parsed
				if err := svc.Claw.Invoke[i].When.Validate(); err != nil {
					return err
				}
			}
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

type resolvedMemorySubscription struct {
	Service string
	Config  *pod.MemoryEntry
}

type resolvedPodCapabilities struct {
	Tools  map[string][]describe.ToolSpec
	Memory map[string]*resolvedMemorySubscription
}

func resolvePodCapabilities(podFile, podDir string, p *pod.Pod, imageRefs map[string]string, infos map[string]*inspect.ClawInfo, descriptors map[string]*describe.ServiceDescriptor) (*resolvedPodCapabilities, error) {
	refreshed := make(map[string]struct{})
	for {
		capabilities, err := resolvePodCapabilitiesOnce(podDir, p, imageRefs, infos, descriptors)
		if err == nil {
			return capabilities, nil
		}

		serviceName, canRefresh := runtimeDescriptorRefreshCandidate(err, p, descriptors, refreshed)
		if !canRefresh {
			return nil, err
		}
		if !composeUpFix {
			return nil, remediationErrorf(upFixCommand(podFile), "%s", err)
		}
		if _, already := refreshed[serviceName]; already {
			return nil, err
		}
		if err := refreshRuntimeDescriptor(podFile, podDir, p, serviceName, imageRefs, infos, descriptors); err != nil {
			return nil, err
		}
		refreshed[serviceName] = struct{}{}
	}
}

func resolvePodCapabilitiesOnce(podDir string, p *pod.Pod, imageRefs map[string]string, infos map[string]*inspect.ClawInfo, descriptors map[string]*describe.ServiceDescriptor) (*resolvedPodCapabilities, error) {
	if err := collectServiceDescriptors(podDir, p, imageRefs, infos, descriptors); err != nil {
		return nil, err
	}
	feedRegistry, err := describe.BuildFeedRegistry(descriptors)
	if err != nil {
		return nil, fmt.Errorf("build feed registry: %w", err)
	}
	toolRegistry, err := describe.BuildToolRegistry(descriptors)
	if err != nil {
		return nil, fmt.Errorf("build tool registry: %w", err)
	}
	if err := resolveFeedSubscriptions(p, feedRegistry); err != nil {
		return nil, err
	}
	resolvedTools, err := resolveToolSubscriptions(p, toolRegistry)
	if err != nil {
		return nil, err
	}
	resolvedMemory, err := resolveMemorySubscriptions(p, descriptors)
	if err != nil {
		return nil, err
	}
	return &resolvedPodCapabilities{
		Tools:  resolvedTools,
		Memory: resolvedMemory,
	}, nil
}

type toolResolutionError struct {
	ConsumerService string
	PolicyIndex     int
	ToolService     string
	ToolName        string
}

func (e *toolResolutionError) Error() string {
	if e == nil {
		return ""
	}
	if e.ToolName == "" {
		return fmt.Sprintf("service %q: tool policy %d references unknown tool service %q", e.ConsumerService, e.PolicyIndex, e.ToolService)
	}
	return fmt.Sprintf("service %q: tool policy for %q references unknown tool %q", e.ConsumerService, e.ToolService, e.ToolName)
}

type memoryResolutionError struct {
	ConsumerService   string
	MemoryService     string
	MissingDescriptor bool
}

func (e *memoryResolutionError) Error() string {
	if e == nil {
		return ""
	}
	if e.MissingDescriptor {
		return fmt.Sprintf("service %q: memory target %q has no descriptor", e.ConsumerService, e.MemoryService)
	}
	return fmt.Sprintf("service %q: memory target %q does not declare a memory capability", e.ConsumerService, e.MemoryService)
}

type feedResolutionError struct {
	ConsumerService string
	FeedName        string
}

func (e *feedResolutionError) Error() string {
	if e == nil {
		return ""
	}
	return fmt.Sprintf("service %q: feed %q was not found in the descriptor registry", e.ConsumerService, e.FeedName)
}

func runtimeDescriptorRefreshCandidate(err error, p *pod.Pod, descriptors map[string]*describe.ServiceDescriptor, refreshed map[string]struct{}) (string, bool) {
	if p == nil {
		return "", false
	}

	var toolErr *toolResolutionError
	if errors.As(err, &toolErr) && toolErr != nil {
		return buildBackedRefreshCandidate(toolErr.ToolService, p, refreshed)
	}

	var memoryErr *memoryResolutionError
	if errors.As(err, &memoryErr) && memoryErr != nil {
		return buildBackedRefreshCandidate(memoryErr.MemoryService, p, refreshed)
	}

	var feedErr *feedResolutionError
	if errors.As(err, &feedErr) && feedErr != nil {
		return feedRefreshCandidate(feedErr.FeedName, p, descriptors, refreshed)
	}

	return "", false
}

func buildBackedRefreshCandidate(serviceName string, p *pod.Pod, refreshed map[string]struct{}) (string, bool) {
	serviceName = strings.TrimSpace(serviceName)
	if serviceName == "" || p == nil {
		return "", false
	}
	if _, already := refreshed[serviceName]; already {
		return "", false
	}
	svc := p.Services[serviceName]
	if !serviceHasBuildConfig(svc) {
		return "", false
	}
	return serviceName, true
}

func feedRefreshCandidate(feedName string, p *pod.Pod, descriptors map[string]*describe.ServiceDescriptor, refreshed map[string]struct{}) (string, bool) {
	feedName = strings.TrimSpace(feedName)
	if feedName == "" || p == nil {
		return "", false
	}
	serviceNames := make([]string, 0, len(p.Services))
	for name := range p.Services {
		serviceNames = append(serviceNames, name)
	}
	sort.Strings(serviceNames)
	for _, serviceName := range serviceNames {
		if _, already := refreshed[serviceName]; already {
			continue
		}
		svc := p.Services[serviceName]
		if !serviceHasBuildConfig(svc) {
			continue
		}
		if descriptorDeclaresFeed(descriptors[serviceName], feedName) {
			continue
		}
		return serviceName, true
	}
	return "", false
}

func descriptorDeclaresFeed(descriptor *describe.ServiceDescriptor, feedName string) bool {
	if descriptor == nil {
		return false
	}
	for _, feed := range descriptor.Feeds {
		if feed.Name == feedName {
			return true
		}
	}
	return false
}

func serviceHasBuildConfig(svc *pod.Service) bool {
	if svc == nil {
		return false
	}
	cfg, err := parseServiceBuildConfig(svc.Compose["build"])
	return err == nil && cfg != nil
}

func upFixCommand(podFile string) string {
	podFile = strings.TrimSpace(podFile)
	if podFile == "" || podFile == "claw-pod.yml" {
		return "claw up --fix -d"
	}
	return "claw up --fix -d " + podFile
}

func resolveToolSubscriptions(p *pod.Pod, registry describe.ToolRegistry) (map[string][]describe.ToolSpec, error) {
	if p == nil {
		return nil, nil
	}

	resolved := make(map[string][]describe.ToolSpec)
	for serviceName, svc := range p.Services {
		if svc == nil || svc.Claw == nil || len(svc.Claw.Tools) == 0 {
			continue
		}

		selected := make([]describe.ToolSpec, 0)
		seen := make(map[string]struct{})
		for i, policy := range svc.Claw.Tools {
			specs, ok := registry[policy.Service]
			if !ok {
				return nil, &toolResolutionError{
					ConsumerService: serviceName,
					PolicyIndex:     i,
					ToolService:     policy.Service,
				}
			}
			byName := make(map[string]describe.ToolSpec, len(specs))
			for _, spec := range specs {
				byName[spec.Name] = spec
			}

			if len(policy.Allow) == 1 && policy.Allow[0] == "all" {
				for _, spec := range specs {
					key := spec.Service + "." + spec.Name
					if _, exists := seen[key]; exists {
						continue
					}
					seen[key] = struct{}{}
					selected = append(selected, spec)
				}
				continue
			}

			for _, toolName := range policy.Allow {
				spec, ok := byName[toolName]
				if !ok {
					return nil, &toolResolutionError{
						ConsumerService: serviceName,
						PolicyIndex:     i,
						ToolService:     policy.Service,
						ToolName:        toolName,
					}
				}
				key := spec.Service + "." + spec.Name
				if _, exists := seen[key]; exists {
					continue
				}
				seen[key] = struct{}{}
				selected = append(selected, spec)
			}
		}
		if len(selected) > 0 {
			resolved[serviceName] = selected
		}
	}
	return resolved, nil
}

func resolveMemorySubscriptions(p *pod.Pod, descriptors map[string]*describe.ServiceDescriptor) (map[string]*resolvedMemorySubscription, error) {
	if p == nil {
		return nil, nil
	}

	resolved := make(map[string]*resolvedMemorySubscription)
	for serviceName, svc := range p.Services {
		if svc == nil || svc.Claw == nil || svc.Claw.Memory == nil {
			continue
		}
		target := svc.Claw.Memory.Service
		descriptor := descriptors[target]
		if descriptor == nil {
			return nil, &memoryResolutionError{
				ConsumerService:   serviceName,
				MemoryService:     target,
				MissingDescriptor: true,
			}
		}
		if descriptor.Memory == nil {
			return nil, &memoryResolutionError{
				ConsumerService: serviceName,
				MemoryService:   target,
			}
		}
		resolved[serviceName] = &resolvedMemorySubscription{
			Service: target,
			Config:  svc.Claw.Memory,
		}
	}
	return resolved, nil
}

func buildFeedManifestEntries(p *pod.Pod, descriptors map[string]*describe.ServiceDescriptor, runtimeEnv map[string]string, serviceName string, clawID string, serviceAuth []cllama.ServiceAuthEntry) ([]cllama.FeedManifestEntry, error) {
	svc := p.Services[serviceName]
	if svc == nil || svc.Claw == nil || len(svc.Claw.Feeds) == 0 {
		return nil, nil
	}
	if strings.TrimSpace(clawID) == "" {
		clawID = serviceName
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
		feedPath := strings.ReplaceAll(feed.Path, "{claw_id}", clawID)
		feedName := strings.ReplaceAll(feed.Name, "{claw_id}", clawID)
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

// agentToolPolicy resolves a service's x-claw.tool-policy overrides into the
// effective cllama tool policy. Returns nil when the service declares no
// overrides, so the context generator falls back to DefaultToolPolicy.
func agentToolPolicy(p *pod.Pod, serviceName string) *cllama.ToolPolicy {
	svc := p.Services[serviceName]
	if svc == nil || svc.Claw == nil || svc.Claw.ToolPolicy == nil {
		return nil
	}
	tp := svc.Claw.ToolPolicy
	policy := cllama.EffectiveToolPolicy(tp.MaxRounds, tp.TimeoutPerToolMS, tp.TotalTimeoutMS)
	return &policy
}

func agentBudgetPolicy(p *pod.Pod, serviceName string) *cllama.BudgetPolicy {
	svc := p.Services[serviceName]
	if svc == nil || svc.Claw == nil || svc.Claw.Budget == nil {
		return nil
	}
	budget := svc.Claw.Budget
	return &cllama.BudgetPolicy{
		LimitUSD:    budget.LimitUSD,
		MaxRequests: budget.MaxRequests,
		Window:      budget.Window,
		Behavior:    budget.Behavior,
	}
}

func agentRuntimeReminders(p *pod.Pod, serviceName string) []cllama.RuntimeReminderManifestEntry {
	if p == nil {
		return nil
	}
	var reminders []pod.RuntimeReminderConfig
	if p.Context != nil && p.Context.RuntimeReminders != nil {
		reminders = p.Context.RuntimeReminders
	}
	if svc := p.Services[serviceName]; svc != nil && svc.Claw != nil && svc.Claw.Context != nil && svc.Claw.Context.RuntimeReminders != nil {
		reminders = svc.Claw.Context.RuntimeReminders
	}
	if len(reminders) == 0 {
		return nil
	}
	out := make([]cllama.RuntimeReminderManifestEntry, 0, len(reminders))
	for _, reminder := range reminders {
		out = append(out, cllama.RuntimeReminderManifestEntry{
			ID:        reminder.ID,
			Text:      reminder.Text,
			Enabled:   reminder.Enabled,
			Placement: reminder.Placement,
			MaxChars:  reminder.MaxChars,
			Cadence:   reminder.Cadence,
		})
	}
	return out
}

func injectAgentBudget(meta map[string]any, budget *cllama.BudgetPolicy) map[string]any {
	if budget != nil {
		meta["budget"] = budget
	}
	return meta
}

func buildToolManifestEntries(p *pod.Pod, descriptors map[string]*describe.ServiceDescriptor, runtimeEnv map[string]string, serviceName string, tools []describe.ToolSpec, serviceAuth []cllama.ServiceAuthEntry) ([]cllama.ToolManifestEntry, error) {
	svc := p.Services[serviceName]
	if svc == nil || svc.Claw == nil || len(tools) == 0 {
		return nil, nil
	}

	entries := make([]cllama.ToolManifestEntry, 0, len(tools))
	for _, tool := range tools {
		baseURL, err := resolveServiceBaseURL(p, tool.Service)
		if err != nil {
			return nil, fmt.Errorf("tool %q: %w", tool.Name, err)
		}
		auth, err := resolveManifestAuth(svc.Environment, descriptors[tool.Service], runtimeEnv, serviceAuth, tool.Service)
		if err != nil {
			return nil, fmt.Errorf("tool %q: %w", tool.Name, err)
		}
		var execution cllama.ToolExecution
		switch {
		case tool.HTTP != nil:
			execution = cllama.ToolExecution{
				Transport: "http",
				Service:   tool.Service,
				BaseURL:   baseURL,
				Method:    tool.HTTP.Method,
				Path:      tool.HTTP.Path,
				BodyKey:   tool.HTTP.BodyKey,
				Auth:      auth,
			}
		case tool.MCP != nil:
			execution = cllama.ToolExecution{
				Transport: "mcp",
				Service:   tool.Service,
				BaseURL:   baseURL,
				Path:      tool.MCP.Path,
				ToolName:  tool.Name,
				Auth:      auth,
			}
		default:
			return nil, fmt.Errorf("tool %q from %q has no HTTP or MCP execution metadata", tool.Name, tool.Service)
		}
		entries = append(entries, cllama.ToolManifestEntry{
			Name:        tool.Service + "." + tool.Name,
			Description: tool.Description,
			InputSchema: tool.InputSchema,
			Annotations: tool.Annotations,
			Execution:   execution,
		})
	}
	return entries, nil
}

func buildMemoryManifestEntry(p *pod.Pod, descriptors map[string]*describe.ServiceDescriptor, runtimeEnv map[string]string, serviceName string, memory *resolvedMemorySubscription, serviceAuth []cllama.ServiceAuthEntry) (*cllama.MemoryManifestEntry, error) {
	svc := p.Services[serviceName]
	if svc == nil || svc.Claw == nil || memory == nil {
		return nil, nil
	}

	descriptor := descriptors[memory.Service]
	if descriptor == nil || descriptor.Memory == nil {
		return nil, fmt.Errorf("memory target %q has no descriptor memory capability", memory.Service)
	}

	baseURL, err := resolveServiceBaseURL(p, memory.Service)
	if err != nil {
		return nil, err
	}
	auth, err := resolveManifestAuth(svc.Environment, descriptor, runtimeEnv, serviceAuth, memory.Service)
	if err != nil {
		return nil, err
	}

	entry := &cllama.MemoryManifestEntry{
		Version: 1,
		Service: memory.Service,
		BaseURL: baseURL,
		Auth:    auth,
	}
	if descriptor.Memory.Recall != nil {
		entry.Recall = &cllama.MemoryOp{
			Path:      descriptor.Memory.Recall.Path,
			TimeoutMS: memory.Config.TimeoutMS,
		}
	}
	if descriptor.Memory.Retain != nil {
		entry.Retain = &cllama.MemoryOp{Path: descriptor.Memory.Retain.Path}
	}
	if descriptor.Memory.Forget != nil {
		entry.Forget = &cllama.MemoryOp{Path: descriptor.Memory.Forget.Path}
	}
	return entry, nil
}

func attachCapabilityProvidersToInternalNetwork(p *pod.Pod, resolvedTools map[string][]describe.ToolSpec, resolvedMemory map[string]*resolvedMemorySubscription) error {
	if p == nil {
		return nil
	}

	providers := make(map[string]struct{})
	for _, svc := range p.Services {
		if svc == nil || svc.Claw == nil {
			continue
		}
		for _, feed := range svc.Claw.Feeds {
			if feed.Unresolved || strings.TrimSpace(feed.Source) == "" {
				continue
			}
			providers[feed.Source] = struct{}{}
		}
	}
	for _, tools := range resolvedTools {
		for _, tool := range tools {
			if strings.TrimSpace(tool.Service) == "" {
				continue
			}
			providers[tool.Service] = struct{}{}
		}
	}
	for _, memory := range resolvedMemory {
		if memory == nil || strings.TrimSpace(memory.Service) == "" {
			continue
		}
		providers[memory.Service] = struct{}{}
	}

	for provider := range providers {
		svc := p.Services[provider]
		if svc == nil {
			continue
		}
		if err := ensureServiceOnNetwork(svc, clawInternalNetworkName); err != nil {
			return fmt.Errorf("attach capability provider network: %w", err)
		}
	}
	return nil
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

func resolveManifestAuth(env map[string]string, descriptor *describe.ServiceDescriptor, runtimeEnv map[string]string, projected []cllama.ServiceAuthEntry, targetService string) (*cllama.AuthEntry, error) {
	for _, entry := range projected {
		if entry.Service == targetService && entry.AuthType == "bearer" && entry.Token != "" {
			return &cllama.AuthEntry{
				Type:  "bearer",
				Token: entry.Token,
			}, nil
		}
	}

	token, err := resolveFeedAuthFromServiceEnv(env, descriptor, runtimeEnv)
	if err != nil {
		return nil, err
	}
	if token == "" {
		return nil, nil
	}
	return &cllama.AuthEntry{
		Type:  "bearer",
		Token: token,
	}, nil
}

func resolveServiceBaseURL(p *pod.Pod, source string) (string, error) {
	if p.ClawAPI != nil && source == "claw-api" {
		return buildBaseURL(source, clawAPIInternalPort(p.ClawAPI.Addr)), nil
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
	return buildBaseURL(source, port), nil
}

func resolveFeedURL(p *pod.Pod, source string, feedPath string) (string, error) {
	baseURL, err := resolveServiceBaseURL(p, source)
	if err != nil {
		return "", err
	}
	return buildFeedURL(baseURL, feedPath), nil
}

func buildBaseURL(source, port string) string {
	return fmt.Sprintf("http://%s:%s", source, port)
}

func buildFeedURL(baseURL, feedPath string) string {
	return baseURL + feedPath
}

func augmentClawdapusMD(base string, tools []cllama.ToolManifestEntry, memory *cllama.MemoryManifestEntry) string {
	if len(tools) == 0 && memory == nil {
		return base
	}

	var b strings.Builder
	b.WriteString(base)
	if len(tools) > 0 {
		b.WriteString("## Tools\n\n")
		b.WriteString("Managed service tools are compiled into your proxy context.\n\n")
		for _, tool := range tools {
			presentedName := cllama.PresentedToolName(tool.Name)
			line := fmt.Sprintf("- `%s`", presentedName)
			if presentedName != tool.Name {
				line += fmt.Sprintf(" (canonical: `%s`)", tool.Name)
			}
			if tool.Description != "" {
				line += fmt.Sprintf(" — %s", tool.Description)
			}
			b.WriteString(line + "\n")
		}
		b.WriteString("\n")
	}
	if memory != nil {
		b.WriteString("## Ambient Memory\n\n")
		b.WriteString(fmt.Sprintf("- **Service:** `%s`\n", memory.Service))
		if memory.Recall != nil {
			b.WriteString("- **Recall:** active before each inference turn\n")
		}
		if memory.Retain != nil {
			b.WriteString("- **Retention:** post-turn retention hook compiled\n")
		}
		b.WriteString("\n")
	}
	return b.String()
}

type conversationWallTokenPair struct {
	ChannelID string
	Token     string
}

type conversationWallTokenCandidate struct {
	ServiceName string
	Token       string
}

func injectConversationWall(p *pod.Pod, resolvedClaws map[string]*driver.ResolvedClaw) error {
	if p == nil {
		return nil
	}

	consumedChannelSet := make(map[string]struct{})
	triggerServices := make(map[string][]string)

	for _, name := range sortedResolvedClawNames(resolvedClaws) {
		rc := resolvedClaws[name]
		if rc == nil || len(rc.Cllama) == 0 {
			continue
		}
		svc := p.Services[name]
		if svc == nil || svc.Claw == nil {
			continue
		}

		channelIDs := discordHandleChannelIDs(svc.Claw.Handles)
		if len(channelIDs) == 0 {
			continue
		}

		for _, channelID := range channelIDs {
			consumedChannelSet[channelID] = struct{}{}
		}
		triggerServices[name] = channelIDs
	}

	if len(triggerServices) == 0 {
		return nil
	}
	if _, exists := p.Services[conversationWallServiceName]; exists {
		return fmt.Errorf("service name %q is reserved for the conversation wall sidecar", conversationWallServiceName)
	}

	consumedChannelIDs := sortedConversationWallChannelIDs(consumedChannelSet)
	candidatesByChannel := make(map[string][]conversationWallTokenCandidate, len(consumedChannelIDs))
	for _, name := range sortedConversationWallServiceNames(p.Services) {
		svc := p.Services[name]
		if svc == nil || svc.Claw == nil {
			continue
		}
		token := strings.TrimSpace(svc.Environment["DISCORD_BOT_TOKEN"])
		if token == "" {
			continue
		}
		for _, channelID := range discordHandleChannelIDs(svc.Claw.Handles) {
			if _, consumed := consumedChannelSet[channelID]; !consumed {
				continue
			}
			candidatesByChannel[channelID] = append(candidatesByChannel[channelID], conversationWallTokenCandidate{
				ServiceName: name,
				Token:       token,
			})
		}
	}

	tokenPairs := make([]conversationWallTokenPair, 0, len(consumedChannelIDs))
	for _, channelID := range consumedChannelIDs {
		token, err := selectConversationWallToken(channelID, p.Master, candidatesByChannel[channelID])
		if err != nil {
			return err
		}
		tokenPairs = append(tokenPairs, conversationWallTokenPair{
			ChannelID: channelID,
			Token:     token,
		})
	}

	for name, channelIDs := range triggerServices {
		svc := p.Services[name]
		if svc == nil || svc.Claw == nil {
			continue
		}
		settings := conversationWallChannelContext(p, svc)
		awarenessSettings := conversationWallChannelAwarenessContext(p, svc)
		svc.Claw.Feeds = appendConversationWallAwarenessFeed(svc.Claw.Feeds, channelIDs, awarenessSettings, p.ChannelMemory != nil)
		svc.Claw.Feeds = appendConversationWallFeed(svc.Claw.Feeds, channelIDs, settings)
		svc.Claw.Tools = appendConversationWallToolPolicy(svc.Claw.Tools)
	}

	wallEnv := map[string]string{
		"CLAW_WALL_TOKENS":             formatConversationWallTokenPairs(tokenPairs),
		"CLAW_WALL_LIMIT":              strconv.Itoa(conversationWallBufferForPod(p, triggerServices)),
		"CLAW_WALL_POLL_INTERVAL":      envOrDefault("CLAW_WALL_POLL_INTERVAL", conversationWallPollInterval),
		"CLAW_WALL_RETENTION":          envOrDefault("CLAW_WALL_RETENTION", conversationWallRetention),
		"CLAW_WALL_BACKFILL_MAX_PAGES": envOrDefault("CLAW_WALL_BACKFILL_MAX_PAGES", conversationWallBackfillPages),
	}
	if discordBaseURL := strings.TrimSpace(os.Getenv(conversationWallDiscordBaseEnv)); discordBaseURL != "" {
		wallEnv[conversationWallDiscordBaseEnv] = discordBaseURL
	}
	if err := configureConversationWallChannelMemory(p, wallEnv); err != nil {
		return err
	}

	p.Services[conversationWallServiceName] = &pod.Service{
		Image:       resolveConversationWallImageRef(),
		Environment: wallEnv,
		Expose:      []string{conversationWallInternalPort},
		Compose: map[string]interface{}{
			"networks":  []string{"claw-internal"},
			"restart":   "on-failure",
			"read_only": true,
			"tmpfs":     []string{"/tmp"},
			"healthcheck": map[string]interface{}{
				"test":     []string{"CMD", "/claw-wall", "-healthcheck"},
				"interval": "15s",
				"timeout":  "5s",
				"retries":  3,
			},
			"labels": map[string]string{
				"claw.role": "conversation-wall",
			},
		},
	}
	return nil
}

func configureConversationWallChannelMemory(p *pod.Pod, wallEnv map[string]string) error {
	if p == nil || p.ChannelMemory == nil {
		return nil
	}
	serviceName := strings.TrimSpace(p.ChannelMemory.Service)
	if serviceName == "" {
		return nil
	}
	if _, ok := p.Services[serviceName]; !ok {
		return fmt.Errorf("channel-memory service %q not found", serviceName)
	}
	baseURL, err := resolveServiceBaseURL(p, serviceName)
	if err != nil {
		return fmt.Errorf("channel-memory service %q: %w", serviceName, err)
	}
	wallEnv[conversationWallMemoryIngestEnv] = buildFeedURL(baseURL, "/ingest")
	wallEnv[conversationWallMemoryDigestEnv] = buildFeedURL(baseURL, "/digest")
	if searchURL := strings.TrimSpace(os.Getenv(conversationWallMemorySearchEnv)); searchURL != "" {
		wallEnv[conversationWallMemorySearchEnv] = searchURL
	}
	if sourceURL := strings.TrimSpace(os.Getenv(conversationWallMemorySourceEnv)); sourceURL != "" {
		wallEnv[conversationWallMemorySourceEnv] = sourceURL
	}
	wallEnv["CLAW_WALL_CHANNEL_MEMORY_TIMEOUT"] = envOrDefault("CLAW_WALL_CHANNEL_MEMORY_TIMEOUT", conversationWallMemoryTimeout)
	targetSvc := p.Services[serviceName]
	if targetSvc.Environment == nil {
		targetSvc.Environment = make(map[string]string)
	}
	token := strings.TrimSpace(targetSvc.Environment["CHANNEL_MEMORY_TOKEN"])
	if token == "" {
		token = cllama.GenerateToken(serviceName)
		targetSvc.Environment["CHANNEL_MEMORY_TOKEN"] = token
	}
	wallEnv["CLAW_WALL_CHANNEL_MEMORY_TOKEN"] = token
	if err := ensureServiceOnNetwork(targetSvc, clawInternalNetworkName); err != nil {
		return fmt.Errorf("attach channel-memory service network: %w", err)
	}
	return nil
}

func selectConversationWallToken(channelID, master string, candidates []conversationWallTokenCandidate) (string, error) {
	if len(candidates) == 0 {
		return "", fmt.Errorf("conversation wall injection triggered but channel %q has no eligible Discord reader token", channelID)
	}
	for _, candidate := range candidates {
		if candidate.ServiceName == master {
			return candidate.Token, nil
		}
	}
	return candidates[0].Token, nil
}

func sortedConversationWallChannelIDs(channels map[string]struct{}) []string {
	channelIDs := make([]string, 0, len(channels))
	for channelID := range channels {
		channelIDs = append(channelIDs, channelID)
	}
	sort.Strings(channelIDs)
	return channelIDs
}

func sortedConversationWallServiceNames(services map[string]*pod.Service) []string {
	names := make([]string, 0, len(services))
	for name := range services {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

type conversationWallContextSettings struct {
	Since    string
	Limit    int
	MaxChars int
	Buffer   int
}

func conversationWallChannelContext(p *pod.Pod, svc *pod.Service) conversationWallContextSettings {
	settings := conversationWallContextSettings{
		Since:    conversationWallFeedSince,
		Limit:    conversationWallFeedLimit,
		MaxChars: conversationWallFeedMaxChars,
		Buffer:   conversationWallBufferLimit,
	}
	if p != nil && p.Context != nil {
		settings = applyConversationWallChannelContext(settings, p.Context.Channel)
	}
	if svc != nil && svc.Claw != nil && svc.Claw.Context != nil {
		settings = applyConversationWallChannelContext(settings, svc.Claw.Context.Channel)
	}
	return settings
}

func conversationWallChannelAwarenessContext(p *pod.Pod, svc *pod.Service) conversationWallContextSettings {
	settings := conversationWallContextSettings{
		Since:    conversationWallFeedSince,
		Limit:    conversationWallAwarenessLimit,
		MaxChars: conversationWallFeedMaxChars,
	}
	if p != nil && p.Context != nil {
		settings = applyConversationWallChannelContext(settings, p.Context.Channel)
	}
	if svc != nil && svc.Claw != nil && svc.Claw.Context != nil {
		settings = applyConversationWallChannelContext(settings, svc.Claw.Context.Channel)
	}
	return settings
}

func applyConversationWallChannelContext(settings conversationWallContextSettings, cfg *pod.ChannelContextConfig) conversationWallContextSettings {
	if cfg == nil {
		return settings
	}
	if strings.TrimSpace(cfg.Since) != "" {
		settings.Since = strings.TrimSpace(cfg.Since)
	}
	if cfg.Limit > 0 {
		settings.Limit = cfg.Limit
	}
	if cfg.MaxChars > 0 {
		settings.MaxChars = cfg.MaxChars
	}
	if cfg.Buffer > 0 {
		settings.Buffer = cfg.Buffer
	}
	return settings
}

func conversationWallBufferForPod(p *pod.Pod, triggerServices map[string][]string) int {
	buffer := conversationWallBufferLimit
	for name := range triggerServices {
		svc := p.Services[name]
		if svc == nil {
			continue
		}
		if candidate := conversationWallChannelContext(p, svc).Buffer; candidate > buffer {
			buffer = candidate
		}
	}
	return buffer
}

func appendConversationWallFeed(feeds []pod.FeedEntry, channelIDs []string, settings conversationWallContextSettings) []pod.FeedEntry {
	path := fmt.Sprintf(
		"/channel-context?consumer={claw_id}&channels=%s&mode=tail&since=%s&limit=%d&max_chars=%d",
		strings.Join(channelIDs, ","),
		settings.Since,
		settings.Limit,
		settings.MaxChars,
	)
	for _, feed := range feeds {
		if feed.Name == conversationWallFeedName && feed.Source == conversationWallServiceName && feed.Path == path {
			return feeds
		}
	}
	return append(feeds, pod.FeedEntry{
		Name:   conversationWallFeedName,
		Source: conversationWallServiceName,
		Path:   path,
		TTL:    conversationWallFeedTTL,
	})
}

func appendConversationWallAwarenessFeed(feeds []pod.FeedEntry, channelIDs []string, settings conversationWallContextSettings, channelMemory bool) []pod.FeedEntry {
	contextKind := "raw_window"
	if channelMemory {
		contextKind = "raw_window+digest"
	}
	escapedContextKind := strings.ReplaceAll(contextKind, "+", "%2B")
	path := fmt.Sprintf(
		"/channel-awareness?channels=%s&since=%s&limit=%d&max_chars=%d&context_kind=%s",
		strings.Join(channelIDs, ","),
		settings.Since,
		settings.Limit,
		settings.MaxChars,
		escapedContextKind,
	)
	for _, feed := range feeds {
		if feed.Name == conversationWallAwarenessName && feed.Source == conversationWallServiceName && feed.Path == path {
			return feeds
		}
	}
	return append(feeds, pod.FeedEntry{
		Name:   conversationWallAwarenessName,
		Source: conversationWallServiceName,
		Path:   path,
		TTL:    conversationWallFeedTTL,
	})
}

func appendConversationWallToolPolicy(tools []pod.ToolPolicyEntry) []pod.ToolPolicyEntry {
	required := []string{"search_channel_context", "get_channel_messages"}
	for _, policy := range tools {
		if policy.Service != conversationWallServiceName {
			continue
		}
		if len(policy.Allow) == 1 && policy.Allow[0] == "all" {
			return tools
		}
		seen := make(map[string]struct{}, len(policy.Allow))
		for _, item := range policy.Allow {
			seen[item] = struct{}{}
		}
		covered := true
		for _, name := range required {
			if _, ok := seen[name]; !ok {
				covered = false
				break
			}
		}
		if covered {
			return tools
		}
	}
	return append(tools, pod.ToolPolicyEntry{
		Service: conversationWallServiceName,
		Allow:   required,
	})
}

func discordHandleChannelIDs(handles map[string]*driver.HandleInfo) []string {
	handle := handles["discord"]
	if handle == nil {
		return nil
	}

	seen := make(map[string]struct{})
	channelIDs := make([]string, 0)
	for _, guild := range handle.Guilds {
		for _, channel := range guild.Channels {
			channelID := strings.TrimSpace(channel.ID)
			if channelID == "" {
				continue
			}
			if _, exists := seen[channelID]; exists {
				continue
			}
			seen[channelID] = struct{}{}
			channelIDs = append(channelIDs, channelID)
		}
	}
	sort.Strings(channelIDs)
	return channelIDs
}

func formatConversationWallTokenPairs(pairs []conversationWallTokenPair) string {
	if len(pairs) == 0 {
		return ""
	}

	seen := make(map[string]struct{})
	deduped := make([]conversationWallTokenPair, 0, len(pairs))
	for _, pair := range pairs {
		key := pair.ChannelID + "\x00" + pair.Token
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		deduped = append(deduped, pair)
	}

	sort.Slice(deduped, func(i, j int) bool {
		if deduped[i].ChannelID != deduped[j].ChannelID {
			return deduped[i].ChannelID < deduped[j].ChannelID
		}
		return deduped[i].Token < deduped[j].Token
	})

	entries := make([]string, 0, len(deduped))
	for _, pair := range deduped {
		entries = append(entries, pair.ChannelID+":"+pair.Token)
	}
	return strings.Join(entries, ",")
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
	if p == nil || p.ClawAPI == nil {
		return nil, nil
	}
	clawAPIURL := fmt.Sprintf("http://claw-api:%s", clawAPIInternalPort(p.ClawAPI.Addr))

	auto := make([]clawapi.Principal, 0)
	var (
		masterSvc *pod.Service
		masterRC  *driver.ResolvedClaw
	)

	if strings.TrimSpace(p.Master) != "" {
		masterRC = resolvedClaws[p.Master]
		if masterRC == nil {
			return nil, fmt.Errorf("master service %q did not resolve to a claw", p.Master)
		}
		masterSvc = p.Services[p.Master]
		if masterSvc == nil {
			return nil, fmt.Errorf("master service %q not found in pod services", p.Master)
		}

		masterPrincipal, err := clawapi.BuildMasterPrincipal(p.Name, p.Master)
		if err != nil {
			return nil, err
		}
		auto = append(auto, masterPrincipal)
	}

	if hasPodInvokeEntries(p) {
		schedulerPrincipal, err := clawapi.BuildSchedulerPrincipal(p.Name)
		if err != nil {
			return nil, err
		}
		auto = append(auto, schedulerPrincipal)
	}

	dashboardPrincipal, err := clawapi.BuildDashboardPrincipal(p.Name)
	if err != nil {
		return nil, err
	}
	auto = append(auto, dashboardPrincipal)

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

	// 3a. Resolve effective master token from merged result.
	// An explicit principal may have overridden the auto-generated master by name;
	// use the merged token, not the pre-merge one.
	effectiveMasterToken := ""
	if p.Master != "" {
		for _, m := range merged {
			if m.Principal.Name == p.Master {
				effectiveMasterToken = m.Principal.Token
				break
			}
		}
		if effectiveMasterToken == "" {
			return nil, fmt.Errorf("master principal %q not found in merged result", p.Master)
		}
	}

	// 3b. Reject explicit inject-into claims that target master or claw-api: self services.
	// Those injection points are reserved for their auto-generated principals.
	reservedInjectTargets := map[string]string{}
	if p.Master != "" {
		reservedInjectTargets[p.Master] = "master claw"
	}
	for name, svc := range p.Services {
		if svc.Claw != nil && svc.Claw.ClawAPIMode == "self" {
			reservedInjectTargets[name] = "claw-api: self service"
		}
	}
	for _, m := range merged {
		if m.InjectInto != "" {
			if reason, reserved := reservedInjectTargets[m.InjectInto]; reserved {
				return nil, fmt.Errorf("principal %q: inject-into %q conflicts with %s — that injection point is reserved", m.Principal.Name, m.InjectInto, reason)
			}
		}
	}

	// 4. Write principals.json.
	principals := make([]clawapi.Principal, len(merged))
	for i, m := range merged {
		principals[i] = m.Principal
	}
	store := clawapi.Store{Principals: principals}
	for _, warning := range clawapi.PrincipalVersionSkewWarnings(&store, p.ClawAPI.Image) {
		fmt.Printf("[claw] warning: %s\n", warning)
	}
	if err := writeClawAPIPrincipalStore(runtimeDir, p.ClawAPI.PrincipalsHostPath, store); err != nil {
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

	// Master always gets its (effective, post-merge) token.
	if p.Master != "" {
		injectClawAPIEnv(masterSvc, effectiveMasterToken)
	}

	// Self principals and explicit inject-into targets.
	for _, m := range merged {
		if m.Principal.Name == p.Master {
			continue // already handled above
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

	if p.Master == "" {
		return nil, nil
	}

	// 6. Build cllama service auth entries for the master (feed fetching).
	authEntry := cllama.ServiceAuthEntry{
		Service:   "claw-api",
		AuthType:  "bearer",
		Token:     effectiveMasterToken,
		Principal: p.Master,
	}
	serviceAuth := make(map[string]cllama.ServiceAuthEntry)
	count := 1
	if masterRC.Count > 0 {
		count = masterRC.Count
	}
	for _, agentID := range expandedServiceNames(p.Master, count) {
		serviceAuth[agentID] = authEntry
	}
	return serviceAuth, nil
}

type conversationWallAllowlistFile struct {
	Version int                 `json:"version"`
	Agents  map[string][]string `json:"agents"`
}

func prepareConversationWallRuntime(runtimeDir string, p *pod.Pod, resolvedClaws map[string]*driver.ResolvedClaw) (map[string]cllama.ServiceAuthEntry, map[string][]string, error) {
	if p == nil {
		return nil, nil, nil
	}
	wallSvc := p.Services[conversationWallServiceName]
	if wallSvc == nil {
		return nil, nil, nil
	}

	allowlists := conversationWallAgentAllowlists(p, resolvedClaws)
	if len(allowlists) == 0 {
		return nil, nil, nil
	}

	manifest := conversationWallAllowlistFile{
		Version: 1,
		Agents:  allowlists,
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, nil, fmt.Errorf("marshal claw-wall channel allowlist: %w", err)
	}
	data = append(data, '\n')
	hostPath := filepath.Join(runtimeDir, "claw-wall-agent-channels.json")
	if err := writeRuntimeFile(hostPath, data, 0644); err != nil {
		return nil, nil, fmt.Errorf("write claw-wall channel allowlist: %w", err)
	}

	token := cllama.GenerateToken(conversationWallServiceName)
	if wallSvc.Environment == nil {
		wallSvc.Environment = make(map[string]string)
	}
	wallSvc.Environment[conversationWallToolTokenEnv] = token
	sum := sha256.Sum256(data)
	wallSvc.Environment["CLAW_WALL_AGENT_CHANNELS_SHA"] = hex.EncodeToString(sum[:])
	if wallSvc.Compose == nil {
		wallSvc.Compose = make(map[string]interface{})
	}
	volumes, err := appendComposeString(wallSvc.Compose["volumes"], fmt.Sprintf("%s:%s:ro", hostPath, conversationWallAllowlistPath))
	if err != nil {
		return nil, nil, fmt.Errorf("claw-wall volumes: %w", err)
	}
	wallSvc.Compose["volumes"] = volumes

	serviceAuth := make(map[string]cllama.ServiceAuthEntry, len(allowlists))
	for agentID := range allowlists {
		serviceAuth[agentID] = cllama.ServiceAuthEntry{
			Service:   conversationWallServiceName,
			AuthType:  "bearer",
			Token:     token,
			Principal: agentID,
		}
	}
	return serviceAuth, allowlists, nil
}

func conversationWallAgentAllowlists(p *pod.Pod, resolvedClaws map[string]*driver.ResolvedClaw) map[string][]string {
	allowlists := make(map[string][]string)
	if p == nil {
		return allowlists
	}
	for _, name := range sortedResolvedClawNames(resolvedClaws) {
		rc := resolvedClaws[name]
		if rc == nil || len(rc.Cllama) == 0 {
			continue
		}
		svc := p.Services[name]
		if svc == nil || svc.Claw == nil {
			continue
		}
		channelIDs := discordHandleChannelIDs(svc.Claw.Handles)
		if len(channelIDs) == 0 {
			continue
		}
		count := 1
		if rc.Count > 0 {
			count = rc.Count
		}
		for _, agentID := range expandedServiceNames(name, count) {
			allowlists[agentID] = append([]string(nil), channelIDs...)
		}
	}
	return allowlists
}

func writeClawAPIPrincipalStore(runtimeDir, hostPath string, store clawapi.Store) error {
	principalsDir := filepath.Dir(hostPath)
	if err := os.MkdirAll(principalsDir, 0700); err != nil {
		return fmt.Errorf("create claw-api runtime dir under %q: %w", runtimeDir, err)
	}
	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal claw-api principals: %w", err)
	}
	if err := writeRuntimeFile(hostPath, append(data, '\n'), 0600); err != nil {
		return err
	}
	return nil
}

func lookupClawAPIPrincipalToken(storePath, principalName string) (string, error) {
	store, err := clawapi.LoadStore(storePath)
	if err != nil {
		return "", err
	}
	for _, principal := range store.Principals {
		if strings.TrimSpace(principal.Name) == strings.TrimSpace(principalName) {
			if strings.TrimSpace(principal.Token) == "" {
				return "", fmt.Errorf("principal %q has empty token", principalName)
			}
			return principal.Token, nil
		}
	}
	return "", fmt.Errorf("principal %q not found in %s", principalName, storePath)
}

func prepareHistoryReplayRuntime(p *pod.Pod, resolvedClaws map[string]*driver.ResolvedClaw, resolvedMemory map[string]*resolvedMemorySubscription) (map[string]cllama.ServiceAuthEntry, error) {
	if p == nil || len(resolvedMemory) == 0 {
		return nil, nil
	}

	serviceAgents := make(map[string][]string)
	for consumer, memory := range resolvedMemory {
		if memory == nil || strings.TrimSpace(memory.Service) == "" {
			continue
		}
		count := 1
		if rc := resolvedClaws[consumer]; rc != nil && rc.Count > 0 {
			count = rc.Count
		}
		serviceAgents[memory.Service] = append(serviceAgents[memory.Service], expandedServiceNames(consumer, count)...)
	}
	if len(serviceAgents) == 0 {
		return nil, nil
	}

	serviceAuth := make(map[string]cllama.ServiceAuthEntry)
	for serviceName, agentIDs := range serviceAgents {
		svc := p.Services[serviceName]
		if svc == nil {
			return nil, fmt.Errorf("history replay target %q not found in pod services", serviceName)
		}

		token := cllama.GenerateToken(serviceName + "-history")
		agents := uniqueSortedStrings(agentIDs)
		if svc.Environment == nil {
			svc.Environment = make(map[string]string)
		}
		svc.Environment["CLAW_HISTORY_URL"] = historyReplayBaseURL
		svc.Environment["CLAW_HISTORY_TOKEN"] = token
		svc.Environment["CLAW_HISTORY_AGENT_IDS"] = strings.Join(agents, ",")
		if err := ensureServiceOnNetwork(svc, clawInternalNetworkName); err != nil {
			return nil, fmt.Errorf("service %q: attach history replay network: %w", serviceName, err)
		}

		for _, agentID := range agents {
			serviceAuth[agentID] = cllama.ServiceAuthEntry{
				Service:   historyReplayAuthService,
				AuthType:  "bearer",
				Token:     token,
				Principal: serviceName,
			}
		}
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

func mergeServiceAuthEntries(groups ...[]cllama.ServiceAuthEntry) []cllama.ServiceAuthEntry {
	if len(groups) == 0 {
		return nil
	}

	seen := make(map[string]struct{})
	merged := make([]cllama.ServiceAuthEntry, 0)
	for _, group := range groups {
		for _, entry := range group {
			key := entry.Service + "\x00" + entry.AuthType + "\x00" + entry.Token
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			merged = append(merged, entry)
		}
	}
	if len(merged) == 0 {
		return nil
	}
	return merged
}

func ensureServiceOnNetwork(svc *pod.Service, network string) error {
	if svc == nil {
		return nil
	}
	if svc.Compose == nil {
		svc.Compose = make(map[string]interface{})
	}
	networks, err := appendComposeNetwork(svc.Compose["networks"], network)
	if err != nil {
		return err
	}
	svc.Compose["networks"] = networks
	return nil
}

func appendComposeString(base interface{}, value string) (interface{}, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return base, nil
	}
	switch current := base.(type) {
	case nil:
		return []string{value}, nil
	case []string:
		for _, item := range current {
			if item == value {
				return current, nil
			}
		}
		return append(current, value), nil
	case []interface{}:
		for _, item := range current {
			if s, ok := item.(string); ok && s == value {
				return current, nil
			}
		}
		return append(current, value), nil
	default:
		return nil, fmt.Errorf("expected list, got %T", base)
	}
}

func appendComposeNetwork(base interface{}, network string) (interface{}, error) {
	switch tv := base.(type) {
	case nil:
		return []string{network}, nil
	case []string:
		out := append([]string(nil), tv...)
		for _, existing := range out {
			if existing == network {
				return out, nil
			}
		}
		return append(out, network), nil
	case []interface{}:
		out := make([]interface{}, 0, len(tv)+1)
		found := false
		for _, item := range tv {
			out = append(out, item)
			if s, ok := item.(string); ok && s == network {
				found = true
			}
		}
		if !found {
			out = append(out, network)
		}
		return out, nil
	case map[string]interface{}:
		out := make(map[string]interface{}, len(tv)+1)
		for key, value := range tv {
			out[key] = value
		}
		if _, ok := out[network]; !ok {
			out[network] = nil
		}
		return out, nil
	default:
		return nil, fmt.Errorf("unsupported networks value type %T", base)
	}
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
	if strings.TrimSpace(env["REPO_ROOT"]) == "" {
		env["REPO_ROOT"] = podDir
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

// mergeModelSlots overlays pod-declared model slots onto image-declared slots.
// Image-only slots are preserved; pod entries replace image entries by key.
// Empty or nil pod maps suppress pod defaults only; image labels still apply.
func mergeModelSlots(image, pod map[string]string) map[string]string {
	out := cloneStringMap(image)
	if len(pod) == 0 {
		return out
	}
	if out == nil {
		out = make(map[string]string, len(pod))
	}
	for key, value := range pod {
		out[key] = value
	}
	return out
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

func detectHermes(claws map[string]*driver.ResolvedClaw) bool {
	for _, rc := range claws {
		if rc.ClawType == "hermes" {
			return true
		}
	}
	return false
}

func resolveAgentTimezone(env map[string]string, runtimeEnv map[string]string) string {
	const fallback = "UTC"

	raw := strings.TrimSpace(env["TZ"])
	if raw == "" {
		return fallback
	}

	tz := raw
	if strings.Contains(raw, "${") {
		expanded, err := expandRuntimeValue(raw, runtimeEnv)
		if err != nil {
			return fallback
		}
		tz = expanded
	}
	tz = strings.TrimSpace(tz)
	if tz == "" {
		return fallback
	}
	if _, err := time.LoadLocation(tz); err != nil {
		return fallback
	}
	return tz
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

// -- provider seed helpers ---------------------------------------------------

// seedKeyDef maps a cllama-env var name to its deterministic pool attributes.
type seedKeyDef struct {
	envVar   string
	provider string
	keyID    string // e.g. "seed:OPENAI_API_KEY"
	label    string
}

var seedKeyDefs = []seedKeyDef{
	{"OPENAI_API_KEY", "openai", "seed:OPENAI_API_KEY", "primary"},
	{"OPENAI_API_KEY_1", "openai", "seed:OPENAI_API_KEY_1", "backup-1"},
	{"OPENAI_API_KEY_2", "openai", "seed:OPENAI_API_KEY_2", "backup-2"},
	{"XAI_API_KEY", "xai", "seed:XAI_API_KEY", "primary"},
	{"XAI_API_KEY_1", "xai", "seed:XAI_API_KEY_1", "backup-1"},
	{"ANTHROPIC_API_KEY", "anthropic", "seed:ANTHROPIC_API_KEY", "primary"},
	{"ANTHROPIC_API_KEY_1", "anthropic", "seed:ANTHROPIC_API_KEY_1", "backup-1"},
	{"OPENROUTER_API_KEY", "openrouter", "seed:OPENROUTER_API_KEY", "primary"},
	{"OPENROUTER_API_KEY_1", "openrouter", "seed:OPENROUTER_API_KEY_1", "backup-1"},
	{"GEMINI_API_KEY", "google", "seed:GEMINI_API_KEY", "primary"},
	{"GEMINI_API_KEY_1", "google", "seed:GEMINI_API_KEY_1", "backup-1"},
	{"GOOGLE_API_KEY", "google", "seed:GOOGLE_API_KEY", "backup-2"},
}

// v2ProviderFile is the providers.json v2 on-disk shape (write path only).
type v2ProviderFile struct {
	Version   int                         `json:"version"`
	Providers map[string]*v2ProviderState `json:"providers"`
}

type v2ProviderState struct {
	BaseURL     string       `json:"base_url"`
	Auth        string       `json:"auth,omitempty"`
	APIFormat   string       `json:"api_format,omitempty"`
	ActiveKeyID string       `json:"active_key_id,omitempty"`
	Source      string       `json:"source,omitempty"` // "seed" | "runtime"
	Keys        []v2KeyEntry `json:"keys"`
}

type v2KeyEntry struct {
	ID              string `json:"id"`
	Label           string `json:"label,omitempty"`
	Secret          string `json:"secret"`
	Source          string `json:"source"`
	State           string `json:"state"`
	CooldownUntil   string `json:"cooldown_until"`
	LastErrorCode   int    `json:"last_error_code"`
	LastErrorReason string `json:"last_error_reason"`
	LastErrorAt     string `json:"last_error_at"`
	AddedAt         string `json:"added_at"`
}

var defaultBaseURLs = map[string]string{
	"openai":     "https://api.openai.com/v1",
	"xai":        "https://api.x.ai/v1",
	"anthropic":  "https://api.anthropic.com/v1",
	"openrouter": "https://openrouter.ai/api/v1",
	"google":     "https://generativelanguage.googleapis.com/v1beta/openai",
	"ollama":     "http://ollama:11434/v1",
}

var defaultAuths = map[string]string{
	"anthropic": "x-api-key",
	"ollama":    "none",
}

var defaultFormats = map[string]string{
	"anthropic": "anthropic",
}

// mergeProviderSeeds loads any existing providers.json from authDir, merges
// seed keys compiled from the pod's cllama-env, seeds keyless providers
// referenced by resolved models, and writes the result atomically.
func mergeProviderSeeds(authDir string, p *pod.Pod, claws map[string]*driver.ResolvedClaw) error {
	// Collect deduplicated cllama-env from all services.
	merged := make(map[string]string)
	for _, svc := range p.Services {
		if svc == nil || svc.Claw == nil {
			continue
		}
		for k, v := range svc.Claw.CllamaEnv {
			if _, exists := merged[k]; !exists {
				merged[k] = v
			}
		}
	}

	// Load existing file (v1 or v2) into our local v2 struct.
	existing := loadExistingProviders(authDir)

	// Build new provider states from seed defs.
	// Group seed defs by provider.
	type provSeed struct {
		def    seedKeyDef
		secret string
	}
	bySvc := make(map[string][]provSeed)
	for _, def := range seedKeyDefs {
		v := strings.TrimSpace(merged[def.envVar])
		if v == "" {
			continue
		}
		bySvc[def.provider] = append(bySvc[def.provider], provSeed{def, v})
	}

	// Also collect base URLs from cllama-env.
	baseURLEnvMap := map[string]string{
		"OPENAI_BASE_URL":     "openai",
		"XAI_BASE_URL":        "xai",
		"ANTHROPIC_BASE_URL":  "anthropic",
		"OPENROUTER_BASE_URL": "openrouter",
		"GOOGLE_BASE_URL":     "google",
		"OLLAMA_BASE_URL":     "ollama",
	}
	customBaseURLs := make(map[string]string)
	for envKey, prov := range baseURLEnvMap {
		if v := strings.TrimSpace(merged[envKey]); v != "" {
			customBaseURLs[prov] = v
		}
	}

	now := time.Now().UTC().Format(time.RFC3339)

	// Seed keyless providers (auth "none", e.g. ollama) referenced by
	// resolved models — image MODEL labels merged with pod overrides. No
	// cllama-env key exists for them, so the key-based loop below never
	// creates them — but without a provider entry, cllama rejects every
	// request with "unknown provider". A placeholder key is required even
	// under auth "none": cllama's key selection demands an active key
	// before dispatch, while the auth mode strips the header so the
	// placeholder never goes on the wire.
	for _, rc := range claws {
		if rc == nil || len(rc.Cllama) == 0 {
			continue
		}
		for _, provider := range shared.CollectProviders(rc.Models) {
			if !shared.ProviderAllowsEmptyAPIKey(provider) {
				continue
			}
			state, exists := existing[provider]
			if exists {
				// Seed-owned entries refresh their base URL on re-up (the
				// operator may have switched sidecar/host mode); runtime-
				// owned entries are never touched.
				if state.Source == "seed" {
					if cu := customBaseURLs[provider]; cu != "" {
						state.BaseURL = cu
					} else if defaultBaseURLs[provider] != "" {
						state.BaseURL = defaultBaseURLs[provider]
					}
				}
				continue
			}
			state = &v2ProviderState{
				Auth:        defaultAuths[provider],
				APIFormat:   "openai",
				Source:      "seed",
				ActiveKeyID: "seed:keyless",
				Keys: []v2KeyEntry{{
					ID:      "seed:keyless",
					Label:   "keyless",
					Secret:  "none",
					Source:  "seed",
					State:   "ready",
					AddedAt: now,
				}},
			}
			if cu := customBaseURLs[provider]; cu != "" {
				state.BaseURL = cu
			} else {
				state.BaseURL = defaultBaseURLs[provider]
			}
			existing[provider] = state
		}
	}

	// Merge for each provider that has seeds.
	for provName, seeds := range bySvc {
		state, exists := existing[provName]
		if !exists {
			state = &v2ProviderState{
				Auth:      defaultAuths[provName],
				APIFormat: defaultFormats[provName],
			}
			if state.Auth == "" {
				state.Auth = "bearer"
			}
			if state.APIFormat == "" {
				state.APIFormat = "openai"
			}
		}
		// Update base URL.
		if cu := customBaseURLs[provName]; cu != "" {
			state.BaseURL = cu
		}
		if state.BaseURL == "" {
			state.BaseURL = defaultBaseURLs[provName]
		}

		// Build index of existing keys by ID.
		existingByID := make(map[string]*v2KeyEntry, len(state.Keys))
		for i := range state.Keys {
			existingByID[state.Keys[i].ID] = &state.Keys[i]
		}

		// Determine which seed IDs are present in the new config.
		newSeedIDs := make(map[string]bool)
		for _, s := range seeds {
			newSeedIDs[s.def.keyID] = true
		}

		// Keep runtime keys unchanged; rebuild seed keys.
		var runtimeKeys []v2KeyEntry
		for _, k := range state.Keys {
			if k.Source == "runtime" {
				runtimeKeys = append(runtimeKeys, k)
			}
		}

		var newKeys []v2KeyEntry
		var firstSeedID string
		for _, s := range seeds {
			if firstSeedID == "" {
				firstSeedID = s.def.keyID
			}
			if old, ok := existingByID[s.def.keyID]; ok && old.Source == "seed" {
				if old.Secret == s.secret {
					// Same secret — keep existing runtime state (e.g., dead stays dead).
					newKeys = append(newKeys, *old)
				} else {
					// Different secret — reset to ready.
					*old = v2KeyEntry{
						ID:      s.def.keyID,
						Label:   s.def.label,
						Secret:  s.secret,
						Source:  "seed",
						State:   "ready",
						AddedAt: now,
					}
					newKeys = append(newKeys, *old)
				}
			} else {
				newKeys = append(newKeys, v2KeyEntry{
					ID:      s.def.keyID,
					Label:   s.def.label,
					Secret:  s.secret,
					Source:  "seed",
					State:   "ready",
					AddedAt: now,
				})
			}
		}
		// Append runtime keys after seed keys.
		newKeys = append(newKeys, runtimeKeys...)
		state.Keys = newKeys

		// Preserve active_key_id if it still exists, otherwise default to first seed.
		found := false
		for _, k := range state.Keys {
			if k.ID == state.ActiveKeyID {
				found = true
				break
			}
		}
		if !found {
			state.ActiveKeyID = firstSeedID
		}

		if old, alreadyExists := existing[provName]; alreadyExists && old.Source == "runtime" {
			fmt.Fprintf(os.Stderr, "warning: claw up seeds provider %q, overwriting runtime-added provider\n", provName)
		}
		existing[provName] = state
	}

	out := v2ProviderFile{
		Version:   2,
		Providers: existing,
	}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	dest := filepath.Join(authDir, "providers.json")
	tmp := dest + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, dest)
}

// loadExistingProviders reads providers.json from authDir, handling v1 and v2 formats.
// Returns an empty map if the file doesn't exist.
func loadExistingProviders(authDir string) map[string]*v2ProviderState {
	out := make(map[string]*v2ProviderState)
	path := filepath.Join(authDir, "providers.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return out
	}

	var probe struct {
		Version   int                        `json:"version"`
		Providers map[string]json.RawMessage `json:"providers"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return out
	}

	if probe.Version >= 2 {
		// v2: unmarshal full ProviderState.
		for name, raw := range probe.Providers {
			var state v2ProviderState
			if err := json.Unmarshal(raw, &state); err == nil {
				cp := state
				out[strings.ToLower(strings.TrimSpace(name))] = &cp
			}
		}
		return out
	}

	// v1: extract api_key into a single seed entry.
	for name, raw := range probe.Providers {
		var v1 struct {
			BaseURL   string `json:"base_url"`
			APIKey    string `json:"api_key"`
			Auth      string `json:"auth"`
			APIFormat string `json:"api_format"`
		}
		if err := json.Unmarshal(raw, &v1); err != nil {
			continue
		}
		n := strings.ToLower(strings.TrimSpace(name))
		state := &v2ProviderState{
			BaseURL:   v1.BaseURL,
			Auth:      v1.Auth,
			APIFormat: v1.APIFormat,
		}
		if v1.APIKey != "" {
			keyID := "seed:" + strings.ToUpper(n) + "_API_KEY"
			state.ActiveKeyID = keyID
			state.Keys = []v2KeyEntry{{
				ID:      keyID,
				Label:   "primary",
				Secret:  v1.APIKey,
				Source:  "seed",
				State:   "ready",
				AddedAt: time.Now().UTC().Format(time.RFC3339),
			}}
		}
		out[n] = state
	}
	return out
}

// loadOrGenerateUIToken reads the persisted UI token from authDir, or generates
// and writes a new one if none exists.
func loadOrGenerateUIToken(authDir string) (string, error) {
	tokenPath := filepath.Join(authDir, "ui-token")
	data, err := os.ReadFile(tokenPath)
	if err == nil {
		token := strings.TrimSpace(string(data))
		if token != "" {
			return token, nil
		}
	}
	// Generate a new 32-byte random token.
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate UI token: %w", err)
	}
	token := hex.EncodeToString(b)
	if err := os.WriteFile(tokenPath, []byte(token+"\n"), 0o600); err != nil {
		return "", fmt.Errorf("write UI token: %w", err)
	}
	return token, nil
}

func isProviderKey(key string) bool {
	switch key {
	case "OPENAI_API_KEY", "OPENAI_API_KEY_1", "OPENAI_API_KEY_2",
		"XAI_API_KEY", "XAI_API_KEY_1",
		"ANTHROPIC_API_KEY", "ANTHROPIC_API_KEY_1",
		"OPENROUTER_API_KEY", "OPENROUTER_API_KEY_1",
		"GEMINI_API_KEY", "GEMINI_API_KEY_1", "GOOGLE_API_KEY":
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
	if serviceName == conversationWallServiceName && p != nil && p.Services[conversationWallServiceName] != nil {
		descriptor := builtinClawWallDescriptor()
		descriptors[serviceName] = descriptor
		return strings.TrimSpace(svc.Image), infos[serviceName], descriptor, nil
	}

	if descriptorPath := explicitDescribeFile(podDir, svc); descriptorPath != "" {
		descriptor, err := loadDescriptorFromFile(descriptorPath)
		if err != nil {
			return "", nil, nil, fmt.Errorf("load descriptor from describe-file: %w", err)
		}
		if svc != nil && strings.TrimSpace(svc.Image) != "" {
			imageRefs[serviceName] = strings.TrimSpace(svc.Image)
		}
		descriptors[serviceName] = descriptor
		return strings.TrimSpace(svc.Image), infos[serviceName], descriptor, nil
	}

	if descriptorPath := discoveredDescribeFile(podDir, serviceName, svc); descriptorPath != "" {
		descriptor, err := loadDescriptorFromFile(descriptorPath)
		if err != nil {
			return "", nil, nil, fmt.Errorf("load discovered descriptor %q: %w", descriptorPath, err)
		}
		if svc != nil && svc.IsMCPStdioSidecar() {
			warnDiscoveryDrift(serviceName, descriptor, svc)
		} else if !runtimeDescriptorSnapshotUsable(descriptor, svc) {
			descriptor = nil
		}
		if descriptor != nil {
			if svc != nil && strings.TrimSpace(svc.Image) != "" {
				imageRefs[serviceName] = strings.TrimSpace(svc.Image)
			}
			descriptors[serviceName] = descriptor
			return strings.TrimSpace(svc.Image), infos[serviceName], descriptor, nil
		}
	}

	imageRef, info, err := inspectServiceMetadata(podDir, p, serviceName, svc, imageRefs, infos)
	if err != nil {
		return "", nil, nil, err
	}

	var descriptor *describe.ServiceDescriptor
	if imageRef != "" {
		descriptorPath, implicitDescriptorPath := resolvedImageDescriptorPath(info)
		if imageExistsLocally(imageRef) {
			descriptor, err = loadDescriptorFromImage(imageRef, descriptorPath)
			if err != nil {
				if !(implicitDescriptorPath && errors.Is(err, os.ErrNotExist)) {
					return "", nil, nil, fmt.Errorf("load descriptor from image: %w", err)
				}
			} else {
				descriptors[serviceName] = descriptor
				return imageRef, info, descriptor, nil
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
	if descriptor == nil && svc != nil && svc.IsMCPStdioSidecar() {
		return "", nil, nil, fmt.Errorf("stdio MCP sidecar has no descriptor snapshot; run 'claw discover %s' or set x-claw.describe-file", serviceName)
	}
	return imageRef, info, descriptor, nil
}

func explicitDescribeFile(podDir string, svc *pod.Service) string {
	if svc == nil || svc.Claw == nil {
		return ""
	}
	path := strings.TrimSpace(svc.Claw.DescribeFile)
	if path == "" {
		return ""
	}
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Clean(filepath.Join(podDir, path))
}

func discoveredDescribeFile(podDir, serviceName string, svc *pod.Service) string {
	if svc == nil || (!svc.IsMCPStdioSidecar() && !serviceHasBuildConfig(svc)) {
		return ""
	}
	path := discoveredSnapshotPath(podDir, serviceName)
	if _, err := os.Stat(path); err == nil {
		return path
	}
	return ""
}

func runtimeDescriptorSnapshotUsable(descriptor *describe.ServiceDescriptor, svc *pod.Service) bool {
	if descriptor == nil || svc == nil {
		return false
	}
	meta := descriptor.XClawDiscovery
	if meta == nil || strings.TrimSpace(meta.Command) != "runtime-descriptor" {
		return false
	}
	imageRef := strings.TrimSpace(svc.Image)
	if meta.WrapperImage != "" && imageRef != "" && meta.WrapperImage != imageRef {
		return false
	}
	if meta.WrapperImageID != "" && imageRef != "" {
		currentID, _ := discoveryImageIdentity(context.Background(), imageRef)
		if currentID != "" && currentID != meta.WrapperImageID {
			return false
		}
	}
	return true
}

func refreshRuntimeDescriptorDefault(podFile, podDir string, p *pod.Pod, serviceName string, imageRefs map[string]string, infos map[string]*inspect.ClawInfo, descriptors map[string]*describe.ServiceDescriptor) error {
	if p == nil {
		return fmt.Errorf("refresh runtime descriptor: pod is required")
	}
	svc := p.Services[serviceName]
	if svc == nil {
		return fmt.Errorf("refresh runtime descriptor: service %q not found", serviceName)
	}
	cfg, err := parseServiceBuildConfig(svc.Compose["build"])
	if err != nil {
		return fmt.Errorf("service %q: parse build: %w", serviceName, err)
	}
	if cfg == nil {
		return fmt.Errorf("service %q: runtime descriptor refresh requires a build-backed service", serviceName)
	}

	imageRef := strings.TrimSpace(svc.Image)
	if imageRef == "" {
		imageRef = managedServiceImageRef(p.Name, serviceName)
		assignServiceImageRef(svc, imageRef)
	}

	fmt.Printf("[claw] %s: refreshing runtime descriptor via --fix\n", serviceName)
	if err := buildManagedServiceImage(podFile, podDir, imageRef, cfg); err != nil {
		return fmt.Errorf("service %q: build before descriptor refresh: %w", serviceName, err)
	}

	delete(imageRefs, serviceName)
	delete(infos, serviceName)
	imageRef, info, err := inspectServiceMetadata(podDir, p, serviceName, svc, imageRefs, infos)
	if err != nil {
		return fmt.Errorf("service %q: inspect descriptor metadata after build: %w", serviceName, err)
	}
	descriptorPath, _ := resolvedImageDescriptorPath(info)

	composePath := filepath.Join(podDir, "compose.generated.yml")
	if _, err := os.Stat(composePath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("service %q: cannot refresh runtime descriptor because compose.generated.yml is missing; run an initial deploy with a baked descriptor or set x-claw.describe-file", serviceName)
		}
		return fmt.Errorf("service %q: stat compose.generated.yml: %w", serviceName, err)
	}

	if out, err := runRuntimeDescriptorCommand("compose", "-f", composePath, "up", "-d", "--force-recreate", serviceName); err != nil {
		return fmt.Errorf("service %q: start provider for descriptor refresh: %w%s", serviceName, err, commandOutputSuffix(out))
	}

	descriptor, err := copyRuntimeDescriptorSnapshot(composePath, serviceName, descriptorPath, imageRef)
	if err != nil {
		return err
	}
	descriptors[serviceName] = descriptor
	fmt.Printf("[claw] %s: refreshed descriptor snapshot %s\n", serviceName, discoveredSnapshotPath(podDir, serviceName))
	return nil
}

func copyRuntimeDescriptorSnapshot(composePath, serviceName, descriptorPath, imageRef string) (*describe.ServiceDescriptor, error) {
	podDir := filepath.Dir(composePath)
	snapshotPath := discoveredSnapshotPath(podDir, serviceName)
	tmp, err := os.CreateTemp("", "claw-runtime-descriptor-*.json")
	if err != nil {
		return nil, fmt.Errorf("service %q: create descriptor refresh temp file: %w", serviceName, err)
	}
	tmpPath := tmp.Name()
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return nil, fmt.Errorf("service %q: close descriptor refresh temp file: %w", serviceName, err)
	}
	defer os.Remove(tmpPath)

	deadline := time.Now().Add(runtimeDescriptorRefreshTimeout)
	var lastErr error
	for {
		_ = os.Remove(tmpPath)
		source := fmt.Sprintf("%s:%s", serviceName, descriptorPath)
		if out, err := runRuntimeDescriptorCommand("compose", "-f", composePath, "cp", source, tmpPath); err != nil {
			lastErr = fmt.Errorf("docker compose cp: %w%s", err, commandOutputSuffix(out))
		} else if descriptor, err := loadDescriptorFromFile(tmpPath); err != nil {
			lastErr = err
		} else {
			imageID, digest := discoveryImageIdentity(context.Background(), imageRef)
			descriptor.XClawDiscovery = &describe.DiscoveryMetadata{
				Command:            "runtime-descriptor",
				WrapperImage:       imageRef,
				WrapperImageDigest: digest,
				WrapperImageID:     imageID,
				DiscoveredAt:       time.Now().UTC().Format(time.RFC3339),
			}
			if err := writeDescriptorSnapshot(snapshotPath, descriptor); err != nil {
				return nil, fmt.Errorf("service %q: write runtime descriptor snapshot: %w", serviceName, err)
			}
			return descriptor, nil
		}

		if runtimeDescriptorRefreshTimeout <= 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(runtimeDescriptorRefreshPollInterval)
	}

	logs := runtimeDescriptorLogs(composePath, serviceName)
	return nil, fmt.Errorf("service %q: descriptor %q was not available after refresh: %v%s", serviceName, descriptorPath, lastErr, logs)
}

func runtimeDescriptorLogs(composePath, serviceName string) string {
	out, err := runRuntimeDescriptorCommand("compose", "-f", composePath, "logs", "--tail", "80", serviceName)
	if err != nil {
		return fmt.Sprintf("\ncontainer logs unavailable: %v%s", err, commandOutputSuffix(out))
	}
	if strings.TrimSpace(string(out)) == "" {
		return "\ncontainer logs were empty"
	}
	return "\ncontainer logs:\n" + string(out)
}

func commandOutputSuffix(out []byte) string {
	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" {
		return ""
	}
	return ": " + trimmed
}

func resolvedImageDescriptorPath(info *inspect.ClawInfo) (string, bool) {
	if info != nil && strings.TrimSpace(info.DescribePath) != "" {
		return strings.TrimSpace(info.DescribePath), false
	}
	return "/" + describe.DefaultDescriptorFile, true
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
	if info == nil {
		var err error
		info, err = inspectBuildMetadata(podDir, svc.Compose["build"])
		if err != nil {
			return "", nil, err
		}
	}
	imageRefs[serviceName] = imageRef
	infos[serviceName] = info
	return imageRef, info, nil
}

func inspectBuildMetadata(podDir string, buildRaw interface{}) (*inspect.ClawInfo, error) {
	if buildRaw == nil {
		return nil, nil
	}

	contextDir, err := describe.ResolveBuildContextDir(podDir, buildRaw)
	if err != nil || contextDir == "" {
		return nil, err
	}

	dockerfilePath, err := resolveBuildDockerfilePath(contextDir, resolveComposeBuildDockerfile(buildRaw))
	if err != nil {
		return nil, err
	}
	return loadDockerfileMetadata(dockerfilePath)
}

func resolveComposeBuildDockerfile(buildRaw interface{}) string {
	switch raw := buildRaw.(type) {
	case map[string]interface{}:
		if dockerfile, ok := raw["dockerfile"].(string); ok {
			return strings.TrimSpace(dockerfile)
		}
	case map[interface{}]interface{}:
		if dockerfile, ok := raw["dockerfile"].(string); ok {
			return strings.TrimSpace(dockerfile)
		}
	}
	return ""
}

func collectServiceDescriptors(podDir string, p *pod.Pod, imageRefs map[string]string, infos map[string]*inspect.ClawInfo, descriptors map[string]*describe.ServiceDescriptor) error {
	if p == nil {
		return nil
	}
	if p.ClawAPI != nil {
		descriptors["claw-api"] = builtinClawAPIDescriptor()
	}
	if p.Services[conversationWallServiceName] != nil {
		descriptors[conversationWallServiceName] = builtinClawWallDescriptor()
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
				return &feedResolutionError{
					ConsumerService: serviceName,
					FeedName:        feed.Name,
				}
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

func cloneHermesConfig(cfg *driver.HermesConfig) *driver.HermesConfig {
	if cfg == nil {
		return nil
	}
	return &driver.HermesConfig{
		AllowTools: append([]string(nil), cfg.AllowTools...),
	}
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
	if len(descriptor.Endpoints) > 0 && len(descriptor.Tools) == 0 {
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
		Description: "Governance API for fleet telemetry, health, logs, metrics, alerts, and schedule state/control.",
		Feeds: []describe.FeedDescriptor{{
			Name: "fleet-alerts",
			// Keep the pushed anomaly feed on a shorter horizon so agents do not
			// self-report long-cleared incidents for a full hour.
			Path:        "/fleet/alerts?since=15m",
			TTL:         30,
			Description: "Threshold-based fleet alert summaries for the pod.",
		}},
		Endpoints: []describe.EndpointDescriptor{
			{Method: "GET", Path: "/fleet/status", Description: "Scoped service health and uptime."},
			{Method: "GET", Path: "/fleet/logs", Description: "Recent logs for one in-scope service."},
			{Method: "GET", Path: "/fleet/metrics", Description: "Normalized telemetry for one claw."},
			{Method: "GET", Path: "/fleet/alerts", Description: "Threshold-based anomaly summaries across the fleet."},
			{Method: "GET", Path: "/schedule", Description: "Current scheduled invocation state for in-scope services."},
			{Method: "GET", Path: "/schedule/:id", Description: "Detail for one scheduled invocation."},
			{Method: "POST", Path: "/schedule/:id/pause", Description: "Pause one scheduled invocation, optionally until a timestamp."},
			{Method: "POST", Path: "/schedule/:id/resume", Description: "Clear pause state for one scheduled invocation."},
			{Method: "POST", Path: "/schedule/:id/skip-next", Description: "Skip the next scheduled fire for one invocation."},
			{Method: "POST", Path: "/schedule/:id/fire", Description: "Trigger an immediate fire for one scheduled invocation."},
		},
		Auth: &describe.AuthDescriptor{
			Type: "bearer",
			Env:  "CLAW_API_TOKEN",
		},
	}
}

func builtinClawWallDescriptor() *describe.ServiceDescriptor {
	return &describe.ServiceDescriptor{
		Version:     2,
		Description: "Conversation wall channel context, awareness, and retrieval for Discord-backed surfaces.",
		Auth: &describe.AuthDescriptor{
			Type: "bearer",
			Env:  conversationWallToolTokenEnv,
		},
		Tools: []describe.ToolDescriptor{
			{
				Name:        "search_channel_context",
				Description: "Search this agent's recent raw channel buffer and durable channel-memory results when configured.",
				InputSchema: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"channels": map[string]interface{}{
							"type":  "array",
							"items": map[string]interface{}{"type": "string"},
						},
						"query": map[string]interface{}{
							"type": "string",
						},
						"since": map[string]interface{}{
							"type":    "string",
							"default": "24h",
						},
						"author": map[string]interface{}{
							"type": "string",
						},
						"limit": map[string]interface{}{
							"type":    "integer",
							"default": 20,
						},
					},
					"required": []interface{}{"channels", "query"},
				},
				HTTP: &describe.ToolHTTP{
					Method: http.MethodPost,
					Path:   "/search_channel_context",
				},
			},
			{
				Name:        "get_channel_messages",
				Description: "Fetch exact channel messages by message ID, source handle, ID range, or author/time window.",
				InputSchema: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"channels": map[string]interface{}{
							"type":  "array",
							"items": map[string]interface{}{"type": "string"},
						},
						"message_ids": map[string]interface{}{
							"type":  "array",
							"items": map[string]interface{}{"type": "string"},
						},
						"source_handles": map[string]interface{}{
							"type":  "array",
							"items": map[string]interface{}{"type": "string"},
						},
						"after": map[string]interface{}{
							"type": "string",
						},
						"before": map[string]interface{}{
							"type": "string",
						},
						"author": map[string]interface{}{
							"type": "string",
						},
						"limit": map[string]interface{}{
							"type":    "integer",
							"default": 20,
						},
					},
					"required": []interface{}{"channels"},
				},
				HTTP: &describe.ToolHTTP{
					Method: http.MethodPost,
					Path:   "/get_channel_messages",
				},
			},
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

func resolveManagedServiceImage(_ string, p *pod.Pod, serviceName string, svc *pod.Service) (string, error) {
	imageRef := strings.TrimSpace(svc.Image)
	cfg, err := parseServiceBuildConfig(svc.Compose["build"])
	if err != nil {
		return "", fmt.Errorf("service %q: parse build: %w", serviceName, err)
	}

	if cfg == nil {
		if imageRef == "" {
			return "", fmt.Errorf("service %q: claw-managed services require image: or build:", serviceName)
		}
		if imageExistsLocally(imageRef) {
			return imageRef, nil
		}
		return "", remediationErrorf("claw pull", "service %q image %q missing locally", serviceName, imageRef)
	}

	if imageRef == "" {
		imageRef = managedServiceImageRef(p.Name, serviceName)
	}
	assignServiceImageRef(svc, imageRef)

	if !imageExistsLocally(imageRef) {
		return "", remediationErrorf("claw build", "service %q image not built", serviceName)
	}
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

func buildManagedServiceImage(podFile, podDir, imageRef string, cfg *serviceBuildConfig) error {
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
			return fmt.Errorf("generate Dockerfile from %q: %w", dockerfilePath, formatGenerateError(err, podFile, dockerfilePath))
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

// ensurePersistentCllamaDir creates a persistent directory at podDir/<name> that
// survives claw up resets (unlike .claw-runtime which is wiped on every up).
// Returns the absolute path. Permissions are 0o777 so container users with
// different UIDs can write.
func ensurePersistentCllamaDir(podDir, name string) (string, error) {
	dir := filepath.Join(podDir, name)
	if err := os.MkdirAll(dir, 0o777); err != nil {
		return "", fmt.Errorf("create %s dir: %w", name, err)
	}
	return dir, nil
}

func appendPersistentSkillMount(result *driver.MaterializeResult, skillRoot string, rc *driver.ResolvedClaw) error {
	if result == nil {
		return nil
	}
	containerSkillDir := strings.TrimSpace(result.SkillDir)
	if containerSkillDir == "" {
		return nil
	}
	if rc == nil {
		return fmt.Errorf("resolved claw is nil")
	}
	serviceName := strings.TrimSpace(rc.ServiceName)
	ids := expandedServiceNames(serviceName, rc.Count)
	hostPaths := make(map[string]string, len(ids))
	for _, id := range ids {
		hostSkillDir, err := preparePersistentSkillDir(skillRoot, id)
		if err != nil {
			return err
		}
		hostPaths[id] = hostSkillDir
	}
	mount := driver.Mount{
		ContainerPath: containerSkillDir,
		ReadOnly:      false,
	}
	if len(ids) == 1 {
		mount.HostPath = hostPaths[ids[0]]
	} else {
		mount.HostPathByService = hostPaths
	}
	result.Mounts = append(result.Mounts, mount)
	return nil
}

func preparePersistentSkillDir(skillRoot, serviceName string) (string, error) {
	serviceName = strings.TrimSpace(serviceName)
	if serviceName == "" || serviceName == "." || serviceName == ".." ||
		strings.HasPrefix(serviceName, ".") ||
		strings.ContainsAny(serviceName, `/\`) {
		return "", fmt.Errorf("invalid service name %q", serviceName)
	}
	if strings.TrimSpace(skillRoot) == "" {
		return "", fmt.Errorf("skill root is empty")
	}

	dir := filepath.Join(skillRoot, serviceName, "skills")
	if err := os.MkdirAll(dir, 0o777); err != nil {
		return "", fmt.Errorf("create persistent skill dir: %w", err)
	}
	if err := os.Chmod(dir, 0o777); err != nil {
		return "", fmt.Errorf("chmod persistent skill dir: %w", err)
	}
	return dir, nil
}

func ensureInfraImages(p *pod.Pod, cllamaEnabled bool, proxies []pod.CllamaProxyConfig, api *pod.ClawAPIConfig, dash *pod.ClawdashConfig) error {
	for _, spec := range requiredInfraImageSpecs(p, cllamaEnabled, proxies, api, dash) {
		if err := pullInfraImageFromRegistry(spec); err != nil {
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

func runRuntimeDescriptorCommandDefault(args ...string) ([]byte, error) {
	cmd := exec.Command("docker", args...)
	return cmd.CombinedOutput()
}

func warnRunnerBaseDrift(podFile string, plans []plannedServiceImage) {
	buildCommand := "claw build"
	if strings.TrimSpace(podFile) != "" && strings.TrimSpace(podFile) != "claw-pod.yml" {
		buildCommand = "claw build -f " + strings.TrimSpace(podFile)
	}
	for _, plan := range plans {
		if plan.BuildConfig == nil || strings.TrimSpace(plan.ImageRef) == "" || !imageExistsLocally(plan.ImageRef) {
			continue
		}

		info, err := inspectClawImage(plan.ImageRef)
		if err != nil || info == nil || strings.TrimSpace(info.RunnerDriver) == "" || strings.TrimSpace(info.RunnerImage) == "" {
			continue
		}

		d, err := driver.Lookup(info.RunnerDriver)
		if err != nil {
			continue
		}
		provider, ok := d.(driver.RunnerBaseProvider)
		if !ok {
			continue
		}

		provenance, err := resolveLocalRunnerProvenance(info.RunnerDriver, provider)
		if err != nil || provenance == nil || strings.TrimSpace(provenance.ImageID) == "" {
			continue
		}
		if provenance.ImageID == info.RunnerImage {
			continue
		}

		builtRef := strings.TrimSpace(info.RunnerBuilt)
		if builtRef == "" {
			builtRef = plan.ImageRef
		}
		currentRef := strings.TrimSpace(provenance.BuiltRef)
		if currentRef == "" {
			currentRef = provenance.ImageRef
		}

		fmt.Printf("[claw] %s: built against %s (image %s), current local alias is %s (image %s); consider running: %s\n",
			plan.ServiceName,
			builtRef,
			shortImageIDForDisplay(info.RunnerImage),
			currentRef,
			shortImageIDForDisplay(provenance.ImageID),
			buildCommand,
		)
	}
}

func shortImageIDForDisplay(imageID string) string {
	trimmed := strings.TrimPrefix(strings.TrimSpace(imageID), "sha256:")
	if len(trimmed) > 12 {
		return trimmed[:12]
	}
	return trimmed
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
	composeUpCmd.Flags().BoolVar(&composeUpFix, "fix", false, "Pull/build missing images before starting the pod")
	composeUpCmd.Flags().BoolVar(&composeUpDiscoverTools, "discover-tools", false, "Discover missing or stale stdio MCP tool snapshots before compiling")
	rootCmd.AddCommand(composeUpCmd)
}
