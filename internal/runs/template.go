// Package runs implements per-run worker templates and the run supervisor
// behind `claw runs serve` (issue #372).
//
// A template is compiled by `claw up` from a pod service declared with
// x-claw.lifecycle: per-run. The supervisor instantiates it once per run as a
// separate Compose project; Compose remains the only lifecycle writer.
package runs

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/mostlydev/clawdapus/internal/cllama"
	"github.com/mostlydev/clawdapus/internal/driver"
	"regexp"
	"strings"
)

const (
	// StateDirName is the pod-relative directory holding templates, the ledger,
	// rendered projects and the pod lock. claw up never rotates it.
	StateDirName = ".claw-runs"
	// RevocationDirName is the pod-relative proxy-owned revocation ledger dir.
	RevocationDirName = ".claw-revocations"

	templateVersion = 1
)

// Limits bound every run instantiated from a template.
type Limits struct {
	MaxRuntimeSeconds   int64  `json:"max_runtime_seconds"`
	MaxAuthoritySeconds int64  `json:"max_authority_seconds"`
	MaxTaskBytes        int    `json:"max_task_bytes"`
	MaxResultBytes      int    `json:"max_result_bytes"`
	Memory              string `json:"memory"`
	Pids                int    `json:"pids"`
}

func (l Limits) MaxRuntime() time.Duration { return time.Duration(l.MaxRuntimeSeconds) * time.Second }
func (l Limits) MaxAuthority() time.Duration {
	return time.Duration(l.MaxAuthoritySeconds) * time.Second
}

// Template is the compiled, claw up-owned description of a per-run worker.
// The caller of a launch only selects within it.
type Template struct {
	Version  int    `json:"version"`
	ID       string `json:"template_id"`
	Name     string `json:"template"`
	Pod      string `json:"pod"`
	ClawType string `json:"claw_type"`
	// ImageRef is the reference declared in the pod; ImageID pins the exact
	// image resolved at claw up.
	ImageRef     string `json:"image_ref"`
	ImageID      string `json:"image_id"`
	RunNetwork   string `json:"run_network"`
	CllamaURL    string `json:"cllama_url"`
	CllamaAPIURL string `json:"cllama_api_url"`
	// ToolsGrant is the sorted set of managed tool names a run may enable.
	ToolsGrant []string `json:"tools_grant"`
	// ContextDigest binds the cllama context (tools, models, policy, rules,
	// with credentials redacted) into the template identity.
	ContextDigest string               `json:"context_digest"`
	Limits        Limits               `json:"limits"`
	Worker        driver.RunWorkerSpec `json:"worker"`
	// Content lists read-only mounts compiled for a task-home template. It is
	// omitted when empty so a stateless template's identity does not change.
	Content []ContentMount `json:"content,omitempty"`
	// RequestedModels is aligned with the compiled model chain: for each
	// entry, the model family reference the pod wrote, or "" when it named
	// an exact model. It is omitted when no entry came from a family, so a
	// template of exact models keeps its bytes and its ID.
	RequestedModels []string `json:"requested_models,omitempty"`
}

