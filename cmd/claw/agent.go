package main

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mostlydev/clawdapus/internal/pod"
	"github.com/mostlydev/clawdapus/internal/runtime"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var (
	agentNameFlag      string
	agentTypeFlag      string
	agentModelFlag     string
	agentCllamaFlag    string
	agentPlatformFlag  string
	agentContractFlag  string
	agentVolumesFlag   []string
	agentDryRunFlag    bool
	agentYesFlag       bool
	agentSharedProfile string
	agentLayoutFlag    string
)

const (
	agentLayoutCanonical = "canonical"
	agentLayoutFlat      = "flat"
)

type agentAddOptions struct {
	AgentName     string
	ClawType      string
	Model         string
	Cllama        string
	Platform      string
	ContractPath  string
	VolumeSpecs   []string
	DryRun        bool
	AssumeYes     bool
	SharedProfile string
	Layout        string
	InteractiveIO bool
}

type agentAddContext struct {
	RootDir           string
	PodName           string
	Layout            string
	HasCllama         bool
	CllamaValue       string
	CllamaEnv         map[string]string
	PreferredPlatform string
	HandleTemplate    *platformTemplate
	ExistingContracts []string
	ExistingVolumes   []string
	ExistingPrefixes  map[string][]string
	ExistingEnvKeys   map[string]struct{}
}

type platformTemplate struct {
	Username string
	Guilds   []platformGuild
}

type platformGuild struct {
	ID   string
	Name string
}

type agentAddResolvedConfig struct {
	AgentName        string
	ClawType         string
	Model            string
	Cllama           string
	Platform         string
	ContractPath     string
	AgentDirective   string
	ClawfileRelPath  string
	AgentFileRelPath string
	SkillsDirRelPath string
	CreateAgentFile  bool
	CreateSharedFile bool
	SharedFilePath   string
	SharedSourcePath string
	RewireExisting   bool
	VolumeModes      map[string]string
	EnvExampleVars   []string
	Image            string
	BuildContext     string
	BuildDockerfile  string
	CllamaEnv        map[string]string
	HandleTemplate   *platformTemplate
	Warnings         []string
}

type scaffoldBuild struct {
	Context    string `yaml:"context"`
	Dockerfile string `yaml:"dockerfile,omitempty"`
}

type scaffoldGuild struct {
	ID   string `yaml:"id"`
	Name string `yaml:"name,omitempty"`
}

type scaffoldHandle struct {
	ID       string          `yaml:"id"`
	Username string          `yaml:"username,omitempty"`
	Guilds   []scaffoldGuild `yaml:"guilds,omitempty"`
}

type scaffoldXClaw struct {
	Agent     string                    `yaml:"agent"`
	Cllama    string                    `yaml:"cllama,omitempty"`
	CllamaEnv map[string]string         `yaml:"cllama-env,omitempty"`
	Handles   map[string]scaffoldHandle `yaml:"handles,omitempty"`
	Surfaces  []string                  `yaml:"surfaces,omitempty"`
}

type scaffoldService struct {
	Image       string            `yaml:"image"`
	Build       scaffoldBuild     `yaml:"build"`
	XClaw       scaffoldXClaw     `yaml:"x-claw"`
	Environment map[string]string `yaml:"environment,omitempty"`
}

var agentCmd = &cobra.Command{
	Use:   "agent",
	Short: "Manage agents in a Clawdapus project",
}

var agentAddCmd = &cobra.Command{
	Use:   "add [name]",
	Short: "Add an agent to an existing project",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := strings.TrimSpace(agentNameFlag)
		if name == "" && len(args) == 1 {
			name = strings.TrimSpace(args[0])
		}
		if name == "" {
			return fmt.Errorf("agent name is required (pass positional name or --agent)")
		}

		podFile := composePodFile
		if podFile == "" {
			podFile = "claw-pod.yml"
		}
		absPodFile, err := filepath.Abs(podFile)
		if err != nil {
			return fmt.Errorf("resolve pod file %q: %w", podFile, err)
		}

		opts := agentAddOptions{
			AgentName:     name,
			ClawType:      agentTypeFlag,
			Model:         agentModelFlag,
			Cllama:        agentCllamaFlag,
			Platform:      agentPlatformFlag,
			ContractPath:  agentContractFlag,
			VolumeSpecs:   append([]string(nil), agentVolumesFlag...),
			DryRun:        agentDryRunFlag,
			AssumeYes:     agentYesFlag,
			SharedProfile: agentSharedProfile,
			Layout:        agentLayoutFlag,
			InteractiveIO: shouldPromptInteractively(),
		}

		return runAgentAdd(absPodFile, opts)
	},
}

