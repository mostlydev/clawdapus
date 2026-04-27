package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunComposeUpRemediatesMissingRegistryImageWithPull(t *testing.T) {
	dir := t.TempDir()
	podFile := filepath.Join(dir, "claw-pod.yml")
	podYAML := `
services:
  api:
    image: ghcr.io/example/api:latest
`
	if err := os.WriteFile(podFile, []byte(podYAML), 0o644); err != nil {
		t.Fatalf("write pod file: %v", err)
	}

	prevExists := imageExistsLocally
	prevFix := composeUpFix
	imageExistsLocally = func(string) bool { return false }
	composeUpFix = false
	defer func() {
		imageExistsLocally = prevExists
		composeUpFix = prevFix
	}()

	err := runComposeUp(podFile)
	if err == nil {
		t.Fatal("expected remediation error")
	}
	if !strings.Contains(err.Error(), "run: claw pull") {
		t.Fatalf("expected claw pull remediation, got %v", err)
	}
}

func TestRunComposeUpRemediatesMissingBuiltImageWithBuild(t *testing.T) {
	dir := t.TempDir()
	podFile := filepath.Join(dir, "claw-pod.yml")
	podYAML := `
x-claw:
  pod: ops
services:
  analyst:
    build:
      context: .
`
	if err := os.WriteFile(podFile, []byte(podYAML), 0o644); err != nil {
		t.Fatalf("write pod file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM alpine:latest\n"), 0o644); err != nil {
		t.Fatalf("write Dockerfile: %v", err)
	}

	prevExists := imageExistsLocally
	prevFix := composeUpFix
	imageExistsLocally = func(string) bool { return false }
	composeUpFix = false
	defer func() {
		imageExistsLocally = prevExists
		composeUpFix = prevFix
	}()

	err := runComposeUp(podFile)
	if err == nil {
		t.Fatal("expected remediation error")
	}
	if !strings.Contains(err.Error(), "run: claw build") {
		t.Fatalf("expected claw build remediation, got %v", err)
	}
}

func TestPullInfraImageFromRegistryNeverBuilds(t *testing.T) {
	prevExists := imageExistsLocally
	prevRunInfra := runInfraDockerCommand
	defer func() {
		imageExistsLocally = prevExists
		runInfraDockerCommand = prevRunInfra
	}()

	imageExistsLocally = func(string) bool { return false }

	var commands []string
	runInfraDockerCommand = func(args ...string) error {
		if len(args) > 0 {
			commands = append(commands, args[0])
		}
		return nil
	}

	spec := infraImageSpec{
		Component:      "test-component",
		ExpectedRef:    "ghcr.io/mostlydev/test:v1.0.0",
		DockerfilePath: "dockerfiles/test/Dockerfile",
		ContextDir:     ".",
	}

	if err := pullInfraImageFromRegistry(spec); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, cmd := range commands {
		if cmd == "build" {
			t.Fatal("pullInfraImageFromRegistry must never invoke docker build")
		}
	}
}

func TestPullInfraImageFromRegistryFailsWhenNotPublished(t *testing.T) {
	prevExists := imageExistsLocally
	prevRunInfra := runInfraDockerCommand
	defer func() {
		imageExistsLocally = prevExists
		runInfraDockerCommand = prevRunInfra
	}()

	imageExistsLocally = func(string) bool { return false }
	runInfraDockerCommand = func(args ...string) error {
		return &exec.ExitError{}
	}

	spec := infraImageSpec{
		Component:   "test-component",
		ExpectedRef: "ghcr.io/mostlydev/test:v1.0.0",
	}

	err := pullInfraImageFromRegistry(spec)
	if err == nil {
		t.Fatal("expected error when registry pull fails")
	}
	if !strings.Contains(err.Error(), "not available in registry") {
		t.Fatalf("expected registry-specific error, got: %v", err)
	}
}

