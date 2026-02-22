package pod

import "github.com/mostlydev/clawdapus/internal/driver"

// Pod represents a parsed claw-pod.yml.
type Pod struct {
	Name     string
	Services map[string]*Service
}

// Service represents a service in a claw-pod.yml.
type Service struct {
	Image       string
	Claw        *ClawBlock
	Environment map[string]string
	Expose      []string // ports exposed to other containers (from compose expose:)
	Ports       []string // container-side ports from compose ports: (host:container or plain container)
}

// InvokeEntry is a scheduled agent task declared in the pod x-claw.invoke block.
type InvokeEntry struct {
	Schedule string // 5-field cron expression
	Message  string // agent task payload
	Name     string // optional human-readable job name
	To       string // channel name (resolved to channel ID from handles at compose-up time)
}

// ClawBlock represents the x-claw extension on a service.
type ClawBlock struct {
	Agent    string
	Persona  string
	Cllama   string
	Count    int
	Handles  map[string]*driver.HandleInfo // platform → contact card
	Surfaces []driver.ResolvedSurface
	Skills   []string
	Invoke   []InvokeEntry
}