func runAgentAdd(podFile string, opts agentAddOptions) error {
	if err := validateEntityName("agent", opts.AgentName); err != nil {
		return err
	}

	data, err := os.ReadFile(podFile)
	if err != nil {
		return fmt.Errorf("read pod file: %w", err)
	}

	parsedPod, err := pod.Parse(bytes.NewReader(data))
	if err != nil {
		return err
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("parse pod YAML AST: %w", err)
	}
	root, err := podRootMap(&doc)
	if err != nil {
		return err
	}

	podDir := filepath.Dir(podFile)
	ctx := buildAgentAddContext(podDir, parsedPod, root)
	resolved, err := resolveAgentAddConfig(ctx, opts)
	if err != nil {
		return err
	}

	servicesNode := ensureMappingNodeValue(root, "services")
	if findMapValue(servicesNode, resolved.AgentName) != nil {
		return fmt.Errorf("service %q already exists in %s", resolved.AgentName, filepath.Base(podFile))
	}

	clawfilePath := filepath.Join(podDir, filepath.FromSlash(resolved.ClawfileRelPath))
	agentFilePath := filepath.Join(podDir, filepath.FromSlash(resolved.AgentFileRelPath))
	envExamplePath := filepath.Join(podDir, ".env.example")

	createFiles := make([]string, 0, 3)
	createFiles = append(createFiles, clawfilePath)
	if resolved.CreateAgentFile {
		createFiles = append(createFiles, agentFilePath)
	}
	if resolved.CreateSharedFile {
		createFiles = append(createFiles, filepath.Join(podDir, filepath.FromSlash(strings.TrimPrefix(resolved.SharedFilePath, "./"))))
	}

	for _, path := range createFiles {
		if _, err := os.Stat(path); err == nil {
			rel, _ := filepath.Rel(podDir, path)
			return fmt.Errorf("%s already exists; refusing to overwrite", filepath.ToSlash(rel))
		} else if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("check %s: %w", path, err)
		}
	}

	svcNode, err := serviceNodeFromConfig(resolved)
	if err != nil {
		return err
	}
	setMapValue(servicesNode, resolved.AgentName, svcNode)

	if len(resolved.VolumeModes) > 0 {
		volumesNode := ensureMappingNodeValue(root, "volumes")
		for name := range resolved.VolumeModes {
			if findMapValue(volumesNode, name) == nil {
				setMapValue(volumesNode, name, &yaml.Node{
					Kind:    yaml.MappingNode,
					Content: []*yaml.Node{},
				})
			}
		}
	}

	rewiredCount := 0
	if resolved.RewireExisting && resolved.SharedSourcePath != "" && resolved.SharedFilePath != "" {
		rewiredCount = rewireContracts(servicesNode, normalizeContractPath(resolved.SharedSourcePath), normalizeContractPath(resolved.SharedFilePath))
	}

	podOut, err := marshalYAMLDocument(&doc)
	if err != nil {
		return err
	}

	planned := make([]string, 0, 8)
	planned = append(planned, fmt.Sprintf("+ create %s", resolved.ClawfileRelPath))
	if resolved.CreateAgentFile {
		planned = append(planned, fmt.Sprintf("+ create %s", resolved.AgentFileRelPath))
	}
	if resolved.CreateSharedFile {
		planned = append(planned, fmt.Sprintf("+ create %s", strings.TrimPrefix(resolved.SharedFilePath, "./")))
	}
	planned = append(planned, "~ update claw-pod.yml (add service "+resolved.AgentName+")")
	if rewiredCount > 0 {
		planned = append(planned, fmt.Sprintf("~ rewire %d existing service contract(s) to %s", rewiredCount, strings.TrimPrefix(resolved.SharedFilePath, "./")))
	}
	if len(resolved.EnvExampleVars) > 0 {
		planned = append(planned, fmt.Sprintf("~ update .env.example (append %s)", strings.Join(resolved.EnvExampleVars, ", ")))
	}

	fmt.Println("[claw] planned changes:")
	for _, line := range planned {
		fmt.Println(" ", line)
	}
	if len(resolved.Warnings) > 0 {
		fmt.Println("[claw] warnings:")
		for _, line := range resolved.Warnings {
			fmt.Println("  !", line)
		}
	}

	if opts.DryRun {
		fmt.Println("[claw] dry-run enabled; no files were written")
		return nil
	}

	if opts.InteractiveIO && !opts.AssumeYes {
		reader := bufio.NewReader(os.Stdin)
		ok, err := promptYesNo(reader, os.Stdout, "Apply these changes?", true)
		if err != nil {
			return fmt.Errorf("prompt confirmation: %w", err)
		}
		if !ok {
			fmt.Println("[claw] cancelled")
			return nil
		}
	}

	if err := os.MkdirAll(filepath.Dir(clawfilePath), 0o755); err != nil {
		return fmt.Errorf("create clawfile directory: %w", err)
	}
	if resolved.SkillsDirRelPath != "" {
		if err := os.MkdirAll(filepath.Join(podDir, filepath.FromSlash(resolved.SkillsDirRelPath)), 0o755); err != nil {
			return fmt.Errorf("create agent skills directory: %w", err)
		}
	}
	if err := os.WriteFile(clawfilePath, []byte(renderAgentClawfile(resolved.ClawType, resolved.Model, resolved.Cllama, resolved.Platform, resolved.AgentDirective)), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", clawfilePath, err)
	}
	if resolved.CreateAgentFile {
		if err := os.WriteFile(agentFilePath, []byte(defaultAgentContract()), 0o644); err != nil {
			return fmt.Errorf("write %s: %w", agentFilePath, err)
		}
	}
	if resolved.CreateSharedFile {
		srcHost := filepath.Join(podDir, filepath.FromSlash(strings.TrimPrefix(normalizeContractPath(resolved.SharedSourcePath), "./")))
		srcData, err := os.ReadFile(srcHost)
		if err != nil {
			return fmt.Errorf("read shared profile source %s: %w", resolved.SharedSourcePath, err)
		}
		sharedHost := filepath.Join(podDir, filepath.FromSlash(strings.TrimPrefix(resolved.SharedFilePath, "./")))
		if err := os.MkdirAll(filepath.Dir(sharedHost), 0o755); err != nil {
			return fmt.Errorf("create shared profile directory: %w", err)
		}
		if err := os.WriteFile(sharedHost, srcData, 0o644); err != nil {
			return fmt.Errorf("write shared profile %s: %w", sharedHost, err)
		}
	}
	if err := os.WriteFile(podFile, podOut, 0o644); err != nil {
		return fmt.Errorf("write pod file: %w", err)
	}
	if len(resolved.EnvExampleVars) > 0 {
		if _, err := appendMissingEnvExampleVars(envExamplePath, resolved.EnvExampleVars); err != nil {
			return err
		}
	}

	fmt.Printf("[claw] created %s\n", resolved.ClawfileRelPath)
	if resolved.CreateAgentFile {
		fmt.Printf("[claw] created %s\n", resolved.AgentFileRelPath)
	}
	if resolved.CreateSharedFile {
		fmt.Printf("[claw] created %s\n", strings.TrimPrefix(resolved.SharedFilePath, "./"))
	}
	fmt.Printf("[claw] updated %s\n", filepath.Base(podFile))
	if len(resolved.EnvExampleVars) > 0 {
		fmt.Println("[claw] updated .env.example")
	}
	return nil
}

