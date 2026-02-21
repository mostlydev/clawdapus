package runtime

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mostlydev/clawdapus/internal/driver"
)

// ResolveSkills validates that all skill files exist, checks for path traversal,
// detects duplicate basenames, and returns resolved skills. Fail-closed.
func ResolveSkills(baseDir string, paths []string) ([]driver.ResolvedSkill, error) {
	if len(paths) == 0 {
		return nil, nil
	}

	absBase, err := filepath.Abs(baseDir)
	if err != nil {
		return nil, fmt.Errorf("skill resolution: cannot resolve base dir %q: %w", baseDir, err)
	}

	seen := make(map[string]string) // basename -> original path (for error messages)
	skills := make([]driver.ResolvedSkill, 0, len(paths))

	for _, p := range paths {
		hostPath, err := filepath.Abs(filepath.Join(baseDir, p))
		if err != nil {
			return nil, fmt.Errorf("skill resolution: cannot resolve path %q: %w", p, err)
		}

		// Path traversal guard
		if !strings.HasPrefix(hostPath, absBase+string(filepath.Separator)) && hostPath != absBase {
			return nil, fmt.Errorf("skill resolution: path %q escapes base directory %q", p, baseDir)
		}

		// File existence check
		if _, err := os.Stat(hostPath); err != nil {
			return nil, fmt.Errorf("skill resolution: file %q not found: %w", hostPath, err)
		}

		// Duplicate basename check
		name := filepath.Base(hostPath)
		if prev, exists := seen[name]; exists {
			return nil, fmt.Errorf("skill resolution: duplicate basename %q (from %q and %q)", name, prev, p)
		}
		seen[name] = p

		skills = append(skills, driver.ResolvedSkill{
			Name:     name,
			HostPath: hostPath,
		})
	}

	return skills, nil
}
