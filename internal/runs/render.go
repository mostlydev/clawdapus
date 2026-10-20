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

	workerService = "worker"
	runNetworkRef = "claw-runs"
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
func renderProject(tpl *Template, rec *Record) ([]byte, error) {
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
	doc := map[string]any{
		"name":     rec.Project,
		"services": map[string]any{workerService: svc},
		"networks": map[string]any{
			runNetworkRef: map[string]any{"external": true, "name": tpl.RunNetwork},
		},
	}
	return yaml.Marshal(doc)
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