func buildAgentAddContext(rootDir string, parsed *pod.Pod, root *yaml.Node) *agentAddContext {
	ctx := &agentAddContext{
		RootDir:           rootDir,
		PodName:           parsed.Name,
		Layout:            agentLayoutCanonical,
		CllamaEnv:         map[string]string{},
		ExistingContracts: make([]string, 0),
		ExistingPrefixes:  map[string][]string{},
		ExistingEnvKeys:   map[string]struct{}{},
	}

	ctx.Layout = detectAgentLayout(rootDir, root, parsed)

	volumeNames := make([]string, 0)
	if volumesNode := findMapValue(root, "volumes"); volumesNode != nil && volumesNode.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(volumesNode.Content); i += 2 {
			volumeNames = append(volumeNames, strings.TrimSpace(volumesNode.Content[i].Value))
		}
	}
	ctx.ExistingVolumes = uniqueSorted(volumeNames)

	serviceNames := make([]string, 0, len(parsed.Services))
	for name := range parsed.Services {
		serviceNames = append(serviceNames, name)
	}
	sort.Strings(serviceNames)

	for _, name := range serviceNames {
		svc := parsed.Services[name]
		if svc == nil || svc.Claw == nil {
			continue
		}
		prefix := envPrefixFromName(name)
		ctx.ExistingPrefixes[prefix] = append(ctx.ExistingPrefixes[prefix], name)
		if svc.Claw.Agent != "" {
			ctx.ExistingContracts = append(ctx.ExistingContracts, normalizeContractPath(svc.Claw.Agent))
		}
		if !ctx.HasCllama && len(svc.Claw.Cllama) > 0 {
			ctx.HasCllama = true
			ctx.CllamaValue = strings.TrimSpace(svc.Claw.Cllama[0])
			for k, v := range svc.Claw.CllamaEnv {
				ctx.CllamaEnv[k] = v
			}
		}
		if ctx.PreferredPlatform == "" && len(svc.Claw.Handles) > 0 {
			platforms := make([]string, 0, len(svc.Claw.Handles))
			for platform := range svc.Claw.Handles {
				platforms = append(platforms, platform)
			}
			sort.Strings(platforms)
			if len(platforms) > 0 {
				ctx.PreferredPlatform = platforms[0]
				if info := svc.Claw.Handles[ctx.PreferredPlatform]; info != nil {
					tpl := &platformTemplate{Username: info.Username}
					for _, g := range info.Guilds {
						tpl.Guilds = append(tpl.Guilds, platformGuild{
							ID:   g.ID,
							Name: g.Name,
						})
					}
					ctx.HandleTemplate = tpl
				}
			}
		}
	}

	ctx.ExistingContracts = uniqueSorted(ctx.ExistingContracts)
	if ctx.PreferredPlatform == "" {
		ctx.PreferredPlatform = defaultPlatform
	}
	if strings.TrimSpace(ctx.CllamaValue) == "" && ctx.HasCllama {
		ctx.CllamaValue = defaultCllamaType
	}
	if strings.TrimSpace(ctx.PodName) == "" {
		ctx.PodName = filepath.Base(rootDir)
	}
	if strings.TrimSpace(ctx.PodName) == "" || ctx.PodName == "." {
		ctx.PodName = "my-project"
	}
	if len(ctx.CllamaEnv) == 0 && ctx.HasCllama {
		ctx.CllamaEnv["OPENROUTER_API_KEY"] = "${OPENROUTER_API_KEY}"
	}

	envExamplePath := filepath.Join(rootDir, ".env.example")
	if data, err := os.ReadFile(envExamplePath); err == nil {
		for _, line := range splitLines(string(data)) {
			key := parseEnvExampleKey(line)
			if key == "" {
				continue
			}
			ctx.ExistingEnvKeys[key] = struct{}{}
		}
	}

	return ctx
}

