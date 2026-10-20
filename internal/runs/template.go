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
// endpoints, models, policy or rules does.
func (c *ContextTemplate) digest() (string, error) {
	in := c.Input
	in.Metadata = make(map[string]interface{}, len(c.Input.Metadata))
	for k, v := range c.Input.Metadata {
		if k != "token" {
			in.Metadata[k] = v
		}
	}
	in.Tools = append([]cllama.ToolManifestEntry(nil), c.Input.Tools...)
	for i := range in.Tools {
		if a := in.Tools[i].Execution.Auth; a != nil {
			redacted := *a
			redacted.Token = ""
			in.Tools[i].Execution.Auth = &redacted
		}
	}
	in.Feeds = append([]cllama.FeedManifestEntry(nil), c.Input.Feeds...)
	for i := range in.Feeds {
		if in.Feeds[i].Auth != "" {
			in.Feeds[i].Auth = "redacted"
		}
	}
	in.ServiceAuth = append([]cllama.ServiceAuthEntry(nil), c.Input.ServiceAuth...)
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
	raw, err := json.Marshal(in)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
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
	return &tpl, &ctx, nil
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
