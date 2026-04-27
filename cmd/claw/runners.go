package main

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/mostlydev/clawdapus/internal/build"
	"github.com/mostlydev/clawdapus/internal/clawfile"
	"github.com/mostlydev/clawdapus/internal/describe"
	"github.com/mostlydev/clawdapus/internal/driver"
	"github.com/mostlydev/clawdapus/internal/pod"
)

var (
	refreshRunnerBase = build.RefreshRunnerBase
	registeredDrivers = driver.Registered
)

func requiredRunnerDriversForPod(podDir string, p *pod.Pod, plans []plannedServiceImage) (map[string]driver.RunnerBaseProvider, error) {
	if p == nil {
		return nil, nil
	}

	out := make(map[string]driver.RunnerBaseProvider)
	for _, plan := range plans {
		if plan.BuildConfig == nil {
			continue
		}

		svc := p.Services[plan.ServiceName]
		if svc == nil {
			continue
		}

		driverName, provider, err := runnerDriverForBuildConfig(podDir, svc.Compose["build"])
		if err != nil {
			return nil, fmt.Errorf("service %q: resolve runner driver: %w", plan.ServiceName, err)
		}
		if provider == nil || strings.TrimSpace(driverName) == "" {
			continue
		}
		out[driverName] = provider
	}
	return out, nil
}

func runnerDriverForBuildConfig(podDir string, buildRaw interface{}) (string, driver.RunnerBaseProvider, error) {
	if buildRaw == nil {
		return "", nil, nil
	}

	contextDir, err := describe.ResolveBuildContextDir(podDir, buildRaw)
	if err != nil || contextDir == "" {
		return "", nil, err
	}

	dockerfilePath, err := resolveBuildDockerfilePath(contextDir, resolveComposeBuildDockerfile(buildRaw))
	if err != nil {
		return "", nil, err
	}
	if isClawBuildFile(dockerfilePath) {
		name, provider, err := optionalRunnerDriverForClawfile(dockerfilePath)
		if err != nil {
			return "", nil, err
		}
		return name, provider, nil
	}

	info, err := inspectBuildMetadata(podDir, buildRaw)
	if err != nil {
		return "", nil, err
	}
	if info == nil || strings.TrimSpace(info.ClawType) == "" {
		return "", nil, nil
	}

	d, err := driver.Lookup(info.ClawType)
	if err != nil {
		return "", nil, err
	}
	provider, ok := d.(driver.RunnerBaseProvider)
	if !ok {
		return strings.TrimSpace(info.ClawType), nil, nil
	}
	return strings.TrimSpace(info.ClawType), provider, nil
}

func optionalRunnerDriverForClawfile(clawfilePath string) (string, driver.RunnerBaseProvider, error) {
	file, err := os.Open(clawfilePath)
	if err != nil {
		return "", nil, fmt.Errorf("open clawfile %q: %w", clawfilePath, err)
	}
	defer file.Close()

	parsed, err := clawfile.Parse(file)
	if err != nil {
		return "", nil, fmt.Errorf("parse clawfile %q: %w", clawfilePath, err)
	}
	driverName := strings.TrimSpace(parsed.Config.ClawType)
	if driverName == "" {
		return "", nil, fmt.Errorf("clawfile %q has no CLAW_TYPE", clawfilePath)
	}

	d, err := driver.Lookup(driverName)
	if err != nil {
		return "", nil, err
	}
	provider, ok := d.(driver.RunnerBaseProvider)
	if !ok {
		return driverName, nil, nil
	}
	return driverName, provider, nil
}

func refreshableRunnerDriverForClawfile(clawfilePath string) (string, driver.RunnerBaseProvider, error) {
	driverName, provider, err := optionalRunnerDriverForClawfile(clawfilePath)
	if err != nil {
		return "", nil, err
	}
	if provider == nil {
		return "", nil, fmt.Errorf("driver %q does not have a refreshable local runner base", driverName)
	}
	return driverName, provider, nil
}

func locallyTaggedRunnerDrivers() map[string]driver.RunnerBaseProvider {
	out := make(map[string]driver.RunnerBaseProvider)
	for name, d := range registeredDrivers() {
		provider, ok := d.(driver.RunnerBaseProvider)
		if !ok {
			continue
		}
		tag, _ := provider.BaseImage()
		if strings.TrimSpace(tag) == "" || !imageExistsLocally(tag) {
			continue
		}
		out[name] = provider
	}
	return out
}

func refreshSelectedRunnerDrivers(drivers map[string]driver.RunnerBaseProvider) error {
	names := make([]string, 0, len(drivers))
	for name := range drivers {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		provider := drivers[name]
		tag, _ := provider.BaseImage()
		fmt.Printf("[claw] %s: refreshing runner base %s from upstream sources\n", name, tag)
		result, err := refreshRunnerBase(name, provider)
		if err != nil {
			return fmt.Errorf("refresh runner base %q: %w", name, err)
		}
		if strings.TrimSpace(result.BuiltRef) != "" && strings.TrimSpace(result.PreviousRef) != "" && result.PreviousRef != result.BuiltRef {
			fmt.Printf("[claw] %s: refreshed %s and tagged %s (was %s)\n", name, result.ImageRef, result.BuiltRef, result.PreviousRef)
		} else if strings.TrimSpace(result.BuiltRef) != "" {
			fmt.Printf("[claw] %s: refreshed %s and tagged %s\n", name, result.ImageRef, result.BuiltRef)
		} else {
			fmt.Printf("[claw] %s: refreshed %s\n", name, result.ImageRef)
		}
	}
	return nil
}
