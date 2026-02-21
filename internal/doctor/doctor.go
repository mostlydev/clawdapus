package doctor

import (
	"errors"
	"os/exec"
	"strings"
)

type Runner func(name string, args ...string) ([]byte, error)

type CheckResult struct {
	Name    string
	OK      bool
	Version string
	Detail  string
}

func defaultRunner(name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	stdout, err := cmd.Output()
	if err == nil {
		return stdout, nil
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		combined := make([]byte, 0, len(stdout)+len(exitErr.Stderr))
		combined = append(combined, stdout...)
		combined = append(combined, exitErr.Stderr...)
		return combined, err
	}

	return stdout, err
}

func CheckDocker(run Runner) CheckResult {
	return check("docker", run, "docker", "version", "--format", "{{.Client.Version}}")
}

func CheckBuildx(run Runner) CheckResult {
	return check("buildkit", run, "docker", "buildx", "version")
}

func CheckCompose(run Runner) CheckResult {
	return check("compose", run, "docker", "compose", "version", "--short")
}

func RunAll() []CheckResult {
	return RunAllWithRunner(defaultRunner)
}

func RunAllWithRunner(run Runner) []CheckResult {
	return []CheckResult{
		CheckDocker(run),
		CheckBuildx(run),
		CheckCompose(run),
	}
}

func check(name string, run Runner, binary string, args ...string) CheckResult {
	output, err := run(binary, args...)
	if err != nil {
		return CheckResult{
			Name:   name,
			OK:     false,
			Detail: strings.TrimSpace(string(output)),
		}
	}

	version := strings.TrimSpace(firstLine(string(output)))
	return CheckResult{
		Name:    name,
		OK:      version != "",
		Version: version,
	}
}

func firstLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[0])
}
