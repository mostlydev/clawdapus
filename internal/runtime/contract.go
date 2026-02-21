package runtime

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ContractMount represents a verified agent contract bind mount.
type ContractMount struct {
	HostPath      string
	ContainerPath string
	ReadOnly      bool
}

// ResolveContract validates that the agent file exists and returns the mount spec.
// Fail-closed: missing or empty filename is a hard error.
func ResolveContract(baseDir string, agentFilename string) (*ContractMount, error) {
	if agentFilename == "" {
		return nil, fmt.Errorf("contract enforcement: AGENT filename is empty (no contract, no start)")
	}

	// Prevent path traversal: resolved path must stay within baseDir.
	hostPath := filepath.Clean(filepath.Join(baseDir, agentFilename))
	absBase, _ := filepath.Abs(baseDir)
	if !strings.HasPrefix(hostPath, absBase+string(filepath.Separator)) && hostPath != absBase {
		return nil, fmt.Errorf("contract enforcement: agent path %q escapes base directory %q", agentFilename, baseDir)
	}
	if _, err := os.Stat(hostPath); err != nil {
		return nil, fmt.Errorf("contract enforcement: agent file %q not found: %w (no contract, no start)", hostPath, err)
	}

	return &ContractMount{
		HostPath:      hostPath,
		ContainerPath: filepath.Join("/claw", agentFilename),
		ReadOnly:      true,
	}, nil
}