func detectAgentLayout(rootDir string, root *yaml.Node, parsed *pod.Pod) string {
	canonicalHints := 0
	flatHints := 0

	servicesNode := findMapValue(root, "services")

	for serviceName, svc := range parsed.Services {
		if svc == nil || svc.Claw == nil {
			continue
		}

		agentPath := normalizeContractPath(svc.Claw.Agent)
		switch {
		case strings.HasPrefix(agentPath, "./agents/"):
			canonicalHints++
		case isRootLevelPath(agentPath):
			flatHints++
		}

		if servicesNode == nil || servicesNode.Kind != yaml.MappingNode {
			continue
		}
		serviceNode := findMapValue(servicesNode, serviceName)
		contextValue, dockerfileValue := readServiceBuild(serviceNode)
		contextNorm := normalizeBuildContextHint(contextValue)
		switch {
		case strings.HasPrefix(contextNorm, "agents/"):
			canonicalHints++
		case contextNorm == ".":
			flatHints++
		}
		if contextNorm == "." && strings.HasPrefix(strings.ToLower(strings.TrimSpace(dockerfileValue)), "clawfile") {
			flatHints++
		}
	}

	if canonicalHints == 0 && flatHints == 0 {
		if info, err := os.Stat(filepath.Join(rootDir, "agents")); err == nil && info.IsDir() {
			return agentLayoutCanonical
		}
		return agentLayoutFlat
	}
	if flatHints > canonicalHints {
		return agentLayoutFlat
	}
	return agentLayoutCanonical
}

func readServiceBuild(serviceNode *yaml.Node) (contextValue string, dockerfileValue string) {
	if serviceNode == nil || serviceNode.Kind != yaml.MappingNode {
		return "", ""
	}
	buildNode := findMapValue(serviceNode, "build")
	if buildNode == nil {
		return "", ""
	}
	switch buildNode.Kind {
	case yaml.ScalarNode:
		return strings.TrimSpace(buildNode.Value), ""
	case yaml.MappingNode:
		contextNode := findMapValue(buildNode, "context")
		if contextNode != nil && contextNode.Kind == yaml.ScalarNode {
			contextValue = strings.TrimSpace(contextNode.Value)
		}
		dockerfileNode := findMapValue(buildNode, "dockerfile")
		if dockerfileNode != nil && dockerfileNode.Kind == yaml.ScalarNode {
			dockerfileValue = strings.TrimSpace(dockerfileNode.Value)
		}
	}
	return contextValue, dockerfileValue
}

