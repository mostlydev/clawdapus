package runs

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/mostlydev/clawdapus/internal/driver"
	"gopkg.in/yaml.v3"
)

// Worker container labels. The key hash is the reconciliation handle; the
// others let reconcile verify a container against its ledger record.
const (
	LabelPod        = "claw.pod"
	LabelRole       = "claw.role"
	LabelRunKey     = "claw.run.key"
	LabelInstall    = "claw.run.installation"
	LabelRunID      = "claw.run.id"
	LabelGeneration = "claw.run.generation"
	LabelTemplate   = "claw.run.template"
	LabelTemplateID = "claw.run.template-id"
	LabelDigest     = "claw.run.spec-digest"
	LabelPodNS      = "claw.run.pod-ns"
	RoleRunWorker   = "run-worker"
	// RoleRunGate marks a task-home run's forwarder. It deliberately carries
	// no run-key label, so the supervisor's view of a run stays one container.
	RoleRunGate  = "run-gate"
	LabelGateFor = "claw.run.gate-for"

	workerService = "worker"
	runNetworkRef = "claw-runs"
	// privateNetworkRef is a task-home run's own network: the worker and its
	// gate, and nothing else.
	privateNetworkRef = "run"
	// workerUser is a fixed unprivileged identity; the worker only writes tmpfs.
	workerUser = "65534:65534"
)

var inputNameRe = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

// composeLiteral escapes s so Compose interpolates nothing in it.
func composeLiteral(s string) string { return strings.ReplaceAll(s, "$", "$$") }

// composeWithInputs escapes s except for exact ${NAME} references to declared
// inputs, which Compose interpolates from the supervisor-provided environment.
// Interpolated values are not re-interpolated, so task text containing "$" or
// "${...}" stays literal.
func composeWithInputs(s string, inputs map[string]string) string {
	out := composeLiteral(s)
	for name := range inputs {
		out = strings.ReplaceAll(out, "$${"+name+"}", "${"+name+":-}")
	}
	return out
}

// renderProject renders a run's Compose project. The file contains no secrets
// and no task text: those reach the container only through interpolation of
// the declared inputs at `compose up`.
func renderProject(tpl *Template, rec *Record, homeSource string) ([]byte, error) {
	if (tpl.Worker.Home != nil) != (homeSource != "") {
		return nil, fmt.Errorf("template %q: task home and its mount source must be set together", tpl.Name)
	}
	if tpl.Worker.Home != nil && tpl.Worker.Gate == nil {
		return nil, fmt.Errorf("template %q: a task-home worker needs a gate", tpl.Name)
	}
	if tpl.Worker.Home == nil && (tpl.Worker.Gate != nil || len(tpl.Content) > 0) {
		return nil, fmt.Errorf("template %q: gate and content require a task home", tpl.Name)
	}
	w := tpl.Worker
	for name := range w.Inputs {
		if !inputNameRe.MatchString(name) {
			return nil, fmt.Errorf("template %q: invalid input name %q", tpl.Name, name)
		}
		if _, clash := w.Environment[name]; clash {
			return nil, fmt.Errorf("template %q: input %q also declared as static env", tpl.Name, name)
		}
	}
	if len(w.Entrypoint) == 0 {
		return nil, fmt.Errorf("template %q: worker entrypoint is empty", tpl.Name)
	}
	env := make(map[string]string, len(w.Environment)+len(w.Inputs))
	for k, v := range w.Environment {
		env[k] = composeLiteral(v)
	}
	for name := range w.Inputs {
		env[name] = "${" + name + ":-}"
	}
	entrypoint := make([]string, len(w.Entrypoint))
	for i, s := range w.Entrypoint {
		entrypoint[i] = composeLiteral(s)
	}
	command := make([]string, len(w.Command))
	for i, s := range w.Command {
		command[i] = composeWithInputs(s, w.Inputs)
	}
	tmpfs := make([]string, len(w.Tmpfs))
	for i, s := range w.Tmpfs {
		tmpfs[i] = composeLiteral(s)
	}
	labels := map[string]string{
		LabelPod:        composeLiteral(tpl.Pod),
		LabelRole:       RoleRunWorker,
		LabelRunKey:     rec.RunKey,
		LabelPodNS:      podNamespaceOf(rec),
		LabelInstall:    rec.Key.Installation,
		LabelRunID:      rec.Key.RunID,
		LabelGeneration: strconv.FormatUint(rec.Generation, 10),
		LabelTemplate:   tpl.Name,
		LabelTemplateID: tpl.ID,
		LabelDigest:     rec.SpecDigest,
	}
	svc := map[string]any{
		"image":             tpl.ImageID,
		"pull_policy":       "never",
		"entrypoint":        entrypoint,
		"command":           command,
		"environment":       env,
		"user":              workerUser,
		"read_only":         true,
		"init":              true,
		"cap_drop":          []string{"ALL"},
		"security_opt":      []string{"no-new-privileges:true"},
		"tmpfs":             tmpfs,
		"networks":          []string{runNetworkRef},
		"restart":           "no",
		"stop_grace_period": "10s",
		"labels":            labels,
	}
	if tpl.Limits.Memory != "" {
		svc["mem_limit"] = tpl.Limits.Memory
	}
	if tpl.Limits.Pids > 0 {
		svc["pids_limit"] = tpl.Limits.Pids
	}
	services := map[string]any{workerService: svc}
	networks := map[string]any{
		runNetworkRef: map[string]any{"external": true, "name": tpl.RunNetwork},
	}
	if tpl.Worker.Home != nil {
		volumes := []any{map[string]any{
			"type":   "bind",
			"source": composeLiteral(homeSource),
			"target": composeLiteral(tpl.Worker.Home.Mount),
			"bind":   map[string]any{"create_host_path": false},
		}}
		for _, c := range tpl.Content {
			volumes = append(volumes, map[string]any{
				"type":      "bind",
				"source":    composeLiteral(c.Source),
				"target":    composeLiteral(c.Target),
				"read_only": true,
				"bind":      map[string]any{"create_host_path": false},
			})
		}
		svc["volumes"] = volumes
		// The worker joins only its own network. Its one route out is the
		// gate, which forwards to cllama's API port and nothing else.
		svc["networks"] = []string{privateNetworkRef}
		svc["depends_on"] = []string{driver.GateServiceName}
		svc["logging"] = runLogging(tpl)
		services[driver.GateServiceName] = renderGate(tpl, rec)
		networks[privateNetworkRef] = map[string]any{"internal": true}
	}
	doc := map[string]any{
		"name":     rec.Project,
		"services": services,
		"networks": networks,
	}
	return yaml.Marshal(doc)
}

