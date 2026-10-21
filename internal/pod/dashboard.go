package pod

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

// NormalizeDashboardBind accepts a literal host publication IP. Empty keeps
// Docker's existing default; brackets are accepted for IPv6 configuration.
func NormalizeDashboardBind(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	if strings.HasPrefix(raw, "[") && strings.HasSuffix(raw, "]") {
		raw = raw[1 : len(raw)-1]
	}
	ip := net.ParseIP(raw)
	if ip == nil {
		return "", fmt.Errorf("CLAW_DASHBOARD_BIND must be a literal IPv4 or IPv6 address")
	}
	return ip.String(), nil
}

func dashboardPortMapping(bind, hostPort, containerPort string) (string, error) {
	ip, err := NormalizeDashboardBind(bind)
	if err != nil {
		return "", err
	}
	if ip == "" {
		return hostPort + ":" + containerPort, nil
	}
	return net.JoinHostPort(ip, hostPort) + ":" + containerPort, nil
}

// DashboardURL is reachable from the host even when publication is wildcard.
// Callers validate bind using NormalizeDashboardBind before compilation.
func DashboardURL(bind, port string) string {
	host := bind
	switch host {
	case "", "0.0.0.0":
		host = "127.0.0.1"
	case "::":
		host = "::1"
	}
	return "http://" + net.JoinHostPort(host, port)
}

// ClawdashListenAddress controls the container listener, not host publication.
// A loopback container listener cannot receive Docker's forwarded requests.
func ClawdashListenAddress(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ":8082", nil
	}
	if !strings.Contains(raw, ":") {
		raw = ":" + raw
	}
	host, port, err := net.SplitHostPort(raw)
	if err != nil {
		return "", fmt.Errorf("CLAWDASH_ADDR must be a wildcard container listen address and port: %w", err)
	}
	if host != "" {
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsUnspecified() {
			return "", fmt.Errorf("CLAWDASH_ADDR must listen on a wildcard container address; use CLAW_DASHBOARD_BIND for host publication")
		}
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return "", fmt.Errorf("CLAWDASH_ADDR port must be between 1 and 65535")
	}
	return net.JoinHostPort(host, strconv.Itoa(n)), nil
}
