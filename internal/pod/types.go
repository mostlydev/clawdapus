package pod

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
}

// ClawBlock represents the x-claw extension on a service.
type ClawBlock struct {
	Agent    string
	Persona  string
	Cllama   string
	Count    int
	Surfaces []string
}
