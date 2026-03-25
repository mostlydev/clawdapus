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
)

const (
	VerbFleetStatus        = "fleet.status"
	VerbFleetLogs          = "fleet.logs"
	VerbFleetQueryMetrics  = "fleet.query_metrics"
	VerbFleetAlerts        = "fleet.alerts"
	VerbFleetRestart       = "fleet.restart"
	VerbFleetQuarantine    = "fleet.quarantine"
	VerbFleetBudgetSet     = "fleet.budget.set"
	VerbFleetModelRestrict = "fleet.model.restrict"
)

var AllReadVerbs = []string{VerbFleetStatus, VerbFleetLogs, VerbFleetQueryMetrics, VerbFleetAlerts}
var AllWriteVerbs = []string{VerbFleetRestart, VerbFleetQuarantine, VerbFleetBudgetSet, VerbFleetModelRestrict}
var AllVerbs = append(append([]string{}, AllReadVerbs...), AllWriteVerbs...)

type Store struct {
	Principals []Principal `json:"principals"`
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
	raw, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("read claw-api principals %q: %w", filePath, err)
	}
	var store Store
	if err := json.Unmarshal(raw, &store); err != nil {
		return nil, fmt.Errorf("parse claw-api principals %q: %w", filePath, err)
	}
	if err := validateStore(&store); err != nil {
		return nil, fmt.Errorf("validate claw-api principals %q: %w", filePath, err)
	}
	return &store, nil
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

func validateStore(store *Store) error {
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
		if err := validateVerbs(principal.Verbs); err != nil {
			return fmt.Errorf("principal %q: %w", principal.Name, err)
		}
	}
	return nil
}

func validateVerbs(verbs []string) error {
	for _, verb := range verbs {
		verb = strings.TrimSpace(verb)
		known := false
		for _, v := range AllVerbs {
			if v == verb {
				known = true
				break
			}
		}
		if !known {
			return fmt.Errorf("unknown verb %q", verb)
		}
	}
	return nil
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
