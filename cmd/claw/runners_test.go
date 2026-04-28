package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mostlydev/clawdapus/internal/build"
	"github.com/mostlydev/clawdapus/internal/driver"
	"github.com/mostlydev/clawdapus/internal/driver/hermes"
)

func TestResolvePullTargetTreatsNonYAMLArgumentAsClawfile(t *testing.T) {
	dir := t.TempDir()
	clawfilePath := filepath.Join(dir, "Clawfile.openclaw")
	if err := os.WriteFile(clawfilePath, []byte("FROM openclaw:latest\nCLAW_TYPE openclaw\nAGENT AGENTS.md\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	target, err := resolvePullTarget("", []string{clawfilePath})
	if err != nil {
		t.Fatalf("resolvePullTarget: %v", err)
	}
	if !target.HasClawfile || target.ClawfilePath != clawfilePath {
		t.Fatalf("expected clawfile target %q, got %#v", clawfilePath, target)
	}
}

func TestResolvePullTargetTreatsYAMLArgumentAsPod(t *testing.T) {
	target, err := resolvePullTarget("", []string{"examples/quickstart/claw-pod.yml"})
	if err != nil {
		t.Fatalf("resolvePullTarget: %v", err)
	}
	if !target.HasPod || target.PodFile != "examples/quickstart/claw-pod.yml" {
		t.Fatalf("expected pod target, got %#v", target)
	}
}

func TestRunPullClawfileRefreshesRunner(t *testing.T) {
	dir := t.TempDir()
	clawfilePath := filepath.Join(dir, "Clawfile")
	if err := os.WriteFile(clawfilePath, []byte("FROM openclaw:latest\nCLAW_TYPE openclaw\nAGENT AGENTS.md\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	prevRefresh := refreshRunnerBase
	defer func() { refreshRunnerBase = prevRefresh }()

	var called []string
	refreshRunnerBase = func(name string, d driver.RunnerBaseProvider) (*build.RefreshResult, error) {
		called = append(called, name)
		tag, _ := d.BaseImage()
		return &build.RefreshResult{DriverName: name, Alias: d.RunnerAlias(), ImageRef: tag}, nil
	}

	err := runPullTarget(pullTarget{ClawfilePath: clawfilePath, HasClawfile: true}, pullOptions{})
	if err != nil {
		t.Fatalf("runPullTarget: %v", err)
	}
	if len(called) != 1 || called[0] != "openclaw" {
		t.Fatalf("expected refresh for openclaw, got %v", called)
	}
}

func TestRunPullPodRefreshesUsedRunner(t *testing.T) {
	dir := t.TempDir()
	clawfilePath := filepath.Join(dir, "Clawfile")
	if err := os.WriteFile(clawfilePath, []byte("FROM openclaw:latest\nCLAW_TYPE openclaw\nAGENT AGENTS.md\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	podFile := filepath.Join(dir, "claw-pod.yml")
	podYAML := `
services:
  analyst:
    build:
      context: .
`
	if err := os.WriteFile(podFile, []byte(podYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	prevRefresh := refreshRunnerBase
	defer func() { refreshRunnerBase = prevRefresh }()

	var called []string
	refreshRunnerBase = func(name string, d driver.RunnerBaseProvider) (*build.RefreshResult, error) {
		called = append(called, name)
		tag, _ := d.BaseImage()
		return &build.RefreshResult{DriverName: name, Alias: d.RunnerAlias(), ImageRef: tag}, nil
	}

	if err := runPullTarget(pullTarget{PodFile: podFile, HasPod: true}, pullOptions{}); err != nil {
		t.Fatalf("runPullTarget: %v", err)
	}
	if len(called) != 1 || called[0] != "openclaw" {
		t.Fatalf("expected refresh for openclaw, got %v", called)
	}
}

func TestRunPullPodSkipsNonRefreshableClawfileDrivers(t *testing.T) {
	dir := t.TempDir()
	openClawfilePath := filepath.Join(dir, "openclaw", "Clawfile")
	hermesClawfilePath := filepath.Join(dir, "hermes", "Clawfile")
	if err := os.MkdirAll(filepath.Dir(openClawfilePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(hermesClawfilePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(openClawfilePath, []byte("FROM openclaw:latest\nCLAW_TYPE openclaw\nAGENT AGENTS.md\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hermesClawfilePath, []byte("FROM "+hermes.BaseImageTag+"\nCLAW_TYPE hermes\nAGENT AGENTS.md\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	podFile := filepath.Join(dir, "claw-pod.yml")
	podYAML := `
services:
  analyst:
    build:
      context: ./openclaw
  messenger:
    build:
      context: ./hermes
`
	if err := os.WriteFile(podFile, []byte(podYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	prevRefresh := refreshRunnerBase
	defer func() { refreshRunnerBase = prevRefresh }()

	var called []string
	refreshRunnerBase = func(name string, d driver.RunnerBaseProvider) (*build.RefreshResult, error) {
		called = append(called, name)
		tag, _ := d.BaseImage()
		return &build.RefreshResult{DriverName: name, Alias: d.RunnerAlias(), ImageRef: tag}, nil
	}

	if err := runPullTarget(pullTarget{PodFile: podFile, HasPod: true}, pullOptions{}); err != nil {
		t.Fatalf("runPullTarget: %v", err)
	}
	if len(called) != 1 || called[0] != "openclaw" {
		t.Fatalf("expected only openclaw to refresh, got %v", called)
	}
}

func TestRunPullNoRunnersSkipsRunnerRefresh(t *testing.T) {
	dir := t.TempDir()
	clawfilePath := filepath.Join(dir, "Clawfile")
	if err := os.WriteFile(clawfilePath, []byte("FROM openclaw:latest\nCLAW_TYPE openclaw\nAGENT AGENTS.md\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	prevRefresh := refreshRunnerBase
	defer func() { refreshRunnerBase = prevRefresh }()

	refreshRunnerBase = func(name string, d driver.RunnerBaseProvider) (*build.RefreshResult, error) {
		t.Fatalf("refreshRunnerBase should not be called for --no-runners, got %q", name)
		return nil, nil
	}

	err := runPullTarget(pullTarget{ClawfilePath: clawfilePath, HasClawfile: true}, pullOptions{NoRunners: true})
	if err != nil {
		t.Fatalf("runPullTarget: %v", err)
	}
}

func TestRunPullPodNoRunnersSkipsRunnerRefresh(t *testing.T) {
	dir := t.TempDir()
	clawfilePath := filepath.Join(dir, "Clawfile")
	if err := os.WriteFile(clawfilePath, []byte("FROM openclaw:latest\nCLAW_TYPE openclaw\nAGENT AGENTS.md\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	podFile := filepath.Join(dir, "claw-pod.yml")
	podYAML := `
services:
  analyst:
    build:
      context: .
`
	if err := os.WriteFile(podFile, []byte(podYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	prevRefresh := refreshRunnerBase
	defer func() { refreshRunnerBase = prevRefresh }()

	refreshRunnerBase = func(name string, d driver.RunnerBaseProvider) (*build.RefreshResult, error) {
		t.Fatalf("refreshRunnerBase should not be called for --no-runners, got %q", name)
		return nil, nil
	}

	err := runPullTarget(pullTarget{PodFile: podFile, HasPod: true}, pullOptions{NoRunners: true})
	if err != nil {
		t.Fatalf("runPullTarget: %v", err)
	}
}

func TestRunPullNoTargetUsesLocallyTaggedRunners(t *testing.T) {
	prevRefresh := refreshRunnerBase
	prevExists := imageExistsLocally
	defer func() {
		refreshRunnerBase = prevRefresh
		imageExistsLocally = prevExists
	}()

	imageExistsLocally = func(image string) bool {
		return image == "openclaw:latest" || strings.HasPrefix(image, "ghcr.io/mostlydev/")
	}

	var called []string
	refreshRunnerBase = func(name string, d driver.RunnerBaseProvider) (*build.RefreshResult, error) {
		called = append(called, name)
		tag, _ := d.BaseImage()
		return &build.RefreshResult{DriverName: name, Alias: d.RunnerAlias(), ImageRef: tag}, nil
	}

	if err := runPullTarget(pullTarget{}, pullOptions{}); err != nil {
		t.Fatalf("runPullTarget: %v", err)
	}
	if len(called) != 1 || called[0] != "openclaw" {
		t.Fatalf("expected only openclaw to refresh, got %v", called)
	}
}
