package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const runtimeCleanupImage = portableMemoryRepairImage

func removeRuntimeDirWithDocker(path string) error {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolve runtime cleanup path %q: %w", path, err)
	}

	parent := filepath.Dir(absPath)
	target := filepath.Base(absPath)
	if target == "" || target == "." || target == ".." || strings.ContainsRune(target, filepath.Separator) {
		return fmt.Errorf("invalid runtime cleanup target %q", target)
	}

	args := []string{
		"run", "--rm",
		"--user", "0:0",
		"-v", parent + ":/runtime-parent:rw",
		"-e", "RUNTIME_CLEANUP_TARGET=" + target,
		runtimeCleanupImage,
		"sh", "-ceu",
		`case "${RUNTIME_CLEANUP_TARGET:-}" in
	""|"."|".."|*/*) echo "invalid runtime cleanup target" >&2; exit 64;;
esac
rm -rf -- "/runtime-parent/${RUNTIME_CLEANUP_TARGET}"`,
	}
	if err := runInfraDockerCommand(args...); err != nil {
		return fmt.Errorf("docker helper runtime cleanup: %w", err)
	}
	if _, err := os.Stat(absPath); err == nil {
		return fmt.Errorf("docker helper runtime cleanup left %q in place", absPath)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat cleaned runtime dir %q: %w", absPath, err)
	}
	return nil
}