// familyRefRe is a model family reference as a pod writes it.
var familyRefRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*/@[a-z][a-z0-9-]*$`)

// ContentMount is one compiled read-only reference mount. Source is an
// absolute host path resolved and checked by claw up.
type ContentMount struct {
	Source string `json:"source"`
	Target string `json:"target"`
}

// computeID digests every field except the ID itself.
func (t *Template) computeID() (string, error) {
	clone := *t
	clone.ID = ""
	raw, err := json.Marshal(clone)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// ContextTemplate is the cllama context compiled for a template. It carries
// managed tool credentials, so it is stored 0600 and never mounted into a
// worker. Each run receives a copy with its own identity and allowlist.
type ContextTemplate struct {
	Input cllama.AgentContextInput `json:"input"`
}

// digest hashes the context with every credential value redacted, so token
// rotation does not change the template identity but any change to tools,
// endpoints, models, policy or rules does. It digests the JSON-normalized
// form, so a template compiled in memory and one loaded from disk agree.
func (c *ContextTemplate) digest() (string, error) {
	raw, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	var normalized ContextTemplate
	if err := json.Unmarshal(raw, &normalized); err != nil {
		return "", err
	}
	in := normalized.Input
	in.Metadata = make(map[string]interface{}, len(normalized.Input.Metadata))
	for k, v := range normalized.Input.Metadata {
		if k != "token" {
			in.Metadata[k] = v
		}
	}
	in.Tools = append([]cllama.ToolManifestEntry(nil), normalized.Input.Tools...)
	for i := range in.Tools {
		if a := in.Tools[i].Execution.Auth; a != nil {
			redacted := *a
			redacted.Token = ""
			in.Tools[i].Execution.Auth = &redacted
		}
	}
	in.Feeds = append([]cllama.FeedManifestEntry(nil), normalized.Input.Feeds...)
	for i := range in.Feeds {
		if in.Feeds[i].Auth != "" {
			in.Feeds[i].Auth = "redacted"
		}
	}
	in.ServiceAuth = append([]cllama.ServiceAuthEntry(nil), normalized.Input.ServiceAuth...)
	for i := range in.ServiceAuth {
		in.ServiceAuth[i].Token = ""
	}
	if in.Memory != nil {
		m := *in.Memory
		if m.Auth != nil {
			redacted := *m.Auth
			redacted.Token = ""
			m.Auth = &redacted
		}
		in.Memory = &m
	}
	out, err := json.Marshal(in)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(out)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// TemplateDir returns the pod's template directory.
func TemplateDir(podDir string) string {
	return filepath.Join(podDir, StateDirName, "templates")
}

// WriteTemplates replaces the pod's compiled templates atomically. Callers
// must hold the pod lock.
func WriteTemplates(podDir string, templates []*Template, contexts map[string]*ContextTemplate) error {
	base := filepath.Join(podDir, StateDirName)
	if err := os.MkdirAll(base, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", base, err)
	}
	stage, err := os.MkdirTemp(base, ".templates-")
	if err != nil {
		return fmt.Errorf("stage templates: %w", err)
	}
	defer os.RemoveAll(stage)
	for _, tpl := range templates {
		tpl.Version = templateVersion
		sort.Strings(tpl.ToolsGrant)
		ctx := contexts[tpl.Name]
		if ctx == nil {
			return fmt.Errorf("template %q: missing cllama context", tpl.Name)
		}
		digest, err := ctx.digest()
		if err != nil {
			return fmt.Errorf("template %q: context digest: %w", tpl.Name, err)
		}
		tpl.ContextDigest = digest
		id, err := tpl.computeID()
		if err != nil {
			return fmt.Errorf("template %q: digest: %w", tpl.Name, err)
		}
		tpl.ID = id
		if err := writeJSONFile(filepath.Join(stage, tpl.Name+".json"), tpl); err != nil {
			return fmt.Errorf("template %q: %w", tpl.Name, err)
		}
		if err := writeJSONFile(filepath.Join(stage, tpl.Name+".context.json"), ctx); err != nil {
			return fmt.Errorf("template %q: context: %w", tpl.Name, err)
		}
	}
	final := TemplateDir(podDir)
	old := final + ".old"
	_ = os.RemoveAll(old)
	if _, err := os.Stat(final); err == nil {
		if err := os.Rename(final, old); err != nil {
			return fmt.Errorf("rotate templates: %w", err)
		}
	}
	if err := os.Rename(stage, final); err != nil {
		_ = os.Rename(old, final)
		return fmt.Errorf("install templates: %w", err)
	}
	_ = os.RemoveAll(old)
	return syncDir(base)
}

// LoadTemplate reads and verifies one compiled template.
func LoadTemplate(podDir, name string) (*Template, *ContextTemplate, error) {
	if !validName(name) {
		return nil, nil, fmt.Errorf("invalid template name %q", name)
	}
	dir := TemplateDir(podDir)
	var tpl Template
	if err := readJSONFile(filepath.Join(dir, name+".json"), &tpl); err != nil {
		return nil, nil, err
	}
	if tpl.Version != templateVersion || tpl.Name != name {
		return nil, nil, fmt.Errorf("template %q: unsupported or mismatched template file", name)
	}
	id, err := tpl.computeID()
	if err != nil {
		return nil, nil, err
	}
	if id != tpl.ID {
		return nil, nil, fmt.Errorf("template %q: digest mismatch; rerun claw up", name)
	}
	var ctx ContextTemplate
	if err := readJSONFile(filepath.Join(dir, name+".context.json"), &ctx); err != nil {
		return nil, nil, err
	}
	ctxDigest, err := ctx.digest()
	if err != nil {
		return nil, nil, err
	}
	if ctxDigest != tpl.ContextDigest {
		return nil, nil, fmt.Errorf("template %q: context digest mismatch; rerun claw up", name)
	}
	if err := validRequestedModels(tpl.RequestedModels, contextModels(&ctx)); err != nil {
		return nil, nil, fmt.Errorf("template %q: %v; rerun claw up", name, err)
	}
	return &tpl, &ctx, nil
}

// templateModels returns the concrete model chain compiled into a template's
// context, in routing order. It is empty when the template declares none.
func templateModels(tpl *Template, ctx *ContextTemplate) []ModelRef {
	models := contextModels(ctx)
	if tpl == nil || len(tpl.RequestedModels) == 0 {
		return models
	}
	// LoadTemplate has checked the list against the chain. A mismatch here
	// labels nothing: a requested name is never attached to the wrong model.
	if len(tpl.RequestedModels) != len(models) {
		return models
	}
	out := make([]ModelRef, len(models))
	for i, m := range models {
		m.Requested = tpl.RequestedModels[i]
		out[i] = m
	}
	return out
}

// validRequestedModels checks a template's requested-model list against its
// compiled chain: one entry per chain entry, each empty or a family
// reference whose provider is the provider of the model it resolved to.
func validRequestedModels(requested []string, chain []ModelRef) error {
	if len(requested) == 0 {
		return nil
	}
	if len(requested) != len(chain) {
		return fmt.Errorf("requested_models has %d entries for a chain of %d", len(requested), len(chain))
	}
	named := false
	for i, ref := range requested {
		if ref == "" {
			continue
		}
		named = true
		provider, _, _ := strings.Cut(chain[i].Ref, "/")
		if !familyRefRe.MatchString(ref) || !strings.HasPrefix(ref, provider+"/@") {
			return fmt.Errorf("requested_models entry %d is not a family reference for its model's provider", i)
		}
	}
	if !named {
		return fmt.Errorf("requested_models names no family")
	}
	return nil
}

func contextModels(ctx *ContextTemplate) []ModelRef {
	if ctx == nil {
		return nil
	}
	raw, err := json.Marshal(ctx.Input.Metadata["model_policy"])
	if err != nil {
		return nil
	}
	// Only the slot and the concrete reference are read from the context. A
	// requested name comes from the verified template and nowhere else.
	var policy struct {
		Allowed []struct {
			Slot string `json:"slot"`
			Ref  string `json:"ref"`
		} `json:"allowed"`
	}
	if json.Unmarshal(raw, &policy) != nil {
		return nil
	}
	if policy.Allowed == nil {
		return nil
	}
	out := make([]ModelRef, len(policy.Allowed))
	for i, m := range policy.Allowed {
		out[i] = ModelRef{Slot: m.Slot, Ref: m.Ref}
	}
	return out
}

// TemplateInfo is what an orchestrator may know about a compiled template:
// its identity and the concrete models it routes to. It carries no
// credential, no context body and no task content.
type TemplateInfo struct {
	Name          string     `json:"name"`
	ID            string     `json:"id"`
	ContextDigest string     `json:"context_digest"`
	Models        []ModelRef `json:"models"`
	// Profile is "task_home" for a template whose runs resume a home and
	// "stateless" otherwise.
	Profile string `json:"profile"`
	// ContentTargets are the in-worker paths of a task-home template's
	// read-only reference mounts. Host paths are not listed.
	ContentTargets []string `json:"content_targets,omitempty"`
	// DataPolicy summarizes what cllama enforces for the template's model
	// calls. It is absent when the template compiled none.
	DataPolicy *DataPolicySummary `json:"data_policy,omitempty"`
}

// DataPolicySummary is the data-handling policy compiled for a template.
// Attestations are named by provider only; their endpoints are not listed.
type DataPolicySummary struct {
	Regions           []string `json:"regions"`
	Training          string   `json:"training"`
	Retention         string   `json:"retention"`
	LocalRetention    string   `json:"local_retention"`
	AttestedProviders []string `json:"attested_providers"`
}

const (
	ProfileStateless = "stateless"
	ProfileTaskHome  = "task_home"
)

func templateDataPolicy(ctx *ContextTemplate) *DataPolicySummary {
	if ctx == nil || ctx.Input.Metadata["data_policy"] == nil {
		return nil
	}
	raw, err := json.Marshal(ctx.Input.Metadata["data_policy"])
	if err != nil {
		return nil
	}
	var policy cllama.DataPolicy
	if json.Unmarshal(raw, &policy) != nil {
		return nil
	}
	out := &DataPolicySummary{
		Regions: append([]string{}, policy.Regions...), Training: policy.Training,
		Retention: policy.Retention, LocalRetention: policy.LocalRetention, AttestedProviders: []string{},
	}
	seen := map[string]bool{}
	for _, a := range policy.Attestations {
		if a.Provider != "" && !seen[a.Provider] {
			seen[a.Provider] = true
			out.AttestedProviders = append(out.AttestedProviders, a.Provider)
		}
	}
	sort.Strings(out.AttestedProviders)
	return out
}

// DescribeTemplates lists the compiled templates that pass their digest
// checks, and names those that do not. A template is described only from
// files that verify against its own ID and context digest.
func DescribeTemplates(podDir string) (valid []TemplateInfo, invalid []string, err error) {
	names, err := ListTemplates(podDir)
	if err != nil {
		return nil, nil, err
	}
	valid, invalid = []TemplateInfo{}, []string{}
	for _, name := range names {
		tpl, ctx, err := LoadTemplate(podDir, name)
		if err != nil {
			invalid = append(invalid, name)
			continue
		}
		models := templateModels(tpl, ctx)
		if models == nil {
			models = []ModelRef{}
		}
		info := TemplateInfo{Name: tpl.Name, ID: tpl.ID, ContextDigest: tpl.ContextDigest, Models: models,
			Profile: ProfileStateless, DataPolicy: templateDataPolicy(ctx)}
		if tpl.Worker.Home != nil {
			info.Profile = ProfileTaskHome
		}
		for _, mount := range tpl.Content {
			info.ContentTargets = append(info.ContentTargets, mount.Target)
		}
		valid = append(valid, info)
	}
	return valid, invalid, nil
}

// ListTemplates returns the names of compiled templates.
func ListTemplates(podDir string) ([]string, error) {
	entries, err := os.ReadDir(TemplateDir(podDir))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		name := e.Name()
		if e.Type().IsRegular() && filepath.Ext(name) == ".json" && filepath.Ext(name[:len(name)-5]) != ".context" {
			names = append(names, name[:len(name)-5])
		}
	}
	sort.Strings(names)
	return names, nil
}
