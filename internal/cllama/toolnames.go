package cllama

import (
	"crypto/sha1"
	"encoding/hex"
	"strings"
)

const (
	maxManagedToolPresentedNameLen = 128
	managedToolAliasHashBytes      = 4
)

// PresentedToolName must match cllama/internal/proxy/toolnames.go so generated
// CLAWDAPUS.md shows the same managed tool name the provider schema exposes.
func PresentedToolName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "managed_tool"
	}
	if isProviderSafeToolName(name) {
		return name
	}

	safe := sanitizeManagedToolName(name)
	if safe == "" {
		safe = "managed_tool"
	}

	sum := sha1.Sum([]byte(name))
	suffix := "_" + hex.EncodeToString(sum[:managedToolAliasHashBytes])
	maxBaseLen := maxManagedToolPresentedNameLen - len(suffix)
	if maxBaseLen < 1 {
		maxBaseLen = 1
	}
	if len(safe) > maxBaseLen {
		safe = safe[:maxBaseLen]
	}
	return safe + suffix
}

func isProviderSafeToolName(name string) bool {
	if len(name) < 1 || len(name) > maxManagedToolPresentedNameLen {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '_' || r == '-':
		default:
			return false
		}
	}
	return true
}

func sanitizeManagedToolName(name string) string {
	var b strings.Builder
	b.Grow(len(name))
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '_' || r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return strings.Trim(b.String(), "_")
}
