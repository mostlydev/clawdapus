package cllama

import (
	"fmt"
	"strings"
)

const passthroughProxyType = "passthrough"

// ProxyType normalizes a proxy type token.
func ProxyType(proxyType string) string {
	normalized := strings.ToLower(strings.TrimSpace(proxyType))
	if normalized == "" {
		return passthroughProxyType
	}
	return normalized
}

// ProxyServiceName returns the compose service name for a proxy type.
// Passthrough is the canonical default and maps to "cllama".
func ProxyServiceName(proxyType string) string {
	pt := ProxyType(proxyType)
	if pt == passthroughProxyType {
		return "cllama"
	}
	return "cllama-" + pt
}

// ProxyImageRef returns the default image reference for a proxy type.
// Passthrough uses the consolidated "ghcr.io/mostlydev/cllama:latest" image.
func ProxyImageRef(proxyType string) string {
	pt := ProxyType(proxyType)
	if pt == passthroughProxyType {
		return "ghcr.io/mostlydev/cllama:latest"
	}
	return fmt.Sprintf("ghcr.io/mostlydev/cllama-%s:latest", pt)
}

// ProxyHealthcheckBinary returns the binary path used by compose healthchecks.
func ProxyHealthcheckBinary(proxyType string) string {
	pt := ProxyType(proxyType)
	if pt == passthroughProxyType {
		return "/cllama"
	}
	return fmt.Sprintf("/cllama-%s", pt)
}

// ProxyBaseURL returns the API base URL for a proxy inside the compose network.
func ProxyBaseURL(proxyType string) string {
	return fmt.Sprintf("http://%s:8080/v1", ProxyServiceName(proxyType))
}
