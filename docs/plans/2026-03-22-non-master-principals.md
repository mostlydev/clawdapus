# Non-Master Principal Declaration — Implementation Plan

**Goal:** Extend the claw-api principal model to auto-generate scoped principals from explicit pod signals and support operator-declared principals for custom scopes.

**Issue:** #80
**Depends on:** ADR-015 (claw-api Authentication and Scoping)
**Enables:** #78 (write plane operations)

---

## Design

### Core Principle: Authority is Explicit

ADR-015 establishes that surfaces grant **reachability** (network topology), not **authority**. A service declaring `surfaces: [service://claw-api]` gets network access to claw-api — it does not automatically get a bearer token. These are separate concerns.

The auth signal is a new pod-level `x-claw` field: `claw-api: self`. This is explicit and extensible:

```yaml
services:
  analyst:
    x-claw:
      claw-api: self            # authority signal
      surfaces:
        - service://claw-api   # topology (reachability)
```

Future modes: `claw-api: { principal: read }`, `claw-api: { principal: write }`. For now only `self` is implemented.

### Auto-Generated Principals

**Master principal** (when `x-claw.master` is set):
- Name: the master service name
- Verbs: all read + write verbs (full access — the master governs)
- Scope: pod-wide (`pods: [podName]`)
- Token injected as `CLAW_API_URL` + `CLAW_API_TOKEN` on the master service
- Token injected into cllama service auth for feed fetching

**Self principal** (when a non-master service declares `claw-api: self`):
- Name: the base service name
- Verbs: all read verbs only
- Scope: `services: [baseName]` — covers all ordinals of a counted service (service-wide self-visibility, not per-ordinal)
- Token injected as `CLAW_API_URL` + `CLAW_API_TOKEN` on the service
- Skipped if the service is the master (master already has full access)

Self principals give agents read access to their own telemetry without requiring a master claw.

### Explicit Declaration

For custom scopes beyond auto-generation (dashboards, CI pipelines, cross-service visibility):

```yaml
x-claw:
  pod: trading-desk
  master: sentinel
  principals:
    - name: dashboard
      verbs: [fleet.status, fleet.query_metrics, fleet.alerts]
      scope: pod
    - name: ci-pipeline
      verbs: [fleet.status, fleet.logs]
      services: [worker-*]
      inject-into: ci-runner
```

**Field definitions:**

| Field | Required | Description |
|-------|----------|-------------|
| `name` | Yes | Principal identity. If it matches an auto-generated name, overrides it. |
| `verbs` | Yes | List of allowed verbs. Validated against the known verb set at parse time. |
| `scope` | No | `pod` — shorthand for pod-wide access. Mutually exclusive with `services`/`claw_ids`/`compose_services`. |
| `services` | No | Glob patterns against base service names. |
| `claw_ids` | No | Glob patterns against claw IDs (ordinal names). |
| `compose_services` | No | Glob patterns against compose service names (e.g. `worker-0`). Required for ordinal-targeted write operations. |
| `inject-into` | No | Service name to receive `CLAW_API_URL` + `CLAW_API_TOKEN` env vars. |

**Constraints:**
- `scope: pod` is mutually exclusive with `services`, `claw_ids`, `compose_services`.
- `inject-into` must reference a service defined in the pod.
- If two different principals (not a same-name override) both target the same service via `inject-into`, `claw up` fails with a hard error.
- Verbs are validated against the known set at parse time — unknown verbs are a parse error.
- Tokens are always auto-generated. Operators read them from `.claw-runtime/claw-api/principals.json` or via `inject-into`.

### Known Verb Set

Read verbs:
```go
VerbFleetStatus       = "fleet.status"
VerbFleetLogs         = "fleet.logs"
VerbFleetQueryMetrics = "fleet.query_metrics"
VerbFleetAlerts       = "fleet.alerts"
```

