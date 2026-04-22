package clawapi

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"strings"

	"golang.org/x/mod/semver"
)

const (
	VerbFleetStatus        = "fleet.status"
	VerbFleetLogs          = "fleet.logs"
	VerbFleetQueryMetrics  = "fleet.query_metrics"
	VerbFleetAlerts        = "fleet.alerts"
	VerbScheduleRead       = "schedule.read"
	VerbAgentContext       = "agent.context"
	VerbFleetRestart       = "fleet.restart"
	VerbFleetQuarantine    = "fleet.quarantine"
	VerbFleetBudgetSet     = "fleet.budget.set"
	VerbFleetModelRestrict = "fleet.model.restrict"
	VerbScheduleControl    = "schedule.control"
)

var AllReadVerbs = []string{VerbFleetStatus, VerbFleetLogs, VerbFleetQueryMetrics, VerbFleetAlerts, VerbScheduleRead, VerbAgentContext}
var AllWriteVerbs = []string{VerbFleetRestart, VerbFleetQuarantine, VerbFleetBudgetSet, VerbFleetModelRestrict, VerbScheduleControl}
var AllVerbs = append(append([]string{}, AllReadVerbs...), AllWriteVerbs...)

type Store struct {
	Principals            []Principal `json:"principals"`
	NormalizationWarnings []string    `json:"-"`
}

type Principal struct {
	Name            string   `json:"name"`
	Token           string   `json:"token"`
	Verbs           []string `json:"verbs"`
	Pods            []string `json:"pods,omitempty"`
	Services        []string `json:"services,omitempty"`
	ClawIDs         []string `json:"claw_ids,omitempty"`
	ComposeServices []string `json:"compose_services,omitempty"`
}

func LoadStore(filePath string) (*Store, error) {
	store, _, err := LoadStoreWithWarnings(filePath)
	return store, err
}

func LoadStoreWithWarnings(filePath string) (*Store, []string, error) {
	raw, err := os.ReadFile(filePath)
	if err != nil {
		return nil, nil, fmt.Errorf("read claw-api principals %q: %w", filePath, err)
	}
	var store Store
	if err := json.Unmarshal(raw, &store); err != nil {
		return nil, nil, fmt.Errorf("parse claw-api principals %q: %w", filePath, err)
	}
	warnings, err := normalizeStore(&store)
	if err != nil {
		return nil, nil, fmt.Errorf("validate claw-api principals %q: %w", filePath, err)
	}
	store.NormalizationWarnings = append([]string(nil), warnings...)
	return &store, warnings, nil
}

func (s *Store) InertPrincipalNames() []string {
	if s == nil {
		return nil
	}
	names := make([]string, 0)
	for _, principal := range s.Principals {
		if len(principal.Verbs) == 0 {
			names = append(names, principal.Name)
		}
	}
	return names
}

func (s *Store) ResolveBearer(header string) (*Principal, error) {
	if s == nil {
		return nil, fmt.Errorf("principal store not configured")
	}
	token := strings.TrimSpace(header)
	if strings.HasPrefix(strings.ToLower(token), "bearer ") {
		token = strings.TrimSpace(token[7:])
	}
	if token == "" {
		return nil, fmt.Errorf("missing bearer token")
	}
	for _, principal := range s.Principals {
		if secureEqual(principal.Token, token) {
			copyPrincipal := principal
			return &copyPrincipal, nil
		}
	}
	return nil, fmt.Errorf("unknown principal")
}

func (p *Principal) AllowsVerb(verb string) bool {
	if p == nil {
		return false
	}
	for _, allowed := range p.Verbs {
		if strings.TrimSpace(allowed) == verb {
			return true
		}
	}
	return false
}

func (p *Principal) AllowsPod(podName string) bool {
	if p == nil {
		return false
	}
	return matchesAny(p.Pods, podName)
}

func (p *Principal) AllowsService(podName, service string) bool {
	if p == nil {
		return false
	}
	if p.AllowsPod(podName) {
		return true
	}
	return matchesAny(p.Services, service)
}

