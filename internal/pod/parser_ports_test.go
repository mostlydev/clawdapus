package pod

import (
	"strings"
	"testing"
)

func TestParsePodExtractsPorts(t *testing.T) {
	const yaml = `
x-claw:
  pod: ports-pod
services:
  api-server:
    image: nginx:alpine
    ports:
      - "8080:80"
      - "9443:443"
`
	p, err := Parse(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	svc := p.Services["api-server"]
	if len(svc.Ports) != 2 {
		t.Fatalf("expected 2 ports, got %d: %v", len(svc.Ports), svc.Ports)
	}
	if svc.Ports[0] != "80" {
		t.Errorf("expected container port '80', got %q", svc.Ports[0])
	}
	if svc.Ports[1] != "443" {
		t.Errorf("expected container port '443', got %q", svc.Ports[1])
	}
}

func TestParsePodPortsPlainContainerPort(t *testing.T) {
	const yaml = `
x-claw:
  pod: ports-pod
services:
  api-server:
    image: nginx:alpine
    ports:
      - "80"
`
	p, err := Parse(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Services["api-server"].Ports[0] != "80" {
		t.Errorf("expected '80', got %q", p.Services["api-server"].Ports[0])
	}
}

func TestParsePodPortsIPColonForm(t *testing.T) {
	const yaml = `
x-claw:
  pod: ports-pod
services:
  api-server:
    image: nginx:alpine
    ports:
      - "127.0.0.1:8080:80"
`
	p, err := Parse(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Services["api-server"].Ports[0] != "80" {
		t.Errorf("expected container port '80', got %q", p.Services["api-server"].Ports[0])
	}
}

func TestParsePodPortsProtocolSuffixStripped(t *testing.T) {
	const yaml = `
x-claw:
  pod: ports-pod
services:
  api-server:
    image: nginx:alpine
    ports:
      - "8080:80/tcp"
      - "9090:90/udp"
`
	p, err := Parse(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Services["api-server"].Ports[0] != "80" {
		t.Errorf("expected '80' (no /tcp), got %q", p.Services["api-server"].Ports[0])
	}
	if p.Services["api-server"].Ports[1] != "90" {
		t.Errorf("expected '90' (no /udp), got %q", p.Services["api-server"].Ports[1])
	}
}

func TestParsePodPortsNumericEntry(t *testing.T) {
	const yaml = `
x-claw:
  pod: ports-pod
services:
  api-server:
    image: nginx:alpine
    ports:
      - 80
`
	p, err := Parse(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Services["api-server"].Ports[0] != "80" {
		t.Errorf("expected '80', got %q", p.Services["api-server"].Ports[0])
	}
}

func TestParsePodPortsMapForm(t *testing.T) {
	const yaml = `
x-claw:
  pod: ports-pod
services:
  api-server:
    image: nginx:alpine
    ports:
      - target: 80
        published: 8080
`
	p, err := Parse(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Services["api-server"].Ports[0] != "80" {
		t.Errorf("expected container port '80' from map form, got %q", p.Services["api-server"].Ports[0])
	}
}

func TestParsePodPortsDefaultsEmpty(t *testing.T) {
	const yaml = `
x-claw:
  pod: ports-pod
services:
  api-server:
    image: nginx:alpine
`
	p, err := Parse(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Services["api-server"].Ports == nil {
		t.Error("expected non-nil Ports slice when no ports declared")
	}
	if len(p.Services["api-server"].Ports) != 0 {
		t.Errorf("expected empty Ports slice, got %v", p.Services["api-server"].Ports)
	}
}

func TestContainerPortFromString(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"8080:80", "80"},
		{"80", "80"},
		{"127.0.0.1:8080:80", "80"},
		{"8080:80/tcp", "80"},
		{"80/udp", "80"},
		{"127.0.0.1:8080:80/tcp", "80"},
	}
	for _, tc := range cases {
		got := containerPortFromString(tc.in)
		if got != tc.want {
			t.Errorf("containerPortFromString(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
