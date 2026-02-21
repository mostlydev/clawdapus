package build

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/mostlydev/clawdapus/internal/clawfile"
	"github.com/mostlydev/clawdapus/internal/driver"
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
	if _, err := driver.Lookup(parsed.Config.ClawType); err != nil {
		return "", fmt.Errorf("validate CLAW_TYPE %q: %w", parsed.Config.ClawType, err)
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