func (p *Principal) AllowsClawID(podName, clawID string) bool {
	if p == nil {
		return false
	}
	if p.AllowsPod(podName) {
		return true
	}
	return matchesAny(p.ClawIDs, clawID)
}

func (p *Principal) AllowsComposeService(podName, composeName string) bool {
	if p == nil {
		return false
	}
	if p.AllowsPod(podName) {
		return true
	}
	return matchesAny(p.ComposeServices, composeName)
}

func BuildMasterPrincipal(podName, principalName string) (Principal, error) {
	token, err := GenerateToken()
	if err != nil {
		return Principal{}, err
	}
	verbs := append([]string{}, AllVerbs...)
	return Principal{
		Name:  principalName,
		Token: token,
		Verbs: verbs,
		Pods:  []string{podName},
	}, nil
}

func BuildSelfPrincipal(podName, serviceName string) (Principal, error) {
	token, err := GenerateToken()
	if err != nil {
		return Principal{}, err
	}
	verbs := append([]string{}, AllReadVerbs...)
	return Principal{
		Name:     serviceName,
		Token:    token,
		Verbs:    verbs,
		Services: []string{serviceName},
	}, nil
}

func BuildSchedulerPrincipal(podName string) (Principal, error) {
	token, err := GenerateToken()
	if err != nil {
		return Principal{}, err
	}
	return Principal{
		Name:  "claw-scheduler",
		Token: token,
		Verbs: []string{VerbScheduleRead, VerbScheduleControl},
		Pods:  []string{podName},
	}, nil
}

func BuildDashboardPrincipal(podName string) (Principal, error) {
	token, err := GenerateToken()
	if err != nil {
		return Principal{}, err
	}
	verbs := append([]string{}, AllReadVerbs...)
	return Principal{
		Name:  "claw-dashboard",
		Token: token,
		Verbs: verbs,
		Pods:  []string{podName},
	}, nil
}

func GenerateToken() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate claw-api token: %w", err)
	}
	return "capi_" + hex.EncodeToString(buf), nil
}

func matchesAny(patterns []string, value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(patterns) == 0 {
		return false
	}
	for _, pattern := range patterns {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			continue
		}
		if !containsGlobMeta(pattern) {
			if secureEqual(pattern, value) {
				return true
			}
			continue
		}
		ok, err := path.Match(pattern, value)
		if err == nil && ok {
			return true
		}
	}
	return false
}

func normalizeStore(store *Store) ([]string, error) {
	if err := validateStoreStructure(store); err != nil {
		return nil, err
	}
	var warnings []string
	for i := range store.Principals {
		filtered, unknown := filterKnownVerbs(store.Principals[i].Verbs)
		for _, verb := range unknown {
			warnings = append(warnings, fmt.Sprintf("ignoring unknown verb %q for principal %q", verb, store.Principals[i].Name))
		}
		if len(filtered) == 0 && len(store.Principals[i].Verbs) > 0 {
			warnings = append(warnings, fmt.Sprintf("principal %q has no recognized verbs; token will authorize nothing", store.Principals[i].Name))
		}
		store.Principals[i].Verbs = filtered
	}
	return warnings, nil
}

func validateStoreStructure(store *Store) error {
	if store == nil {
		return fmt.Errorf("store is nil")
	}
	for i, principal := range store.Principals {
		if strings.TrimSpace(principal.Name) == "" {
			return fmt.Errorf("principal %d: name must not be empty", i)
		}
		if strings.TrimSpace(principal.Token) == "" {
			return fmt.Errorf("principal %q: token must not be empty", principal.Name)
		}
		if err := validatePatterns("pods", principal.Pods); err != nil {
			return fmt.Errorf("principal %q: %w", principal.Name, err)
		}
		if err := validatePatterns("services", principal.Services); err != nil {
			return fmt.Errorf("principal %q: %w", principal.Name, err)
		}
		if err := validatePatterns("claw_ids", principal.ClawIDs); err != nil {
			return fmt.Errorf("principal %q: %w", principal.Name, err)
		}
		if err := validatePatterns("compose_services", principal.ComposeServices); err != nil {
			return fmt.Errorf("principal %q: %w", principal.Name, err)
		}
	}
	return nil
}