func normalizeBuildContextHint(value string) string {
	v := strings.TrimSpace(filepath.ToSlash(value))
	v = strings.TrimPrefix(v, "./")
	if v == "" {
		return "."
	}
	return v
}

func isRootLevelPath(path string) bool {
	p := strings.TrimPrefix(normalizeContractPath(path), "./")
	return p != "" && !strings.Contains(p, "/")
}

func defaultClawfileRelPath(layout, agentName string) string {
	if layout == agentLayoutFlat {
		return "Clawfile." + agentName
	}
	return filepath.ToSlash(filepath.Join("agents", agentName, "Clawfile"))
}

func defaultAgentContractRelPath(layout, agentName string) string {
	if layout == agentLayoutFlat {
		return "AGENTS-" + agentName + ".md"
	}
	return filepath.ToSlash(filepath.Join("agents", agentName, "AGENTS.md"))
}

func defaultAgentDirective(layout, agentName string) string {
	if layout == agentLayoutFlat {
		return "AGENTS-" + agentName + ".md"
	}
	return "AGENTS.md"
}

func defaultBuildContext(layout, agentName string) string {
	if layout == agentLayoutFlat {
		return "."
	}
	return normalizeContractPath(filepath.ToSlash(filepath.Join("agents", agentName)))
}

func defaultBuildDockerfile(layout, agentName string) string {
	if layout == agentLayoutFlat {
		return "Clawfile." + agentName
	}
	return ""
}

func resolveAgentLayout(detected, requested string) (string, error) {
	req := strings.TrimSpace(strings.ToLower(requested))
	switch req {
	case "", "auto":
		if detected == agentLayoutFlat || detected == agentLayoutCanonical {
			return detected, nil
		}
		return agentLayoutCanonical, nil
	case agentLayoutCanonical, agentLayoutFlat:
		return req, nil
	default:
		return "", fmt.Errorf("invalid --layout %q (allowed: auto, canonical, flat)", requested)
	}
}

