package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type goreleaserConfig struct {
	Archives []struct {
		Format       string `yaml:"format"`
		NameTemplate string `yaml:"name_template"`
	} `yaml:"archives"`
}

func TestInstallScriptMatchesArchiveNameTemplate(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(thisFile), "../.."))

	configBytes, err := os.ReadFile(filepath.Join(repoRoot, ".goreleaser.yml"))
	if err != nil {
		t.Fatalf("read .goreleaser.yml: %v", err)
	}

	var cfg goreleaserConfig
	if err := yaml.Unmarshal(configBytes, &cfg); err != nil {
		t.Fatalf("parse .goreleaser.yml: %v", err)
	}

	var archiveTemplate string
	for _, archive := range cfg.Archives {
		if archive.Format == "tar.gz" {
			archiveTemplate = archive.NameTemplate
			break
		}
	}
	if archiveTemplate == "" {
		t.Fatal("no tar.gz archive template found in .goreleaser.yml")
	}

	expectedTarball := strings.NewReplacer(
		"{{ .Version }}", "${VERSION}",
		"{{ .Os }}", "${OS}",
		"{{ .Arch }}", "${ARCH}",
	).Replace(archiveTemplate) + ".tar.gz"

	installBytes, err := os.ReadFile(filepath.Join(repoRoot, "install.sh"))
	if err != nil {
		t.Fatalf("read install.sh: %v", err)
	}

	wantLine := `TARBALL="` + expectedTarball + `"`
	if !strings.Contains(string(installBytes), wantLine) {
		t.Fatalf("install.sh tarball pattern drifted\nwant line: %s", wantLine)
	}
}
