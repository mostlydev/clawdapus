package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/mostlydev/clawdapus/internal/describe"
	"github.com/mostlydev/clawdapus/internal/mcpdiscover"
	"github.com/mostlydev/clawdapus/internal/pod"
)

const discoveredDescriptorDir = ".claw-discovered"

var discoverTimeout = 45 * time.Second

var runDiscoveryDockerCommand = runDiscoveryDockerCommandDefault

var discoverCmd = &cobra.Command{
	Use:   "discover [service...]",
	Short: "Discover stdio MCP sidecar tools and write descriptor snapshots",
	Args:  cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		podFile := composePodFile
		if podFile == "" {
			podFile = "claw-pod.yml"
		}
		p, podDir, err := loadPodDefinition(podFile)
		if err != nil {
			return err
		}
		if err := resolveRuntimePlaceholders(podDir, p); err != nil {
			return fmt.Errorf("resolve x-claw runtime placeholders: %w", err)
		}
		ctx := cmd.Context()
		if ctx == nil {
			ctx = context.Background()
		}
		results, err := discoverMCPStdioServices(ctx, podDir, p, args, discoverSelectionAll)
		if err != nil {
			return err
		}
		for _, result := range results {
			fmt.Printf("[claw] discovered %s: wrote %s (%d tool(s))\n", result.Service, result.Path, result.ToolCount)
		}
		return nil
	},
}

type discoverySelection int

const (
	discoverSelectionAll discoverySelection = iota
	discoverSelectionMissingOrStale
)

type discoveryResult struct {
	Service   string
	Path      string
	ToolCount int
	Skipped   bool
}

func discoverMCPStdioServices(ctx context.Context, podDir string, p *pod.Pod, requested []string, selection discoverySelection) ([]discoveryResult, error) {
	names, err := selectMCPStdioServices(p, requested)
	if err != nil {
		return nil, err
	}
	results := make([]discoveryResult, 0, len(names))
	for _, name := range names {
		svc := p.Services[name]
		if selection == discoverSelectionMissingOrStale {
			if descriptorPath := explicitDescribeFile(podDir, svc); descriptorPath != "" {
				results = append(results, discoveryResult{Service: name, Path: descriptorPath, Skipped: true})
				continue
			}
			stale, err := discoveredSnapshotMissingOrStale(podDir, name, svc)
			if err != nil {
				return nil, err
			}
			if !stale {
				results = append(results, discoveryResult{Service: name, Path: discoveredSnapshotPath(podDir, name), Skipped: true})
				continue
			}
		}
		result, err := discoverMCPStdioService(ctx, podDir, name, svc)
		if err != nil {
			return nil, fmt.Errorf("service %q: %w", name, err)
		}
		results = append(results, result)
	}
	return results, nil
}

