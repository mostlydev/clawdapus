package clawfile

// ClawConfig stores Claw-specific directives parsed from a Clawfile.
type ClawConfig struct {
	ClawType    string
	Agent       string
	Models      map[string]string
	Cllama      string
	Persona     string
	Surfaces    []Surface
	Skills      []string
	Invocations []Invocation
	Privileges  map[string]string
	Configures  []string
	Tracks      []string
}

type Surface struct {
	Raw        string
	Scheme     string
	Target     string
	AccessMode string
}

type Invocation struct {
	Schedule string
	Command  string
}

func NewClawConfig() *ClawConfig {
	return &ClawConfig{
		Models:      make(map[string]string),
		Surfaces:    make([]Surface, 0),
		Skills:      make([]string, 0),
		Invocations: make([]Invocation, 0),
		Privileges:  make(map[string]string),
		Configures:  make([]string, 0),
		Tracks:      make([]string, 0),
	}
}
