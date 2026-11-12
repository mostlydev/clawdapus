package main

import (
	"github.com/mostlydev/clawdapus/internal/pod"
	"os"
	"path/filepath"
	"testing"
)

func TestVersionedContentRoot(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "file"), []byte("x"), 0444)
	home := &pod.RunHome{Content: []pod.RunContent{{Source: "file", Target: "/ref", Versioned: true}}}
	if _, err := resolveRunContent("worker", root, home); err == nil {
		t.Fatal("versioned file accepted")
	}
	os.Mkdir(filepath.Join(root, "versions"), 0755)
	home.Content[0].Source = "versions"
	mounts, err := resolveRunContent("worker", root, home)
	if err != nil || len(mounts) != 1 || !mounts[0].Versioned {
		t.Fatalf("mounts %+v %v", mounts, err)
	}
}