Write verbs (reserved for #78, not yet implemented):
```go
VerbFleetRestart        = "fleet.restart"
VerbFleetQuarantine     = "fleet.quarantine"
VerbFleetBudgetSet      = "fleet.budget.set"
VerbFleetModelRestrict  = "fleet.model.restrict"
```

All known verbs are declared as constants in `internal/clawapi/principal.go`. The parser validates against this set — unknown verbs fail at parse time.

### Principal Scope: Four Dimensions

```go
type Principal struct {
    Name            string   `json:"name"`
    Token           string   `json:"token"`
    Verbs           []string `json:"verbs"`
    Pods            []string `json:"pods,omitempty"`
    Services        []string `json:"services,omitempty"`       // base service names
    ClawIDs         []string `json:"claw_ids,omitempty"`       // ordinal claw IDs
    ComposeServices []string `json:"compose_services,omitempty"` // compose service names
}
```

`compose_services` is unused in the current read plane but required for write-plane ordinal targeting (#78). Adding it now keeps the model stable.

### Merge Order

1. Auto-generate master principal (if `x-claw.master` set) — all verbs, pod scope
2. Auto-generate self principals (services with `claw-api: self`) — read verbs, service scope
3. Apply explicit `x-claw.principals` — same-name entries override auto-generated ones, new names are appended
4. Validate `inject-into` uniqueness: two non-override principals targeting the same service → hard error
5. Write `principals.json`

Override-by-name lets operators restrict the master if desired (e.g. downgrade to read-only).

---

## Implementation

### Task 1: Add `compose_services` to `Principal` and update validation

**Files:**
- Modify: `internal/clawapi/principal.go`
- Modify: `internal/clawapi/principal_test.go`

**Step 1: Write failing test**

```go
func TestPrincipalComposeServiceScope(t *testing.T) {
    p := Principal{
        Name:            "ops",
        Token:           "capi_x",
        Verbs:           []string{VerbFleetRestart},
        ComposeServices: []string{"worker-0"},
    }
    if !p.AllowsComposeService("trading-desk", "worker-0") {
        t.Fatal("expected compose service match")
    }
    if p.AllowsComposeService("trading-desk", "worker-1") {
        t.Fatal("did not expect worker-1 to match")
    }
}
```

**Step 2: Add field and method**

Add `ComposeServices []string` to `Principal`. Add `AllowsComposeService(podName, composeName string) bool` following the existing `matchesAny` pattern. Add `compose_services` to `validateStore` pattern validation.

**Step 3: Run tests, commit**

```
git commit -m "feat(clawapi): add compose_services scope dimension to Principal"
```

---

### Task 2: Add verb validation

**Files:**
- Modify: `internal/clawapi/principal.go`
- Modify: `internal/clawapi/principal_test.go`

**Step 1: Write failing test**

```go
func TestValidateStoreRejectsUnknownVerb(t *testing.T) {
    store := &Store{
        Principals: []Principal{{
            Name:  "bad",
            Token: "capi_x",
            Verbs: []string{"fleet.explode"},
        }},
    }
    if err := validateStore(store); err == nil {
        t.Fatal("expected unknown verb error")
    }
}
```

**Step 2: Add known verb set and validation**

Declare:
```go
var AllReadVerbs  = []string{VerbFleetStatus, VerbFleetLogs, VerbFleetQueryMetrics, VerbFleetAlerts}
var AllWriteVerbs = []string{VerbFleetRestart, VerbFleetQuarantine, VerbFleetBudgetSet, VerbFleetModelRestrict}
var AllVerbs      = append(append([]string{}, AllReadVerbs...), AllWriteVerbs...)
```

Add `validateVerbs` called from `validateStore` that checks each verb is in `AllVerbs`.

**Step 3: Run tests, commit**

```
git commit -m "feat(clawapi): validate verbs against known set at store load time"
```

---

### Task 3: `BuildSelfPrincipal` and update `BuildMasterPrincipal`

**Files:**
- Modify: `internal/clawapi/principal.go`
- Modify: `internal/clawapi/principal_test.go`

**Step 1: Write failing tests**

```go
func TestBuildSelfPrincipalIsReadOnlyAndServiceScoped(t *testing.T) {
    p, err := BuildSelfPrincipal("trading-desk", "analyst")
    if err != nil {
        t.Fatalf("BuildSelfPrincipal: %v", err)
    }
    if p.Name != "analyst" {
        t.Fatalf("unexpected name: %q", p.Name)
    }
    for _, v := range AllWriteVerbs {
        if p.AllowsVerb(v) {
            t.Fatalf("self principal must not have write verb %q", v)
        }
    }
    if !p.AllowsService("trading-desk", "analyst") {
        t.Fatal("expected service-scope match")
    }
    if p.AllowsService("trading-desk", "other") {
        t.Fatal("did not expect other-service access")
    }
}

func TestBuildMasterPrincipalHasAllVerbs(t *testing.T) {
    p, err := BuildMasterPrincipal("trading-desk", "sentinel")
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }
    for _, v := range AllVerbs {
        if !p.AllowsVerb(v) {
            t.Fatalf("master principal missing verb %q", v)
        }
    }
}
```

**Step 2: Implement**

```go
func BuildSelfPrincipal(podName, serviceName string) (Principal, error) {
    token, err := GenerateToken()
    if err != nil {
        return Principal{}, err
    }
    return Principal{
        Name:     serviceName,
        Token:    token,
        Verbs:    AllReadVerbs,
        Services: []string{serviceName},
    }, nil
}
```

Update `BuildMasterPrincipal` to use `AllVerbs` instead of the 4 read verbs.

Update `TestBuildMasterPrincipalIsReadOnlyAndOpaque` — rename it and check for all verbs.

**Step 3: Run tests, commit**

```
git commit -m "feat(clawapi): BuildSelfPrincipal (read-only, service-scoped) and extend master to all verbs"
```

---

### Task 4: Extend pod parser for `claw-api: self` and `x-claw.principals`

**Files:**
- Modify: `internal/pod/parser.go`
- Modify: `internal/pod/types.go`
- Create: `internal/pod/parser_principal_test.go`

**Step 1: Write failing tests**

Test cases:
- `claw-api: self` on a service → parsed into `ClawBlock.ClawAPIMode = "self"`
- `x-claw.principals` → parsed into `Pod.Principals`
- Unknown verb in explicit principal → parse error
- `scope: pod` + `services` together → parse error
- `inject-into` targeting nonexistent service → parse error
- No `claw-api` field → `ClawAPIMode` empty, no principal generated
- Pod with no `principals` key → `Pod.Principals` nil, no error

**Step 2: Add types**

In `internal/pod/types.go`:

```go
type PodPrincipal struct {
    Name            string   `yaml:"name"`
    Verbs           []string `yaml:"verbs"`
    Scope           string   `yaml:"scope,omitempty"`
    Services        []string `yaml:"services,omitempty"`
    ClawIDs         []string `yaml:"claw_ids,omitempty"`
    ComposeServices []string `yaml:"compose_services,omitempty"`
    InjectInto      string   `yaml:"inject-into,omitempty"`
}
```

Add to `ClawBlock`:
```go
ClawAPIMode string // "self" when claw-api: self is declared
```

Add to `Pod`:
```go
Principals []PodPrincipal
```

**Step 3: Parse and validate**

In `rawClawBlock`, add `ClawAPI string \`yaml:"claw-api"\``. In `rawPodClaw`, add `Principals []PodPrincipal \`yaml:"principals"\``.

Validation:
- `claw-api` value must be `"self"` or empty (future-proof: error on unknown values)
- Each principal: name non-empty, verbs non-empty and validated against `clawapi.AllVerbs`
- `scope: pod` mutually exclusive with `services`/`claw_ids`/`compose_services`
- `inject-into` must reference an existing service key

**Step 4: Run tests, commit**

```
git commit -m "feat(pod): parse claw-api: self and x-claw.principals"
```

---

### Task 5: Principal merge helper

**Files:**
- Modify: `internal/clawapi/principal.go`
- Modify: `internal/clawapi/principal_test.go`

**Step 1: Write failing tests**

```go
func TestMergePrincipalsAutoOnlyPassthrough(t *testing.T) { ... }
func TestMergePrincipalsExplicitOverridesByName(t *testing.T) { ... }
func TestMergePrincipalsExplicitNewNameAppended(t *testing.T) { ... }
func TestMergePrincipalsConflictingInjectIntoFails(t *testing.T) { ... }
// Two different principals both inject-into same service → error
// Same-name override: principal A auto-generated, explicit with same name → no conflict
```

**Step 2: Define intermediate type and implement**

The merge function needs `inject-into` info after the merge for projection in Task 6. Keep it in a parallel slice rather than losing it:

```go
type MergedPrincipal struct {
    Principal  Principal
    InjectInto string // service name, may be empty
}

func MergePrincipals(auto []Principal, explicit []pod.PodPrincipal, podName string) ([]MergedPrincipal, error)
```

Logic:
- Build name→index map over `auto`
- For each explicit entry:
  - Validate inject-into uniqueness: track `service→principalName`. If a service already appears (and the new principal has a different name), return error.
  - Convert `scope: pod` → `Pods: [podName]`
  - Generate token, build `Principal`
  - If name in auto map → replace. Otherwise, append.
- Return `[]MergedPrincipal`

**Step 3: Run tests, commit**

```
git commit -m "feat(clawapi): MergePrincipals with conflict detection and inject-into preservation"
```

---

### Task 6: Wire into `prepareClawAPIRuntime`

**Files:**
- Modify: `cmd/claw/compose_up.go`
- Modify: `cmd/claw/compose_up_test.go`

**Step 1: Write failing tests**

Test cases:
- Pod with master only → principals.json has 1 entry with all verbs
- Pod with master + `claw-api: self` service → principals.json has 2 entries; service gets env vars
- `claw-api: self` on master service → no duplicate (skipped)
- Pod with explicit `inject-into` → target service gets `CLAW_API_URL` + `CLAW_API_TOKEN`
- Two explicit principals both `inject-into` same service → `claw up` returns error
- `claw-api: self` service and explicit principal with same name → explicit wins

**Step 2: Refactor `prepareClawAPIRuntime`**

New flow:

```go
// 1. Build master principal
masterPrincipal, err := clawapi.BuildMasterPrincipal(p.Name, p.Master)

// 2. Build self principals from services with ClawAPIMode == "self"
var autoPrincipals []clawapi.Principal
autoPrincipals = append(autoPrincipals, masterPrincipal)
selfInjects := map[string]string{} // serviceName → token (for env injection)
for name, svc := range p.Services {
    if name == p.Master { continue }
    if svc.Claw == nil || svc.Claw.ClawAPIMode != "self" { continue }
    sp, err := clawapi.BuildSelfPrincipal(p.Name, name)
    autoPrincipals = append(autoPrincipals, sp)
    selfInjects[name] = sp.Token
}

// 3. Merge with explicit principals
merged, err := clawapi.MergePrincipals(autoPrincipals, p.Principals, p.Name)

// 4. Write principals.json
store := clawapi.Store{Principals: extractPrincipals(merged)}
// ... write to p.ClawAPI.PrincipalsHostPath

// 5. Inject tokens into services
// Master service
masterSvc.Environment["CLAW_API_URL"] = ...
masterSvc.Environment["CLAW_API_TOKEN"] = masterPrincipal.Token

// Self principals (may have been overridden by explicit — use merged token)
for _, m := range merged {
    if token, ok := selfInjects[m.Principal.Name]; ok {
        _ = token // use merged token, not original
        svc := p.Services[m.Principal.Name]
        svc.Environment["CLAW_API_URL"] = ...
        svc.Environment["CLAW_API_TOKEN"] = m.Principal.Token
    }
    if m.InjectInto != "" {
        target := p.Services[m.InjectInto]
        target.Environment["CLAW_API_URL"] = ...
        target.Environment["CLAW_API_TOKEN"] = m.Principal.Token
    }
}

// 6. Build service auth for cllama (master only — self/explicit principals use HTTP directly)
```

**Step 3: Run full test suite**

```
go test ./... && go vet ./...
```

**Step 4: Commit**

```
git commit -m "feat: wire self principals and explicit principals into prepareClawAPIRuntime"
```

---

### Task 7: Update ADR-015 and docs

**Files:**
- Modify: `docs/decisions/015-claw-api-authentication-and-scoping.md`
- Modify: `AGENTS.md`

**Step 1:** Document in ADR-015:
- `claw-api: self` as authority signal, distinct from surfaces (topology)
- `compose_services` scope dimension (reserved for write-plane ordinal targeting)
- Verb validation at parse time

**Step 2:** Update AGENTS.md "Repo-Specific Gotchas":
```
- `claw-api: self` on a service auto-generates a read-only scoped principal and injects
  CLAW_API_URL + CLAW_API_TOKEN. This is authority, distinct from `surfaces: [service://claw-api]`
  which is topology only. Both are needed for full access (reachability + credentials).
- Write verbs (fleet.restart etc.) are reserved for #78. Declaring them in principals.json is valid
  but the handler will reject requests until the write plane is implemented.
```

**Step 3: Commit**

```
git commit -m "docs: document claw-api: self signal, compose_services scope, and verb validation"
```

---

## Verification Checklist

- [ ] `go test ./...` passes
- [ ] `go vet ./...` clean
- [ ] Pod with `master:` only → 1 principal, all verbs, pod-wide scope
- [ ] Pod with `claw-api: self` on non-master → self principal with read verbs, service scope
- [ ] `claw-api: self` on master → no duplicate principal
- [ ] Explicit `principals:` parsed and validated (unknown verbs rejected at parse time)
- [ ] Same-name explicit overrides auto-generated
- [ ] Two principals with same `inject-into` → parse error
- [ ] `scope: pod` + `services` → parse error
- [ ] `inject-into` nonexistent service → parse error
- [ ] `examples/master-claw/claw-pod.yml` still parses cleanly
- [ ] Write verbs declared in explicit principal → accepted (no handler yet but valid)