// runLogging bounds a task-home container's log. A worker with a shell can
// write to the container's stdout directly, so the wrapper's own bound on its
// output is not enough. A flood that rotates the result frame away ends as a
// missing result, never as an inferred answer.
func runLogging(tpl *Template) map[string]any {
	size := 4 * (tpl.Limits.MaxResultBytes + resultPreambleBytes)
	if size < 4<<20 {
		size = 4 << 20
	}
	return map[string]any{
		"driver":  "json-file",
		"options": map[string]string{"max-size": strconv.Itoa(size), "max-file": "1"},
	}
}

// renderGate renders the forwarder: same pinned image, no mounts, no inputs,
// no token. It sits on the run's private network and the pod's run network.
func renderGate(tpl *Template, rec *Record) map[string]any {
	g := tpl.Worker.Gate
	entrypoint := make([]string, len(g.Entrypoint))
	for i, s := range g.Entrypoint {
		entrypoint[i] = composeLiteral(s)
	}
	command := make([]string, len(g.Command))
	for i, s := range g.Command {
		command[i] = composeLiteral(s)
	}
	env := make(map[string]string, len(g.Environment))
	for k, v := range g.Environment {
		env[k] = composeLiteral(v)
	}
	return map[string]any{
		"image":             tpl.ImageID,
		"pull_policy":       "never",
		"entrypoint":        entrypoint,
		"command":           command,
		"environment":       env,
		"user":              workerUser,
		"read_only":         true,
		"init":              true,
		"cap_drop":          []string{"ALL"},
		"security_opt":      []string{"no-new-privileges:true"},
		"networks":          []string{privateNetworkRef, runNetworkRef},
		"restart":           "no",
		"stop_grace_period": "2s",
		"mem_limit":         "128m",
		"pids_limit":        64,
		"logging":           runLogging(tpl),
		"labels": map[string]string{
			LabelPod:     composeLiteral(tpl.Pod),
			LabelRole:    RoleRunGate,
			LabelPodNS:   podNamespaceOf(rec),
			LabelGateFor: rec.RunKey,
		},
	}
}

// inputEnv substitutes run placeholders into the template's inputs. The
// result is passed only to the `compose up` process environment.
func inputEnv(tpl *Template, rec *Record, task, cllamaToken string) map[string]string {
	repl := strings.NewReplacer(
		driver.RunPlaceholderTask, task,
		driver.RunPlaceholderSession, "run-"+rec.RunKey,
		driver.RunPlaceholderCllamaToken, cllamaToken,
	)
	out := make(map[string]string, len(tpl.Worker.Inputs))
	names := make([]string, 0, len(tpl.Worker.Inputs))
	for name := range tpl.Worker.Inputs {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		value := tpl.Worker.Inputs[name]
		if value == driver.RunPlaceholderTask {
			// Exact task inputs bypass the replacer so task text that happens
			// to contain another placeholder is never expanded.
			out[name] = task
			continue
		}
		out[name] = repl.Replace(value)
	}
	return out
}

// podNamespaceOf recovers the pod namespace recorded in a run's project name.
func podNamespaceOf(rec *Record) string {
	parts := strings.Split(rec.Project, "-")
	if len(parts) == 3 {
		return parts[1]
	}
	return ""
}
