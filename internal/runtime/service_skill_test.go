package runtime

import (
	"strings"
	"testing"
)

func TestGenerateServiceSkillFallbackWithPorts(t *testing.T) {
	result := GenerateServiceSkillFallback("api-server", []string{"8080", "9090"})

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
	if !strings.Contains(result, "hostname `api-server`") {
		t.Error("expected usage section with hostname")
	}
}

func TestGenerateServiceSkillFallbackNoPorts(t *testing.T) {
	result := GenerateServiceSkillFallback("fleet-master", nil)

	if !strings.Contains(result, "# fleet-master (service surface)") {
		t.Error("expected header with target name")
	}
	if !strings.Contains(result, "**Hostname:** fleet-master") {
		t.Error("expected hostname")
	}
	if strings.Contains(result, "**Ports:**") {
		t.Error("should not include ports line when no ports available")
	}
}

func TestGenerateServiceSkillFallbackEmptyPorts(t *testing.T) {
	result := GenerateServiceSkillFallback("db", []string{})

	if strings.Contains(result, "**Ports:**") {
		t.Error("should not include ports line for empty ports slice")
	}
}
