package driver

// RunWorkerCompiler is optionally implemented by drivers that can run as a
// one-shot per-run worker (x-claw.lifecycle: per-run). Validation is separate
// from Validate so long-lived service requirements are never weakened.
type RunWorkerCompiler interface {
	ValidateRunWorker(rc *ResolvedClaw) error
	CompileRunWorker(rc *ResolvedClaw, opts RunWorkerOpts) (*RunWorkerSpec, error)
}

type RunWorkerOpts struct {
	PodName string
}

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
}

// RunResultContract describes how the supervisor extracts a worker's answer.
type RunResultContract struct {
	// Source is "stdout".
	Source string `json:"source"`
	// Marker, when set, starts the answer: the answer is everything after the
	// first occurrence of Marker in the stream, minus one trailing newline.
	Marker string `json:"marker,omitempty"`
}