func resolveAgentAddConfig(ctx *agentAddContext, opts agentAddOptions) (*agentAddResolvedConfig, error) {
	promptMode := opts.InteractiveIO && !opts.AssumeYes

	cfg := &agentAddResolvedConfig{
		AgentName:      strings.TrimSpace(opts.AgentName),
		ClawType:       strings.TrimSpace(opts.ClawType),
		Model:          strings.TrimSpace(opts.Model),
		Cllama:         strings.TrimSpace(opts.Cllama),
		Platform:       strings.TrimSpace(opts.Platform),
		ContractPath:   strings.TrimSpace(opts.ContractPath),
		VolumeModes:    make(map[string]string),
		CllamaEnv:      make(map[string]string),
		HandleTemplate: ctx.HandleTemplate,
	}

	layout, err := resolveAgentLayout(ctx.Layout, opts.Layout)
	if err != nil {
		return nil, err
	}
	cfg.ClawfileRelPath = defaultClawfileRelPath(layout, cfg.AgentName)
	cfg.AgentFileRelPath = defaultAgentContractRelPath(layout, cfg.AgentName)
	cfg.AgentDirective = defaultAgentDirective(layout, cfg.AgentName)
	if layout == agentLayoutCanonical {
		cfg.SkillsDirRelPath = filepath.ToSlash(filepath.Join("agents", cfg.AgentName, "skills"))
	}

	reader := bufio.NewReader(os.Stdin)

	if cfg.ClawType == "" {
		if promptMode {
			value, err := promptSelect(reader, os.Stdout, "Claw type", []string{"openclaw", "nanoclaw", "microclaw", "nullclaw", "nanobot", "picoclaw", "generic"}, 0)
			if err != nil {
				return nil, err
			}
			cfg.ClawType = value
		} else {
			cfg.ClawType = defaultClawType
		}
	}
	if cfg.Model == "" {
		if promptMode {
			value, err := promptText(reader, os.Stdout, "Model (provider/model)", defaultModel)
			if err != nil {
				return nil, err
			}
			cfg.Model = value
		} else {
			cfg.Model = defaultModel
		}
	}
	if cfg.Cllama == "" {
		if promptMode {
			defaultIdx := 1
			if ctx.HasCllama {
				defaultIdx = 0
			}
			value, err := promptSelect(reader, os.Stdout, "Use cllama proxy?", []string{"yes", "no"}, defaultIdx)
			if err != nil {
				return nil, err
			}
			cfg.Cllama = value
		} else if ctx.HasCllama {
			cfg.Cllama = "inherit"
		} else {
			cfg.Cllama = "no"
		}
	}
	if cfg.Platform == "" {
		if promptMode {
			choices := []string{"discord", "slack", "telegram", "none"}
			defaultIdx := 0
			for i, c := range choices {
				if c == ctx.PreferredPlatform {
					defaultIdx = i
					break
				}
			}
			value, err := promptSelect(reader, os.Stdout, "Platform", choices, defaultIdx)
			if err != nil {
				return nil, err
			}
			cfg.Platform = value
		} else {
			cfg.Platform = ctx.PreferredPlatform
		}
	}

	clawType, err := parseClawType(cfg.ClawType)
	if err != nil {
		return nil, err
	}
	cfg.ClawType = clawType

	platform, err := parsePlatform(cfg.Platform)
	if err != nil {
		return nil, err
	}
	cfg.Platform = platform

	cllama, err := parseCllamaChoice(cfg.Cllama, defaultCllamaType)
	if err != nil {
		return nil, err
	}
	if cllama == "inherit" {
		if ctx.HasCllama {
			cllama = ctx.CllamaValue
		} else {
			cllama = ""
		}
	}
	cfg.Cllama = cllama
	if cfg.Cllama != "" {
		for k, v := range ctx.CllamaEnv {
			cfg.CllamaEnv[k] = v
		}
		if len(cfg.CllamaEnv) == 0 {
			cfg.CllamaEnv["OPENROUTER_API_KEY"] = "${OPENROUTER_API_KEY}"
		}
	}

	if strings.TrimSpace(cfg.Model) == "" {
		return nil, fmt.Errorf("model is required")
	}

	if strings.TrimSpace(cfg.ContractPath) != "" {
		if filepath.IsAbs(cfg.ContractPath) {
			return nil, fmt.Errorf("absolute contract paths are not supported for --contract; use a path relative to pod root")
		}
		cfg.ContractPath = normalizeContractPath(cfg.ContractPath)
		if _, err := runtime.ResolveContract(ctx.RootDir, cfg.ContractPath); err != nil {
			return nil, err
		}
	} else {
		if promptMode {
			contractMode := "create new"
			modes := []string{"create new"}
			if len(ctx.ExistingContracts) > 0 {
				modes = append(modes, "reuse existing", "create shared profile")
			}
			value, err := promptSelect(reader, os.Stdout, "AGENTS.md", modes, 0)
			if err != nil {
				return nil, err
			}
			contractMode = value

			switch contractMode {
			case "create new":
				cfg.ContractPath = normalizeContractPath(cfg.AgentFileRelPath)
				cfg.CreateAgentFile = true
			case "reuse existing":
				selected, err := promptSelect(reader, os.Stdout, "Reuse contract", ctx.ExistingContracts, 0)
				if err != nil {
					return nil, err
				}
				cfg.ContractPath = normalizeContractPath(selected)
			case "create shared profile":
				source, err := promptSelect(reader, os.Stdout, "Shared profile source", ctx.ExistingContracts, 0)
				if err != nil {
					return nil, err
				}
				profileDefault := opts.SharedProfile
				if strings.TrimSpace(profileDefault) == "" {
					profileDefault = "shared-profile"
				}
				profileName, err := promptText(reader, os.Stdout, "Shared profile name", profileDefault)
				if err != nil {
					return nil, err
				}
				if err := validateEntityName("shared profile", profileName); err != nil {
					return nil, err
				}
				cfg.SharedFilePath = normalizeContractPath(filepath.ToSlash(filepath.Join("shared", profileName, "AGENTS.md")))
				cfg.SharedSourcePath = normalizeContractPath(source)
				cfg.ContractPath = cfg.SharedFilePath
				cfg.CreateSharedFile = true
				cfg.CreateAgentFile = true
				rewire, err := promptYesNo(reader, os.Stdout, "Rewire existing agents using source contract?", false)
				if err != nil {
					return nil, err
				}
				cfg.RewireExisting = rewire
			default:
				return nil, fmt.Errorf("unsupported contract mode %q", contractMode)
			}
		} else {
			cfg.ContractPath = normalizeContractPath(cfg.AgentFileRelPath)
			cfg.CreateAgentFile = true
		}
	}

	if opts.SharedProfile != "" && cfg.CreateSharedFile {
		if err := validateEntityName("shared profile", opts.SharedProfile); err != nil {
			return nil, err
		}
	}

	if cfg.ContractPath == "" {
		cfg.ContractPath = normalizeContractPath(cfg.AgentFileRelPath)
		cfg.CreateAgentFile = true
	}

	for _, spec := range opts.VolumeSpecs {
		name, mode, err := parseVolumeSpec(spec)
		if err != nil {
			return nil, err
		}
		if name == "" {
			continue
		}
		cfg.VolumeModes[name] = mode
	}

	for _, name := range ctx.ExistingVolumes {
		if _, ok := cfg.VolumeModes[name]; ok {
			continue
		}
		if promptMode {
			value, err := promptSelect(reader, os.Stdout, "Volume "+name+" access", []string{"none", "read-only", "read-write"}, 0)
			if err != nil {
				return nil, err
			}
			if value != "none" {
				cfg.VolumeModes[name] = value
			}
		}
	}

	prefix := envPrefixFromName(cfg.AgentName)
	cfg.EnvExampleVars = make([]string, 0, 2)
	if cfg.Platform != "none" {
		tokenKey := platformTokenKey(cfg.Platform)
		idKey := platformIDKey(cfg.Platform)
		if tokenKey != "" && idKey != "" {
			cfg.EnvExampleVars = append(cfg.EnvExampleVars, prefix+"_"+tokenKey, prefix+"_"+idKey)
		}
	}
	if owners, ok := ctx.ExistingPrefixes[prefix]; ok && len(owners) > 0 {
		cfg.Warnings = append(cfg.Warnings, fmt.Sprintf("env prefix %s for agent %q matches existing service(s): %s", prefix, cfg.AgentName, strings.Join(uniqueSorted(owners), ", ")))
	}
	for _, key := range cfg.EnvExampleVars {
		if _, ok := ctx.ExistingEnvKeys[key]; ok {
			cfg.Warnings = append(cfg.Warnings, fmt.Sprintf(".env.example already contains %s; %s will reuse that value", key, cfg.AgentName))
		}
	}

	cfg.Image = fmt.Sprintf("%s-%s:latest", ctx.PodName, cfg.AgentName)
	cfg.BuildContext = defaultBuildContext(layout, cfg.AgentName)
	cfg.BuildDockerfile = defaultBuildDockerfile(layout, cfg.AgentName)
	cfg.EnvExampleVars = uniqueSorted(cfg.EnvExampleVars)
	cfg.Warnings = uniqueSorted(cfg.Warnings)
	return cfg, nil
}

