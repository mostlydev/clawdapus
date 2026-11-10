package driver

// RunWorkerCompiler is optionally implemented by drivers that can run as a
// one-shot per-run worker (x-claw.lifecycle: per-run). Validation is separate
// from Validate so long-lived service requirements are never weakened.
type RunWorkerCompiler interface {
	ValidateRunWorker(rc *ResolvedClaw) error
	CompileRunWorker(rc *ResolvedClaw, opts RunWorkerOpts) (*RunWorkerSpec, error)
}

// RunProfileSupporter is optionally implemented by a RunWorkerCompiler to say
// which per-run profiles it compiles. A compiler without it supports only
// the stateless profile.
type RunProfileSupporter interface {
	SupportsRunProfile(taskHome bool) bool
}

type RunWorkerOpts struct {
	PodName string
	// Home, when set, compiles the task-home profile: the worker gets one
	// persistent writable directory mounted at Home.Mount.
	Home *RunHomeSpec
	// MaxResultBytes is the template's answer limit, for drivers whose wrapper
	// bounds its own output.
	MaxResultBytes int
}

// RunHomeSpec describes the persistent task home of a task-home worker.
type RunHomeSpec struct {
	// Mount is the absolute container path of the home.
	Mount string `json:"mount"`
	// MaxBytes is a watchdog threshold, not a filesystem quota.
	MaxBytes int64 `json:"max_bytes"`
}

// RunGateSpec is a forwarder service rendered beside a task-home worker. It
// runs from the worker's own image, joins the run's private network and the
// pod's run network, and is the worker's only route to cllama.
type RunGateSpec struct {
	Entrypoint  []string          `json:"entrypoint"`
	Command     []string          `json:"command,omitempty"`
	Environment map[string]string `json:"environment,omitempty"`
}

// GateServiceName is the Compose service name, and so the DNS name, of the
// forwarder inside a task-home run project.
const GateServiceName = "gate"

const MeterServiceName = "meter"

// Run placeholders are substituted by the run supervisor at launch. They only
// ever appear as complete values inside RunWorkerSpec.Inputs.
const (
	RunPlaceholderTask        = "{{claw.run.task}}"
	RunPlaceholderSession     = "{{claw.run.session}}"
	RunPlaceholderCllamaToken = "{{claw.run.cllama_token}}"
)

// RunWorkerSpec is the secret-free, driver-compiled process contract for a
// per-run worker. The supervisor never interprets driver configuration: it
// substitutes placeholders into Inputs and passes each input to the container
// through Compose interpolation, so no task text or secret is written into the
// rendered project file.
type RunWorkerSpec struct {
	// Entrypoint and Command are pinned; the image ENTRYPOINT is overridden.
	// Command elements may reference inputs as ${NAME} Compose interpolations.
	Entrypoint []string `json:"entrypoint"`
	Command    []string `json:"command,omitempty"`
	// Environment holds static, non-secret container env.
	Environment map[string]string `json:"environment,omitempty"`
	// Inputs maps a container env name to a value template. Templates may embed
	// run placeholders. Inputs are interpolated, never written to disk.
	Inputs map[string]string `json:"inputs"`
	// Tmpfs mounts are the only writable paths; the root filesystem is read-only.
	Tmpfs []string `json:"tmpfs"`
	// Result describes where the worker reports its answer.
	Result RunResultContract `json:"result"`
	// Home and Gate are set only by the task-home profile. Both are omitted
	// when empty, so a stateless template's identity does not change.
	Home *RunHomeSpec `json:"home,omitempty"`
	Gate *RunGateSpec `json:"gate,omitempty"`
	// Meter is a networkless, read-only home-size watchdog.
	Meter *RunGateSpec `json:"meter,omitempty"`
}

// RunResultContract describes how the supervisor extracts a worker's answer.
type RunResultContract struct {
	// Source is "stdout".
	Source string `json:"source"`
	// Marker, when set, starts the answer: the answer is everything after the
	// first occurrence of Marker in the stream, minus one trailing newline.
	Marker string `json:"marker,omitempty"`
	// Header, when RunResultHeaderJSONLine, means the first line after Marker
	// is a JSON object of at most 1024 bytes describing the result; the answer
	// is everything after that line.
	Header string `json:"header,omitempty"`
}

// RunResultHeaderJSONLine selects the one-line JSON result header.
const RunResultHeaderJSONLine = "json-line"