func selectMCPStdioServices(p *pod.Pod, requested []string) ([]string, error) {
	if p == nil {
		return nil, fmt.Errorf("pod is required")
	}
	if len(requested) > 0 {
		names := make([]string, 0, len(requested))
		seen := make(map[string]struct{}, len(requested))
		for _, raw := range requested {
			name := strings.TrimSpace(raw)
			if name == "" {
				continue
			}
			if _, exists := seen[name]; exists {
				continue
			}
			seen[name] = struct{}{}
			svc := p.Services[name]
			if svc == nil {
				return nil, fmt.Errorf("service %q not found in pod", name)
			}
			if !svc.IsMCPStdioSidecar() {
				return nil, fmt.Errorf("service %q is not an x-claw.mcp-stdio sidecar", name)
			}
			names = append(names, name)
		}
		sort.Strings(names)
		if len(names) == 0 {
			return nil, fmt.Errorf("no services selected")
		}
		return names, nil
	}

	names := make([]string, 0)
	for name, svc := range p.Services {
		if svc != nil && svc.IsMCPStdioSidecar() {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		return nil, fmt.Errorf("no x-claw.mcp-stdio services found in pod")
	}
	return names, nil
}

func podHasMCPStdioServices(p *pod.Pod) bool {
	if p == nil {
		return false
	}
	for _, svc := range p.Services {
		if svc != nil && svc.IsMCPStdioSidecar() {
			return true
		}
	}
	return false
}

func discoverMCPStdioService(ctx context.Context, podDir, serviceName string, svc *pod.Service) (discoveryResult, error) {
	if svc == nil || svc.Claw == nil || svc.Claw.MCPStdio == nil {
		return discoveryResult{}, fmt.Errorf("not an x-claw.mcp-stdio sidecar")
	}
	imageRef := strings.TrimSpace(svc.Image)
	if imageRef == "" {
		return discoveryResult{}, fmt.Errorf("stdio wrapper service must declare image")
	}

	env, mcpPath, err := discoveryContainerEnv(podDir, svc)
	if err != nil {
		return discoveryResult{}, err
	}
	volumes, err := discoveryVolumeArgs(podDir, svc.Compose["volumes"])
	if err != nil {
		return discoveryResult{}, err
	}
	port, err := freeTCPPort()
	if err != nil {
		return discoveryResult{}, err
	}
	token, err := randomDiscoverToken()
	if err != nil {
		return discoveryResult{}, err
	}
	env["CLAW_MCP_STDIO_AUTH_TOKEN"] = token

	containerName := "claw-discover-" + sanitizeSnapshotName(serviceName) + "-" + token[:8]
	runArgs := []string{
		"run", "--rm", "-d",
		"--name", containerName,
		"-p", fmt.Sprintf("127.0.0.1:%d:8080", port),
	}
	for _, key := range sortedDiscoveryKeys(env) {
		runArgs = append(runArgs, "-e", key+"="+env[key])
	}
	for _, volume := range volumes {
		runArgs = append(runArgs, "-v", volume)
	}
	runArgs = append(runArgs, imageRef)

	containerID, err := runDiscoveryDockerCommand(ctx, runArgs...)
	if err != nil {
		return discoveryResult{}, fmt.Errorf("start discovery container: %w", err)
	}
	containerID = strings.TrimSpace(containerID)
	defer func() {
		_, _ = runDiscoveryDockerCommand(context.Background(), "rm", "-f", containerName)
	}()

	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	httpClient := &httpClientWithTimeout{timeout: 5 * time.Second}
	if err := mcpdiscover.WaitHealth(ctx, httpClient.Client(), baseURL, discoverTimeout); err != nil {
		return discoveryResult{}, fmt.Errorf("%w\ncontainer logs:\n%s", err, discoveryContainerLogs(ctx, containerName))
	}
	discovered, err := (mcpdiscover.Client{
		HTTPClient: httpClient.Client(),
		BaseURL:    baseURL,
		Path:       mcpPath,
		AuthToken:  token,
	}).Discover(ctx)
	if err != nil {
		return discoveryResult{}, fmt.Errorf("%w\ncontainer logs:\n%s", err, discoveryContainerLogs(ctx, containerName))
	}

	imageID, digest := discoveryImageIdentity(ctx, imageRef)
	if imageID == "" {
		imageID = containerID
	}
	descriptor, err := mcpdiscover.Descriptor(fmt.Sprintf("%s MCP stdio tools.", serviceName), mcpPath, discovered, &describe.DiscoveryMetadata{
		Command:            svc.Claw.MCPStdio.Command,
		Args:               append([]string(nil), svc.Claw.MCPStdio.Args...),
		WrapperImage:       imageRef,
		WrapperImageDigest: digest,
		WrapperImageID:     imageID,
		DiscoveredAt:       time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		return discoveryResult{}, fmt.Errorf("build descriptor snapshot: %w", err)
	}

	snapshotPath := discoveredSnapshotPath(podDir, serviceName)
	if err := writeDescriptorSnapshot(snapshotPath, descriptor); err != nil {
		return discoveryResult{}, err
	}
	return discoveryResult{Service: serviceName, Path: snapshotPath, ToolCount: len(descriptor.Tools)}, nil
}

type httpClientWithTimeout struct {
	timeout time.Duration
}

func (h *httpClientWithTimeout) Client() *http.Client {
	timeout := h.timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	return &http.Client{Timeout: timeout}
}

func discoveryContainerEnv(podDir string, svc *pod.Service) (map[string]string, string, error) {
	runtimeEnv, err := loadRuntimeEnv(podDir)
	if err != nil {
		return nil, "", err
	}
	env := make(map[string]string, len(svc.Environment)+8)
	for key, value := range svc.Environment {
		expanded, err := expandRuntimeValue(value, runtimeEnv)
		if err != nil {
			return nil, "", fmt.Errorf("environment %s: %w", key, err)
		}
		env[key] = expanded
	}

	argsJSON, err := json.Marshal(svc.Claw.MCPStdio.Args)
	if err != nil {
		return nil, "", fmt.Errorf("encode mcp-stdio args: %w", err)
	}
	env["CLAW_MCP_STDIO_COMMAND"] = svc.Claw.MCPStdio.Command
	env["CLAW_MCP_STDIO_ARGS"] = string(argsJSON)
	env["CLAW_MCP_STDIO_ADDR"] = ":8080"

	mcpPath := strings.TrimSpace(env["CLAW_MCP_STDIO_PATH"])
	if mcpPath == "" {
		mcpPath = "/mcp"
		env["CLAW_MCP_STDIO_PATH"] = mcpPath
	}
	if !strings.HasPrefix(mcpPath, "/") {
		return nil, "", fmt.Errorf("CLAW_MCP_STDIO_PATH must start with '/'")
	}
	return env, mcpPath, nil
}

func discoveryVolumeArgs(podDir string, raw interface{}) ([]string, error) {
	if raw == nil {
		return nil, nil
	}
	runtimeEnv, err := loadRuntimeEnv(podDir)
	if err != nil {
		return nil, err
	}
	items, err := composeList(raw)
	if err != nil {
		return nil, fmt.Errorf("volumes: %w", err)
	}
	out := make([]string, 0, len(items))
	for i, item := range items {
		switch v := item.(type) {
		case string:
			expanded, err := expandRuntimeValue(v, runtimeEnv)
			if err != nil {
				return nil, fmt.Errorf("volumes[%d]: %w", i, err)
			}
			out = append(out, normalizeVolumeString(podDir, expanded))
		case map[string]interface{}:
			volume, err := normalizeVolumeMap(podDir, v, runtimeEnv)
			if err != nil {
				return nil, fmt.Errorf("volumes[%d]: %w", i, err)
			}
			out = append(out, volume)
		case map[interface{}]interface{}:
			m := make(map[string]interface{}, len(v))
			for key, value := range v {
				m[fmt.Sprint(key)] = value
			}
			volume, err := normalizeVolumeMap(podDir, m, runtimeEnv)
			if err != nil {
				return nil, fmt.Errorf("volumes[%d]: %w", i, err)
			}
			out = append(out, volume)
		default:
			return nil, fmt.Errorf("volumes[%d]: unsupported volume value %T", i, item)
		}
	}
	return out, nil
}

func composeList(raw interface{}) ([]interface{}, error) {
	switch v := raw.(type) {
	case []interface{}:
		return v, nil
	case []string:
		out := make([]interface{}, 0, len(v))
		for _, item := range v {
			out = append(out, item)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("expected list, got %T", raw)
	}
}

func normalizeVolumeString(podDir, spec string) string {
	spec = strings.TrimSpace(spec)
	parts := strings.SplitN(spec, ":", 3)
	if len(parts) < 2 {
		return spec
	}
	source := strings.TrimSpace(parts[0])
	if shouldResolveVolumeSource(source) {
		source = filepath.Join(podDir, source)
	}
	parts[0] = filepath.Clean(source)
	return strings.Join(parts, ":")
}

func normalizeVolumeMap(podDir string, raw map[string]interface{}, runtimeEnv map[string]string) (string, error) {
	volumeType := strings.ToLower(composeMapString(raw, "type"))
	if volumeType == "" {
		volumeType = "volume"
	}
	if volumeType != "bind" && volumeType != "volume" {
		return "", fmt.Errorf("unsupported volume type %q for discovery", volumeType)
	}
	source, err := expandRuntimeValue(composeMapString(raw, "source"), runtimeEnv)
	if err != nil {
		return "", fmt.Errorf("source: %w", err)
	}
	if source == "" {
		source, err = expandRuntimeValue(composeMapString(raw, "src"), runtimeEnv)
		if err != nil {
			return "", fmt.Errorf("src: %w", err)
		}
	}
	target, err := expandRuntimeValue(composeMapString(raw, "target"), runtimeEnv)
	if err != nil {
		return "", fmt.Errorf("target: %w", err)
	}
	if target == "" {
		target, err = expandRuntimeValue(composeMapString(raw, "dst"), runtimeEnv)
		if err != nil {
			return "", fmt.Errorf("dst: %w", err)
		}
	}
	if target == "" {
		target, err = expandRuntimeValue(composeMapString(raw, "destination"), runtimeEnv)
		if err != nil {
			return "", fmt.Errorf("destination: %w", err)
		}
	}
	if source == "" || target == "" {
		return "", fmt.Errorf("source and target are required")
	}
	if volumeType == "bind" && !filepath.IsAbs(source) {
		source = filepath.Join(podDir, source)
	}
	mode := "rw"
	if readOnly, ok := raw["read_only"].(bool); ok && readOnly {
		mode = "ro"
	}
	if rawMode := composeMapString(raw, "mode"); rawMode != "" {
		mode = rawMode
	}
	return filepath.Clean(source) + ":" + target + ":" + mode, nil
}

func composeMapString(raw map[string]interface{}, key string) string {
	value, ok := raw[key]
	if !ok || value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func shouldResolveVolumeSource(source string) bool {
	if source == "" || filepath.IsAbs(source) {
		return false
	}
	return strings.HasPrefix(source, ".") || strings.Contains(source, "/")
}

func discoveredSnapshotPath(podDir, serviceName string) string {
	return filepath.Join(podDir, discoveredDescriptorDir, sanitizeSnapshotName(serviceName)+".claw-describe.json")
}

func sanitizeSnapshotName(serviceName string) string {
	name := strings.TrimSpace(serviceName)
	replacer := strings.NewReplacer("/", "-", "\\", "-", ":", "-", string(os.PathSeparator), "-")
	name = replacer.Replace(name)
	if name == "" || name == "." || name == ".." {
		return "service"
	}
	return name
}

func discoveredSnapshotMissingOrStale(podDir, serviceName string, svc *pod.Service) (bool, error) {
	path := discoveredSnapshotPath(podDir, serviceName)
	descriptor, err := loadDescriptorFromFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return true, nil
		}
		return true, nil
	}
	return discoveryMetadataStale(descriptor, svc), nil
}

func discoveryMetadataStale(descriptor *describe.ServiceDescriptor, svc *pod.Service) bool {
	if descriptor == nil || descriptor.XClawDiscovery == nil || svc == nil || svc.Claw == nil || svc.Claw.MCPStdio == nil {
		return true
	}
	meta := descriptor.XClawDiscovery
	if meta.Command != strings.TrimSpace(svc.Claw.MCPStdio.Command) {
		return true
	}
	if strings.TrimSpace(meta.WrapperImage) != strings.TrimSpace(svc.Image) {
		return true
	}
	if len(meta.Args) != len(svc.Claw.MCPStdio.Args) {
		return true
	}
	for i := range meta.Args {
		if meta.Args[i] != svc.Claw.MCPStdio.Args[i] {
			return true
		}
	}
	return false
}

func warnDiscoveryDrift(serviceName string, descriptor *describe.ServiceDescriptor, svc *pod.Service) {
	if discoveryMetadataStale(descriptor, svc) {
		fmt.Printf("[claw] warning: service %q discovered descriptor is stale; run 'claw discover %s' to refresh %s\n",
			serviceName, serviceName, discoveredDescriptorDir)
	}
}

func writeDescriptorSnapshot(path string, descriptor *describe.ServiceDescriptor) error {
	if descriptor == nil {
		return fmt.Errorf("descriptor is required")
	}
	data, err := json.MarshalIndent(descriptor, "", "  ")
	if err != nil {
		return fmt.Errorf("encode descriptor snapshot: %w", err)
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create descriptor snapshot dir: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create descriptor snapshot temp file: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("write descriptor snapshot: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("close descriptor snapshot: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("install descriptor snapshot: %w", err)
	}
	return nil
}

func discoveryImageIdentity(ctx context.Context, imageRef string) (imageID, digest string) {
	imageID, _ = runDiscoveryDockerCommand(ctx, "image", "inspect", "--format", "{{.Id}}", imageRef)
	imageID = strings.TrimSpace(imageID)

	rawDigests, _ := runDiscoveryDockerCommand(ctx, "image", "inspect", "--format", "{{json .RepoDigests}}", imageRef)
	var digests []string
	if err := json.Unmarshal([]byte(strings.TrimSpace(rawDigests)), &digests); err == nil && len(digests) > 0 {
		digest = digests[0]
	}
	return imageID, digest
}

func discoveryContainerLogs(ctx context.Context, containerName string) string {
	out, err := runDiscoveryDockerCommand(ctx, "logs", "--tail", "80", containerName)
	if err != nil {
		return strings.TrimSpace(err.Error())
	}
	return strings.TrimSpace(out)
}

func freeTCPPort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	addr, ok := l.Addr().(*net.TCPAddr)
	if !ok {
		return 0, fmt.Errorf("unexpected listener address %T", l.Addr())
	}
	return addr.Port, nil
}

func randomDiscoverToken() (string, error) {
	var data [16]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(data[:]), nil
}

func sortedDiscoveryKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func runDiscoveryDockerCommandDefault(ctx context.Context, args ...string) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	cmd := exec.CommandContext(ctx, "docker", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("docker %s: %w\n%s", redactedDockerArgs(args), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func redactedDockerArgs(args []string) string {
	out := make([]string, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		out[i] = arg
		if (arg == "-e" || arg == "--env") && i+1 < len(args) {
			out[i+1] = redactEnvAssignment(args[i+1])
			i++
			continue
		}
		if strings.HasPrefix(arg, "--env=") {
			out[i] = "--env=" + redactEnvAssignment(strings.TrimPrefix(arg, "--env="))
		}
	}
	return strings.Join(out, " ")
}

func redactEnvAssignment(value string) string {
	key, _, ok := strings.Cut(value, "=")
	if !ok {
		return value
	}
	return key + "=<redacted>"
}

func init() {
	discoverCmd.Flags().DurationVar(&discoverTimeout, "timeout", discoverTimeout, "Maximum time to wait for each discovery sidecar")
	rootCmd.AddCommand(discoverCmd)
}