func serviceNodeFromConfig(cfg *agentAddResolvedConfig) (*yaml.Node, error) {
	xclaw := scaffoldXClaw{
		Agent: cfg.ContractPath,
	}
	if cfg.Cllama != "" {
		xclaw.Cllama = cfg.Cllama
		if len(cfg.CllamaEnv) > 0 {
			xclaw.CllamaEnv = cfg.CllamaEnv
		}
	}

	env := make(map[string]string)
	if cfg.Platform != "none" {
		prefix := envPrefixFromName(cfg.AgentName)
		tokenKey := platformTokenKey(cfg.Platform)
		idKey := platformIDKey(cfg.Platform)
		if tokenKey != "" && idKey != "" {
			env[tokenKey] = fmt.Sprintf("${%s_%s}", prefix, tokenKey)
			env[idKey] = fmt.Sprintf("${%s_%s}", prefix, idKey)
			handle := scaffoldHandle{
				ID:       fmt.Sprintf("${%s_%s}", prefix, idKey),
				Username: cfg.AgentName,
			}
			if cfg.HandleTemplate != nil {
				for _, guild := range cfg.HandleTemplate.Guilds {
					handle.Guilds = append(handle.Guilds, scaffoldGuild{
						ID:   guild.ID,
						Name: guild.Name,
					})
				}
			}
			if cfg.Platform == "discord" && len(handle.Guilds) == 0 {
				handle.Guilds = append(handle.Guilds, scaffoldGuild{ID: "${DISCORD_GUILD_ID}"})
			}
			xclaw.Handles = map[string]scaffoldHandle{
				cfg.Platform: handle,
			}
		}
	}

	if len(cfg.VolumeModes) > 0 {
		volumeNames := make([]string, 0, len(cfg.VolumeModes))
		for name := range cfg.VolumeModes {
			volumeNames = append(volumeNames, name)
		}
		sort.Strings(volumeNames)
		xclaw.Surfaces = make([]string, 0, len(volumeNames))
		for _, name := range volumeNames {
			xclaw.Surfaces = append(xclaw.Surfaces, fmt.Sprintf("volume://%s %s", name, cfg.VolumeModes[name]))
		}
	}

	service := scaffoldService{
		Image: cfg.Image,
		Build: scaffoldBuild{
			Context:    cfg.BuildContext,
			Dockerfile: cfg.BuildDockerfile,
		},
		XClaw: xclaw,
	}
	if len(env) > 0 {
		service.Environment = env
	}

	var doc yaml.Node
	raw, err := yaml.Marshal(service)
	if err != nil {
		return nil, fmt.Errorf("marshal service node: %w", err)
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("unmarshal service node: %w", err)
	}
	if len(doc.Content) == 0 {
		return nil, fmt.Errorf("marshal produced empty service node")
	}
	return doc.Content[0], nil
}

