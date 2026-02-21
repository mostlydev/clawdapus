package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/mostlydev/clawdapus/internal/driver"
	_ "github.com/mostlydev/clawdapus/internal/driver/openclaw" // register driver
	"github.com/mostlydev/clawdapus/internal/inspect"
	"github.com/mostlydev/clawdapus/internal/pod"
	"github.com/mostlydev/clawdapus/internal/runtime"
)

var composeUpDetach bool

var composeUpCmd = &cobra.Command{
	Use:   "up",
	Short: "Launch a Claw pod from claw-pod.yml",
	RunE: func(cmd *cobra.Command, args []string) error {
		podFile := "claw-pod.yml"
		if len(args) > 0 {
			podFile = args[0]
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

	podDir, _ := filepath.Abs(filepath.Dir(podFile))
	runtimeDir := filepath.Join(podDir, ".claw-runtime")
	if err := os.MkdirAll(runtimeDir, 0755); err != nil {
		return fmt.Errorf("create runtime dir: %w", err)
	}

	results := make(map[string]*driver.MaterializeResult)

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
		}

		d, err := driver.Lookup(rc.ClawType)
		if err != nil {
			return fmt.Errorf("service %q: %w", name, err)
		}

		if err := d.Validate(rc); err != nil {
			return fmt.Errorf("service %q: validation failed: %w", name, err)
		}

		svcRuntimeDir := filepath.Join(runtimeDir, name)
		if err := os.MkdirAll(svcRuntimeDir, 0755); err != nil {
			return fmt.Errorf("create service runtime dir: %w", err)
		}

		result, err := d.Materialize(rc, driver.MaterializeOpts{RuntimeDir: svcRuntimeDir})
		if err != nil {
			return fmt.Errorf("service %q: materialization failed: %w", name, err)
		}

		results[name] = result
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

	fmt.Println("[claw] pod is up")
	return nil
}

func init() {
	composeUpCmd.Flags().BoolVarP(&composeUpDetach, "detach", "d", false, "Run in background")
	composeCmd.AddCommand(composeUpCmd)
}
