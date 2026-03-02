package build

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/mostlydev/clawdapus/internal/clawfile"
	"github.com/mostlydev/clawdapus/internal/driver"
	_ "github.com/mostlydev/clawdapus/internal/driver/microclaw"
	_ "github.com/mostlydev/clawdapus/internal/driver/nanoclaw"
	_ "github.com/mostlydev/clawdapus/internal/driver/nullclaw"
	_ "github.com/mostlydev/clawdapus/internal/driver/openclaw" // register built-in drivers for build-time validation
)

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

	rendered, err := clawfile.Emit(parsed)
	if err != nil {
		return "", fmt.Errorf("emit dockerfile: %w", err)
	}

	generatedPath := filepath.Join(filepath.Dir(clawfilePath), "Dockerfile.generated")
	if err := os.WriteFile(generatedPath, []byte(rendered), 0o644); err != nil {
		return "", fmt.Errorf("write generated dockerfile: %w", err)
	}

	return generatedPath, nil
}

// ensureBaseImage checks whether the FROM image exists locally. If it's missing
// and the driver implements BaseImageProvider, auto-builds the base image.
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

	fmt.Printf("[claw] building base image %s (first time only)\n", tag)
	return BuildFromDockerfileContent(tag, dockerfile)
}

func BuildFromGenerated(generatedPath string, tag string) error {
	buildContext := filepath.Dir(generatedPath)

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
	tmpDir, err := os.MkdirTemp("", "claw-base-*")
	if err != nil {
		return fmt.Errorf("create temp dir for base image build: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	dockerfilePath := filepath.Join(tmpDir, "Dockerfile")
	if err := os.WriteFile(dockerfilePath, []byte(dockerfile), 0o644); err != nil {
		return fmt.Errorf("write base image Dockerfile: %w", err)
	}

	cmd := exec.Command("docker", "build", "-t", tag, tmpDir)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("build base image %s: %w", tag, err)
	}
	return nil
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
