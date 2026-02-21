package openclaw

import (
	"strings"
	"testing"

	"github.com/mostlydev/clawdapus/internal/driver"
)

func TestGenerateServiceSkillWithPorts(t *testing.T) {
	surface := driver.ResolvedSurface{
		Scheme: "service",
		Target: "api-server",
		Ports:  []string{"8080", "9090"},
	}

	result := GenerateServiceSkill(surface)

	if !strings.Contains(result, "# api-server (service surface)") {
		t.Error("expected header with target name")
	}
	if !strings.Contains(result, "**Hostname:** api-server") {
		t.Error("expected hostname")
	}
	if !strings.Contains(result, "**Ports:** 8080, 9090") {
		t.Error("expected ports list")
	}
	if !strings.Contains(result, "claw-internal") {
		t.Error("expected network name")
	}
}

func TestGenerateServiceSkillWithoutPorts(t *testing.T) {
	surface := driver.ResolvedSurface{
		Scheme: "service",
		Target: "fleet-master",
	}

	result := GenerateServiceSkill(surface)

	if !strings.Contains(result, "**Hostname:** fleet-master") {
		t.Error("expected hostname")
	}
	if strings.Contains(result, "**Ports:**") {
		t.Error("should not include ports line when no ports available")
	}
}

func TestGenerateServiceSkillHostnameInUsage(t *testing.T) {
	surface := driver.ResolvedSurface{
		Scheme: "service",
		Target: "redis-cache",
		Ports:  []string{"6379"},
	}

	result := GenerateServiceSkill(surface)

	if !strings.Contains(result, "hostname `redis-cache`") {
		t.Error("expected hostname in usage section")
	}
}