func TestRunPullDoesNotPullCllamaPolicyByDefault(t *testing.T) {
	dir := t.TempDir()
	podFile := filepath.Join(dir, "claw-pod.yml")
	podYAML := `
services:
  api:
    image: ghcr.io/example/api:latest
`
	if err := os.WriteFile(podFile, []byte(podYAML), 0o644); err != nil {
		t.Fatalf("write pod file: %v", err)
	}

	prevExists := imageExistsLocally
	prevRunInfra := runInfraDockerCommand
	defer func() {
		imageExistsLocally = prevExists
		runInfraDockerCommand = prevRunInfra
	}()

	imageExistsLocally = func(string) bool { return false }

	var pulled []string
	runInfraDockerCommand = func(args ...string) error {
		if len(args) >= 2 && args[0] == "pull" {
			pulled = append(pulled, args[1])
		}
		return nil
	}

	if err := runPull(podFile, true); err != nil {
		t.Fatalf("runPull: %v", err)
	}

	joined := strings.Join(pulled, "\n")
	if strings.Contains(joined, "cllama-policy") {
		t.Fatalf("did not expect cllama-policy to be pulled, got: %v", pulled)
	}
	for _, unexpected := range []string{
		"ghcr.io/mostlydev/claw-api:",
		"ghcr.io/mostlydev/clawdash:",
		"ghcr.io/mostlydev/claw-wall:",
		"ghcr.io/mostlydev/cllama:",
	} {
		if strings.Contains(joined, unexpected) {
			t.Fatalf("did not expect unrelated infra pull %q, got: %v", unexpected, pulled)
		}
	}
}

func TestRunPullPullsCllamaPolicyForPolicyPod(t *testing.T) {
	dir := t.TempDir()
	podFile := filepath.Join(dir, "claw-pod.yml")
	podYAML := `
services:
  analyst:
    image: ghcr.io/example/analyst:latest
    x-claw:
      cllama: policy
`
	if err := os.WriteFile(podFile, []byte(podYAML), 0o644); err != nil {
		t.Fatalf("write pod file: %v", err)
	}

	prevExists := imageExistsLocally
	prevRunInfra := runInfraDockerCommand
	defer func() {
		imageExistsLocally = prevExists
		runInfraDockerCommand = prevRunInfra
	}()

	imageExistsLocally = func(string) bool { return false }

	var pulled []string
	runInfraDockerCommand = func(args ...string) error {
		if len(args) >= 2 && args[0] == "pull" {
			pulled = append(pulled, args[1])
		}
		return nil
	}

	if err := runPull(podFile, true); err != nil {
		t.Fatalf("runPull: %v", err)
	}

	joined := strings.Join(pulled, "\n")
	if !strings.Contains(joined, "cllama-policy") {
		t.Fatalf("expected cllama-policy pull for policy pod, got: %v", pulled)
	}
	if !strings.Contains(joined, "ghcr.io/mostlydev/clawdash:") {
		t.Fatalf("expected clawdash pull for managed pod, got: %v", pulled)
	}
	if strings.Contains(joined, "ghcr.io/mostlydev/claw-api:") {
		t.Fatalf("did not expect claw-api pull without master/schedule need, got: %v", pulled)
	}
}

func TestRunPullSkipsBuildOwnedServices(t *testing.T) {
	dir := t.TempDir()
	podFile := filepath.Join(dir, "claw-pod.yml")
	podYAML := `
x-claw:
  pod: ops
services:
  registry:
    image: ghcr.io/example/registry:latest
  builder:
    build:
      context: .
`
	if err := os.WriteFile(podFile, []byte(podYAML), 0o644); err != nil {
		t.Fatalf("write pod file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM alpine:latest\n"), 0o644); err != nil {
		t.Fatalf("write Dockerfile: %v", err)
	}

	prevExists := imageExistsLocally
	prevRunInfra := runInfraDockerCommand
	defer func() {
		imageExistsLocally = prevExists
		runInfraDockerCommand = prevRunInfra
	}()

	imageExistsLocally = func(string) bool { return false }

	var pulls []string
	runInfraDockerCommand = func(args ...string) error {
		if len(args) >= 2 && args[0] == "pull" {
			pulls = append(pulls, args[1])
		}
		return nil
	}

	if err := runPull(podFile, true); err != nil {
		t.Fatalf("runPull: %v", err)
	}

	joined := strings.Join(pulls, "\n")
	if !strings.Contains(joined, "ghcr.io/example/registry:latest") {
		t.Fatalf("expected registry service image pull, got %v", pulls)
	}
	if strings.Contains(joined, "claw-local/ops-builder:latest") {
		t.Fatalf("did not expect build-owned image pull, got %v", pulls)
	}
}
