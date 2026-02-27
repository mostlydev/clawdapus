package cllama

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

// GenerateToken returns a bearer token in "<agent-id>:<48-hex-secret>" format.
func GenerateToken(agentID string) string {
	b := make([]byte, 24) // 48 hex chars
	_, _ = rand.Read(b)
	return fmt.Sprintf("%s:%s", agentID, hex.EncodeToString(b))
}