func podRootMap(doc *yaml.Node) (*yaml.Node, error) {
	if doc == nil || len(doc.Content) == 0 {
		return nil, fmt.Errorf("invalid pod YAML: missing document root")
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("invalid pod YAML: top-level document must be a mapping")
	}
	return root, nil
}

func ensureMappingNodeValue(root *yaml.Node, key string) *yaml.Node {
	if existing := findMapValue(root, key); existing != nil {
		return existing
	}
	value := &yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{}}
	setMapValue(root, key, value)
	return value
}

func findMapValue(node *yaml.Node, key string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	return nil
}

func setMapValue(node *yaml.Node, key string, value *yaml.Node) {
	if node == nil || node.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			node.Content[i+1] = value
			return
		}
	}
	node.Content = append(node.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		value,
	)
}

func marshalYAMLDocument(doc *yaml.Node) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return nil, fmt.Errorf("encode YAML: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("close YAML encoder: %w", err)
	}
	return buf.Bytes(), nil
}

func rewireContracts(servicesNode *yaml.Node, sourcePath, targetPath string) int {
	if servicesNode == nil || servicesNode.Kind != yaml.MappingNode {
		return 0
	}
	rewired := 0
	for i := 0; i+1 < len(servicesNode.Content); i += 2 {
		serviceNode := servicesNode.Content[i+1]
		if serviceNode.Kind != yaml.MappingNode {
			continue
		}
		xclaw := findMapValue(serviceNode, "x-claw")
		if xclaw == nil || xclaw.Kind != yaml.MappingNode {
			continue
		}
		agentNode := findMapValue(xclaw, "agent")
		if agentNode == nil || agentNode.Kind != yaml.ScalarNode {
			continue
		}
		if normalizeContractPath(agentNode.Value) == sourcePath {
			agentNode.Value = targetPath
			rewired++
		}
	}
	return rewired
}

func init() {
	agentAddCmd.Flags().StringVar(&agentNameFlag, "agent", "", "Agent name (service and directory name)")
	agentAddCmd.Flags().StringVar(&agentTypeFlag, "type", "", "Claw type (openclaw, nanoclaw, microclaw, nullclaw, nanobot, picoclaw, generic)")
	agentAddCmd.Flags().StringVar(&agentModelFlag, "model", "", "Primary model (provider/model)")
	agentAddCmd.Flags().StringVar(&agentCllamaFlag, "cllama", "", "Use cllama proxy (yes/no/inherit)")
	agentAddCmd.Flags().StringVar(&agentPlatformFlag, "platform", "", "Platform handle (discord, slack, telegram, none)")
	agentAddCmd.Flags().StringVar(&agentContractFlag, "contract", "", "Reuse an existing contract path (relative to pod root)")
	agentAddCmd.Flags().StringSliceVar(&agentVolumesFlag, "volume", nil, "Volume access spec (<name>:<read-only|read-write>)")
	agentAddCmd.Flags().StringVar(&agentSharedProfile, "shared-profile", "", "Default shared profile name when creating shared profile interactively")
	agentAddCmd.Flags().StringVar(&agentLayoutFlag, "layout", "auto", "Scaffold layout style (auto, canonical, flat)")
	agentAddCmd.Flags().BoolVar(&agentDryRunFlag, "dry-run", false, "Print planned changes without writing files")
	agentAddCmd.Flags().BoolVar(&agentYesFlag, "yes", false, "Apply without interactive confirmation")

	agentCmd.AddCommand(agentAddCmd)
	rootCmd.AddCommand(agentCmd)
}
