package build

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/mostlydev/clawdapus/internal/clawfile"
	"github.com/mostlydev/clawdapus/internal/driver"
	_ "github.com/mostlydev/clawdapus/internal/driver/hermes"
	_ "github.com/mostlydev/clawdapus/internal/driver/nanobot"
	_ "github.com/mostlydev/clawdapus/internal/driver/openclaw" // register built-in drivers for build-time validation
	_ "github.com/mostlydev/clawdapus/internal/driver/picoclaw"
)

type MissingRunnerBaseError struct {
	Alias    string
	ImageRef string
}

func (e *MissingRunnerBaseError) Error() string {
	if e == nil {
		return "runner base image missing locally"
	}
	if strings.TrimSpace(e.ImageRef) != "" {
		return fmt.Sprintf("runner base image %q missing locally", e.ImageRef)
	}
	if strings.TrimSpace(e.Alias) != "" {
		return fmt.Sprintf("runner base image for %q missing locally", e.Alias)
	}
	return "runner base image missing locally"
}

type RunnerRefreshRequiredError struct {
	Alias    string
	ImageRef string
	Reason   string
}

func (e *RunnerRefreshRequiredError) Error() string {
	if e == nil {
		return "runner refresh required"
	}
	if strings.TrimSpace(e.Reason) != "" {
		return e.Reason
	}
	if strings.TrimSpace(e.ImageRef) != "" {
		return fmt.Sprintf("runner base image %q requires refresh", e.ImageRef)
	}
	if strings.TrimSpace(e.Alias) != "" {
		return fmt.Sprintf("runner base image for %q requires refresh", e.Alias)
	}
	return "runner refresh required"
}

type RefreshResult struct {
	DriverName  string
	Alias       string
	ImageRef    string
	BuiltRef    string
	VersionTag  string
	PreviousRef string
	PreviousTag string
	ImageID     string
	RecipeSHA   string
}

type localImageMetadata struct {
	ID       string   `json:"Id"`
	RepoTags []string `json:"RepoTags"`
}

func Generate(clawfilePath string) (string, error) {
	file, err := os.Open(clawfilePath)
	if err != nil {
		return "", fmt.Errorf("open clawfile %s: %w", clawfilePath, err)
	}
	defer file.Close()

	parsed, err := clawfile.Parse(file)
	if err != nil {
		return "", fmt.Errorf("parse clawfile %s: %w", clawfilePath, err)
	}

	d, err := driver.Lookup(parsed.Config.ClawType)
	if err != nil {
		return "", fmt.Errorf("validate CLAW_TYPE %q: %w", parsed.Config.ClawType, err)
	}

	if err := ensureBaseImage(parsed, d); err != nil {
		return "", err
	}

	var runner *clawfile.RunnerProvenance
	if provider, ok := d.(driver.RunnerBaseProvider); ok {
		tag, _ := provider.BaseImage()
		if extractFROMImage(parsed) == tag {
			runner, err = ResolveLocalRunnerProvenance(parsed.Config.ClawType, provider)
			if err != nil {
				return "", err
			}
		}
	}

	rendered, err := clawfile.Emit(parsed, runner)
	if err != nil {
		return "", fmt.Errorf("emit dockerfile: %w", err)
	}

	generatedPath := filepath.Join(filepath.Dir(clawfilePath), "Dockerfile.generated")
	if err := os.WriteFile(generatedPath, []byte(rendered), 0o644); err != nil {
		return "", fmt.Errorf("write generated dockerfile: %w", err)
	}

	return generatedPath, nil
}

// ensureBaseImage checks whether the FROM image exists locally. Non-runner
// BaseImageProvider drivers still get first-use builds; synthetic runner bases
// must be refreshed explicitly through claw pull.
func ensureBaseImage(parsed *clawfile.ParseResult, d driver.Driver) error {
	fromImage := extractFROMImage(parsed)
	if fromImage == "" {
		return nil
	}

	if ImageExistsLocally(fromImage) {
		return nil
	}

	provider, ok := d.(driver.BaseImageProvider)
	if !ok {
		return nil
	}

	tag, dockerfile := provider.BaseImage()
	if tag == "" || dockerfile == "" {
		return nil
	}

	// Only auto-build if the missing FROM matches the driver's declared base image.
	if fromImage != tag {
		return nil
	}

	if runnerProvider, ok := d.(driver.RunnerBaseProvider); ok {
		return &MissingRunnerBaseError{
			Alias:    runnerProvider.RunnerAlias(),
			ImageRef: tag,
		}
	}

	fmt.Printf("[claw] building base image %s (first time only)\n", tag)
	return BuildFromDockerfileContent(tag, dockerfile)
}

