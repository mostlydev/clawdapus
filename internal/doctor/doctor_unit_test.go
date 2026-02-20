package doctor

import (
	"errors"
	"fmt"
	"testing"
)

func TestRunAllWithRunner_OK(t *testing.T) {
	run := func(name string, args ...string) ([]byte, error) {
		switch signature(name, args...) {
		case "docker version --format {{.Client.Version}}":
			return []byte("26.1.4\n"), nil
		case "docker buildx version":
			return []byte("github.com/docker/buildx v0.14.0\n"), nil
		case "docker compose version --short":
			return []byte("2.27.1\n"), nil
		default:
			return nil, fmt.Errorf("unexpected command: %s %v", name, args)
		}
	}

	results := RunAllWithRunner(run)
	if len(results) != 3 {
		t.Fatalf("expected 3 checks, got %d", len(results))
	}
	for _, result := range results {
		if !result.OK {
			t.Fatalf("expected %s check to pass, got %#v", result.Name, result)
		}
		if result.Version == "" {
			t.Fatalf("expected %s version", result.Name)
		}
	}
}

func TestRunAllWithRunner_Fail(t *testing.T) {
	run := func(name string, args ...string) ([]byte, error) {
		if signature(name, args...) == "docker compose version --short" {
			return []byte("missing plugin"), errors.New("exit status 1")
		}
		return []byte("ok"), nil
	}

	results := RunAllWithRunner(run)
	if len(results) != 3 {
		t.Fatalf("expected 3 checks, got %d", len(results))
	}

	var compose CheckResult
	for _, result := range results {
		if result.Name == "compose" {
			compose = result
			break
		}
	}

	if compose.OK {
		t.Fatalf("expected compose check to fail, got %#v", compose)
	}
	if compose.Detail == "" {
		t.Fatal("expected compose detail message")
	}
}

func signature(name string, args ...string) string {
	if len(args) == 0 {
		return name
	}
	return name + " " + joinArgs(args)
}

func joinArgs(args []string) string {
	out := ""
	for i, arg := range args {
		if i > 0 {
			out += " "
		}
		out += arg
	}
	return out
}
