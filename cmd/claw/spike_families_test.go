//go:build spike

package main

import (
	"context"
	"os/exec"
	"strings"
	"testing"
)

// TestSpikeFamilyPriceExport reads the price table from a real published
// cllama image through the same one-off Compose project claw up uses, and
// checks nothing is left behind.
func TestSpikeFamilyPriceExport(t *testing.T) {
	const tag = "v0.16.0"
	if err := exec.Command("docker", "image", "inspect", "ghcr.io/mostlydev/cllama:"+tag).Run(); err != nil {
		if out, err := exec.Command("docker", "pull", "ghcr.io/mostlydev/cllama:"+tag).CombinedOutput(); err != nil {
			t.Skipf("cllama %s not available: %v\n%s", tag, err, out)
		}
	}
	prev := infraCllamaTag
	infraCllamaTag = tag
	defer func() { infraCllamaTag = prev }()

	prices, source, err := loadPinnedCllamaPrices(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if source.Tag != tag || !strings.HasPrefix(source.ImageID, "sha256:") || prices.Version != tag {
		t.Fatalf("source = %+v, export version %q", source, prices.Version)
	}
	price, ok := prices.Exact("openai", "gpt-6.1-terra")
	if !ok || price.Input != 1 || price.Output != 5 || price.MaxInput != 2 || price.MaxOutput != 7.5 || price.ContextTiers != "all" {
		t.Fatalf("gpt-6.1-terra = %+v (%v)", price, ok)
	}
	if _, ok := prices.Exact("openai", "gpt-6.1-luna"); ok {
		t.Fatal("an unlisted model has an exact price")
	}
	out, _ := exec.Command("docker", "ps", "-a", "--filter", "name=claw-prices-", "--format", "{{.Names}}").Output()
	if strings.TrimSpace(string(out)) != "" {
		t.Fatalf("the price export left containers behind: %s", out)
	}
	nets, _ := exec.Command("docker", "network", "ls", "--filter", "name=claw-prices-", "--format", "{{.Name}}").Output()
	if strings.TrimSpace(string(nets)) != "" {
		t.Fatalf("the price export left networks behind: %s", nets)
	}

	// An image without the command is reported as such.
	infraCllamaTag = "v0.15.1"
	if exec.Command("docker", "image", "inspect", "ghcr.io/mostlydev/cllama:v0.15.1").Run() == nil {
		if _, _, err := loadPinnedCllamaPrices(context.Background()); err == nil || !strings.Contains(err.Error(), "v0.16.0 or later") {
			t.Fatalf("older image: %v", err)
		}
	}
}