func BuildFromGenerated(generatedPath string, tag string, contextDir string) error {
	buildContext := strings.TrimSpace(contextDir)
	if buildContext == "" {
		buildContext = filepath.Dir(generatedPath)
	}

	args := []string{"build", "-f", generatedPath}
	if tag != "" {
		args = append(args, "-t", tag)
	}
	args = append(args, buildContext)

	cmd := exec.Command("docker", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker build failed: %w", err)
	}

	return nil
}

// ImageExistsLocally returns true if the given image tag is available in the
// local Docker daemon.
func ImageExistsLocally(tag string) bool {
	cmd := exec.Command("docker", "image", "inspect", tag)
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run() == nil
}

// BuildFromDockerfileContent writes a Dockerfile string to a temp dir and builds it.
func BuildFromDockerfileContent(tag, dockerfile string) error {
	return buildDockerfileContent(tag, dockerfile, false, false)
}

func RefreshRunnerBase(driverName string, d driver.RunnerBaseProvider) (*RefreshResult, error) {
	if d == nil {
		return nil, fmt.Errorf("refresh runner base %q: nil provider", strings.TrimSpace(driverName))
	}

	tag, dockerfile := d.BaseImage()
	if strings.TrimSpace(tag) == "" || strings.TrimSpace(dockerfile) == "" {
		return nil, fmt.Errorf("refresh runner base %q: empty base image recipe", strings.TrimSpace(driverName))
	}

	previousRef, previousTag := previousRunnerRef(tag)

	tempRef := imageRefWithTag(tag, fmt.Sprintf("claw-refresh-%d-%d", os.Getpid(), time.Now().UTC().UnixNano()))
	if err := buildDockerfileContent(tempRef, dockerfile, true, true); err != nil {
		return nil, err
	}
	defer removeDockerImageTag(tempRef)

	meta, err := inspectLocalImage(tempRef)
	if err != nil {
		return nil, err
	}

	versionTag, err := resolveRunnerVersionTag(tempRef, meta.ID, d)
	if err != nil {
		return nil, err
	}
	builtRef := imageRefWithTag(tag, versionTag)
	if err := dockerTagImage(tempRef, builtRef); err != nil {
		return nil, err
	}
	if err := dockerTagImage(tempRef, tag); err != nil {
		return nil, err
	}

	recipeSHA := sha256.Sum256([]byte(dockerfile))

	return &RefreshResult{
		DriverName:  strings.TrimSpace(driverName),
		Alias:       strings.TrimSpace(d.RunnerAlias()),
		ImageRef:    tag,
		BuiltRef:    builtRef,
		VersionTag:  versionTag,
		PreviousRef: previousRef,
		PreviousTag: previousTag,
		ImageID:     meta.ID,
		RecipeSHA:   "sha256:" + hex.EncodeToString(recipeSHA[:]),
	}, nil
}

func previousRunnerRef(imageRef string) (string, string) {
	meta, err := inspectLocalImage(imageRef)
	if err != nil || meta == nil {
		return "", ""
	}
	versionTag := findRunnerVersionTag(imageRef, meta.RepoTags)
	if versionTag == "" {
		return "", ""
	}
	return imageRefWithTag(imageRef, versionTag), versionTag
}

func ResolveLocalRunnerProvenance(driverName string, d driver.RunnerBaseProvider) (*clawfile.RunnerProvenance, error) {
	if d == nil {
		return nil, fmt.Errorf("resolve local runner provenance %q: nil provider", strings.TrimSpace(driverName))
	}

	tag, dockerfile := d.BaseImage()
	if strings.TrimSpace(tag) == "" {
		return nil, &MissingRunnerBaseError{
			Alias:    strings.TrimSpace(d.RunnerAlias()),
			ImageRef: tag,
		}
	}

	if !ImageExistsLocally(tag) {
		return nil, &MissingRunnerBaseError{
			Alias:    strings.TrimSpace(d.RunnerAlias()),
			ImageRef: tag,
		}
	}

	meta, err := inspectLocalImage(tag)
	if err != nil {
		return nil, err
	}

	versionTag := findRunnerVersionTag(tag, meta.RepoTags)
	if versionTag == "" {
		return nil, &RunnerRefreshRequiredError{
			Alias:    strings.TrimSpace(d.RunnerAlias()),
			ImageRef: tag,
			Reason:   fmt.Sprintf("runner base image %q is missing a versioned local tag", tag),
		}
	}

	recipeSHA := sha256.Sum256([]byte(dockerfile))

	return &clawfile.RunnerProvenance{
		DriverName: strings.TrimSpace(driverName),
		Alias:      strings.TrimSpace(d.RunnerAlias()),
		ImageRef:   tag,
		BuiltRef:   imageRefWithTag(tag, versionTag),
		VersionTag: versionTag,
		ImageID:    meta.ID,
		RecipeSHA:  "sha256:" + hex.EncodeToString(recipeSHA[:]),
	}, nil
}