func filterKnownVerbs(verbs []string) ([]string, []string) {
	filtered := make([]string, 0, len(verbs))
	var unknown []string
	for _, verb := range verbs {
		verb = strings.TrimSpace(verb)
		if verb == "" {
			continue
		}
		if isKnownVerb(verb) {
			filtered = append(filtered, verb)
			continue
		}
		unknown = append(unknown, verb)
	}
	return filtered, unknown
}

func isKnownVerb(verb string) bool {
	for _, v := range AllVerbs {
		if v == verb {
			return true
		}
	}
	return false
}

func validatePatterns(label string, patterns []string) error {
	for _, pattern := range patterns {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			continue
		}
		if _, err := path.Match(pattern, "probe"); err != nil {
			return fmt.Errorf("%s pattern %q is invalid: %w", label, pattern, err)
		}
	}
	return nil
}

func containsGlobMeta(pattern string) bool {
	return strings.ContainsAny(pattern, "*?[\\")
}

var minClawAPIImageVersionByVerb = map[string]string{
	VerbScheduleRead:    "v0.6.0",
	VerbScheduleControl: "v0.6.0",
}

func PrincipalVersionSkewWarnings(store *Store, imageRef string) []string {
	if store == nil {
		return nil
	}
	imageRef = strings.TrimSpace(imageRef)
	if imageRef == "" {
		return nil
	}

	tag, tagged := imageTagFromRef(imageRef)
	version, versioned := normalizeSemverTag(tag)

	seen := make(map[string]struct{})
	warnings := make([]string, 0)
	for _, principal := range store.Principals {
		for _, verb := range principal.Verbs {
			minVersion, tracked := minClawAPIImageVersionByVerb[verb]
			if !tracked {
				continue
			}

			var warning string
			switch {
			case !tagged || !versioned:
				warning = fmt.Sprintf("claw-api image %q is not version-pinned; cannot verify support for principal %q verb %q (known minimum %s)", imageRef, principal.Name, verb, minVersion)
			case semver.Compare(version, minVersion) < 0:
				warning = fmt.Sprintf("claw-api image %q may not support principal %q verb %q (known minimum %s)", imageRef, principal.Name, verb, minVersion)
			default:
				continue
			}
			if _, ok := seen[warning]; ok {
				continue
			}
			seen[warning] = struct{}{}
			warnings = append(warnings, warning)
		}
	}
	return warnings
}

func imageTagFromRef(imageRef string) (string, bool) {
	imageRef = strings.TrimSpace(imageRef)
	if imageRef == "" {
		return "", false
	}
	if i := strings.Index(imageRef, "@"); i >= 0 {
		imageRef = imageRef[:i]
	}
	slash := strings.LastIndex(imageRef, "/")
	colon := strings.LastIndex(imageRef, ":")
	if colon <= slash {
		return "", false
	}
	tag := strings.TrimSpace(imageRef[colon+1:])
	if tag == "" {
		return "", false
	}
	return tag, true
}

func normalizeSemverTag(tag string) (string, bool) {
	tag = strings.TrimSpace(tag)
	if tag == "" {
		return "", false
	}
	if tag[0] >= '0' && tag[0] <= '9' {
		tag = "v" + tag
	}
	if !semver.IsValid(tag) {
		return "", false
	}
	return tag, true
}

func secureEqual(a, b string) bool {
	ab := []byte(a)
	bb := []byte(b)
	maxLen := len(ab)
	if len(bb) > maxLen {
		maxLen = len(bb)
	}
	var diff byte
	for i := 0; i < maxLen; i++ {
		var av byte
		var bv byte
		if i < len(ab) {
			av = ab[i]
		}
		if i < len(bb) {
			bv = bb[i]
		}
		diff |= av ^ bv
	}
	return subtle.ConstantTimeEq(int32(len(ab)), int32(len(bb))) == 1 &&
		subtle.ConstantTimeByteEq(diff, 0) == 1
}
