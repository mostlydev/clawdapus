package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mostlydev/clawdapus/internal/driver"
	_ "github.com/mostlydev/clawdapus/internal/driver/openclaw" // register driver
	"github.com/mostlydev/clawdapus/internal/inspect"
	"github.com/mostlydev/clawdapus/internal/pod"
	"github.com/mostlydev/clawdapus/internal/runtime"
)

var composeUpDetach bool

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
	runtimeDir := filepath.Join(podDir, ".claw-runtime")
	if err := os.MkdirAll(runtimeDir, 0700); err != nil {
		return fmt.Errorf("create runtime dir: %w", err)
	}

	results := make(map[string]*driver.MaterializeResult)
	drivers := make(map[string]driver.Driver)
	resolvedClaws := make(map[string]*driver.ResolvedClaw)

	for name, svc := range p.Services {
		if svc.Claw == nil {
			continue
		}

		info, err := inspect.Inspect(svc.Image)
		if err != nil {
			return fmt.Errorf("inspect image %q for service %q: %w", svc.Image, name, err)
		}

		if info.ClawType == "" {
			return fmt.Errorf("service %q: image %q has no claw.type label", name, svc.Image)
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

		// Parse pod-level surfaces into ResolvedSurface structs
		var surfaces []driver.ResolvedSurface
		if svc.Claw != nil {
			for _, raw := range svc.Claw.Surfaces {
				s, err := pod.ParseSurface(raw)
				if err != nil {
					return fmt.Errorf("service %q: %w", name, err)
				}
				surfaces = append(surfaces, s)
			}
		}

		// Merge skills: image-level (from labels) + pod-level (from x-claw)
		var skillPaths []string
		skillPaths = append(skillPaths, info.Skills...)
		if svc.Claw != nil {
			skillPaths = append(skillPaths, svc.Claw.Skills...)
		}
		skills, err := runtime.ResolveSkills(podDir, skillPaths)
		if err != nil {
			return fmt.Errorf("service %q: %w", name, err)
		}

		rc := &driver.ResolvedClaw{
			ServiceName:   name,
			ImageRef:      svc.Image,
			ClawType:      info.ClawType,
			Agent:         agentFile,
			AgentHostPath: agentHostPath,
			Models:        info.Models,
			Configures:    info.Configures,
			Privileges:    info.Privileges,
			Count:         svc.Claw.Count,
			Environment:   svc.Environment,
			Surfaces:      surfaces,
			Skills:        skills,
		}

		d, err := driver.Lookup(rc.ClawType)
		if err != nil {
			return fmt.Errorf("service %q: %w", name, err)
		}

		if err := d.Validate(rc); err != nil {
			return fmt.Errorf("service %q: validation failed: %w", name, err)
		}

		svcRuntimeDir := filepath.Join(runtimeDir, name)
		if err := os.MkdirAll(svcRuntimeDir, 0700); err != nil {
			return fmt.Errorf("create service runtime dir: %w", err)
		}

		result, err := d.Materialize(rc, driver.MaterializeOpts{RuntimeDir: svcRuntimeDir, PodName: p.Name})
		if err != nil {
			return fmt.Errorf("service %q: materialization failed: %w", name, err)
		}

		// Mount individual skill files into the driver's skill directory
		if result.SkillDir != "" && len(rc.Skills) > 0 {
			for _, sk := range rc.Skills {
				result.Mounts = append(result.Mounts, driver.Mount{
					HostPath:      sk.HostPath,
					ContainerPath: filepath.Join(result.SkillDir, sk.Name),
					ReadOnly:      true,
				})
			}
		}

		results[name] = result
		drivers[name] = d
		resolvedClaws[name] = rc
		fmt.Printf("[claw] %s: validated and materialized (%s driver)\n", name, rc.ClawType)
	}

	output, err := pod.EmitCompose(p, results)
	if err != nil {
		return err
	}

	generatedPath := filepath.Join(podDir, "compose.generated.yml")
	if err := os.WriteFile(generatedPath, []byte(output), 0644); err != nil {
		return fmt.Errorf("write compose.generated.yml: %w", err)
	}
	fmt.Printf("[claw] wrote %s\n", generatedPath)

	if len(drivers) == 0 {
		fmt.Println("[claw] warning: no x-claw services found; running plain docker compose lifecycle")
	}

	if len(drivers) > 0 && !composeUpDetach {
		return fmt.Errorf("claw-managed services require detached mode for fail-closed post-apply verification; rerun with 'claw compose up -d %s'", podFile)
	}

	composeArgs := []string{"compose", "-f", generatedPath, "up"}
	if composeUpDetach {
		composeArgs = append(composeArgs, "-d")
	}

	dockerCmd := exec.Command("docker", composeArgs...)
	dockerCmd.Stdout = os.Stdout
	dockerCmd.Stderr = os.Stderr
	if err := dockerCmd.Run(); err != nil {
		return fmt.Errorf("docker compose up failed: %w", err)
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

func init() {
	composeUpCmd.Flags().BoolVarP(&composeUpDetach, "detach", "d", false, "Run in background")
	composeCmd.AddCommand(composeUpCmd)
}