func buildDockerfileContent(tag, dockerfile string, pull, noCache bool) error {
	tmpDir, err := os.MkdirTemp("", "claw-base-*")
	if err != nil {
		return fmt.Errorf("create temp dir for base image build: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	dockerfilePath := filepath.Join(tmpDir, "Dockerfile")
	if err := os.WriteFile(dockerfilePath, []byte(dockerfile), 0o644); err != nil {
		return fmt.Errorf("write base image Dockerfile: %w", err)
	}

	args := []string{"build"}
	if pull {
		args = append(args, "--pull")
	}
	if noCache {
		args = append(args, "--no-cache")
	}
	args = append(args, "-t", tag, tmpDir)

	cmd := exec.Command("docker", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("build base image %s: %w", tag, err)
	}
	return nil
}

func inspectLocalImage(imageRef string) (*localImageMetadata, error) {
	cmd := exec.Command("docker", "image", "inspect", imageRef)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("inspect local image %q: %w", imageRef, err)
	}

	var items []localImageMetadata
	if err := json.Unmarshal(out, &items); err != nil {
		return nil, fmt.Errorf("decode local image inspect %q: %w", imageRef, err)
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("inspect local image %q: no results", imageRef)
	}
	return &items[0], nil
}

func resolveRunnerVersionTag(imageRef, imageID string, d driver.RunnerBaseProvider) (string, error) {
	if prober, ok := d.(driver.RunnerVersionProber); ok {
		version, err := runRunnerVersionProbe(imageRef, prober.RunnerVersionProbe())
		if err != nil {
			return "", err
		}
		version = strings.TrimSpace(strings.TrimPrefix(version, "v"))
		if version != "" {
			return "v" + version, nil
		}
	}
	return fallbackRunnerVersionTag(imageID), nil
}

func runRunnerVersionProbe(imageRef string, probe []string) (string, error) {
	if len(probe) == 0 {
		return "", nil
	}
	args := append([]string{"run", "--rm", imageRef}, probe...)
	cmd := exec.Command("docker", args...)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("probe runner version from %q: %w", imageRef, err)
	}
	return strings.TrimSpace(string(out)), nil
}

func fallbackRunnerVersionTag(imageID string) string {
	shortID := shortImageID(imageID)
	if shortID == "" {
		shortID = "unknown"
	}
	return "built-" + time.Now().UTC().Format("20060102") + "-" + shortID
}

func findRunnerVersionTag(imageRef string, repoTags []string) string {
	base := imageRepo(imageRef)
	if base == "" {
		return ""
	}

	var fallback string
	for _, tag := range repoTags {
		if !strings.HasPrefix(tag, base+":") {
			continue
		}
		suffix := strings.TrimPrefix(tag, base+":")
		if suffix == "latest" || suffix == "" {
			continue
		}
		if strings.HasPrefix(suffix, "v") {
			return suffix
		}
		if fallback == "" {
			fallback = suffix
		}
	}
	return fallback
}

func dockerTagImage(srcRef, destRef string) error {
	cmd := exec.Command("docker", "tag", srcRef, destRef)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("tag image %q as %q: %w", srcRef, destRef, err)
	}
	return nil
}

func removeDockerImageTag(ref string) {
	if strings.TrimSpace(ref) == "" {
		return
	}
	cmd := exec.Command("docker", "image", "rm", ref)
	cmd.Stdout = nil
	cmd.Stderr = nil
	_ = cmd.Run()
}

func imageRepo(imageRef string) string {
	imageRef = strings.TrimSpace(imageRef)
	if imageRef == "" {
		return ""
	}
	slash := strings.LastIndex(imageRef, "/")
	colon := strings.LastIndex(imageRef, ":")
	if colon > slash {
		return imageRef[:colon]
	}
	return imageRef
}

func imageRefWithTag(imageRef, tag string) string {
	repo := imageRepo(imageRef)
	if repo == "" {
		return imageRef
	}
	return repo + ":" + strings.TrimSpace(tag)
}

func shortImageID(imageID string) string {
	trimmed := strings.TrimPrefix(strings.TrimSpace(imageID), "sha256:")
	if len(trimmed) > 12 {
		return trimmed[:12]
	}
	return trimmed
}

// extractFROMImage returns the image name from the first FROM instruction in
// the parsed Clawfile's Docker nodes.
func extractFROMImage(parsed *clawfile.ParseResult) string {
	for _, node := range parsed.DockerNodes {
		if strings.EqualFold(node.Value, "from") && node.Next != nil {
			return node.Next.Value
		}
	}
	return ""
}
