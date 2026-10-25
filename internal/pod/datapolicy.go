package pod

import (
	"fmt"
	"net"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Data policy values. The defaults are the most conservative: processing in
// the US or EU only, no provider training, no provider retention and no local
// content retention by cllama.
const (
	DataPolicyNone            = "none"
	DataPolicyAllowed         = "allowed"
	DataPolicyProviderDefault = "provider-default"
	DataPolicyRegionGlobal    = "global"
)

// DefaultDataPolicyRegions are the processing regions allowed when a policy
// does not name any.
var DefaultDataPolicyRegions = []string{"eu", "us"}

// DataPolicyGuarantees are the account-level guarantees an operator can
// attest for a provider endpoint. Region guarantees take a region suffix,
// e.g. processing:jp.
var DataPolicyGuarantees = []string{"zero-data-retention", "no-training", "processing:<region>", "storage:<region>"}

// DataPolicyConfig is a service's resolved region, training and retention
// policy. Declaring it says what the operator requires; cllama decides per
// request whether the provider can be shown to meet it and refuses otherwise.
type DataPolicyConfig struct {
	Regions        []string
	Training       string // none | allowed
	Retention      string // provider retention: none | provider-default
	LocalRetention string // cllama local content: none | allowed
	Attestations   []DataPolicyAttestation
}

// DataPolicyAttestation is an operator's statement that an account-level
// guarantee holds for one provider endpoint. An empty Endpoint covers only
// the provider's official endpoints, never an overridden base URL.
type DataPolicyAttestation struct {
	Provider   string
	Endpoint   string
	Guarantees []string
}

// DefaultDataPolicy returns the conservative policy applied when a service
// declares none.
func DefaultDataPolicy() *DataPolicyConfig {
	return &DataPolicyConfig{
		Regions:        append([]string(nil), DefaultDataPolicyRegions...),
		Training:       DataPolicyNone,
		Retention:      DataPolicyNone,
		LocalRetention: DataPolicyNone,
	}
}

type rawDataPolicyConfig struct {
	Regions        []string                   `yaml:"regions"`
	Training       string                     `yaml:"training"`
	Retention      string                     `yaml:"retention"`
	LocalRetention string                     `yaml:"local-retention"`
	Attestations   []rawDataPolicyAttestation `yaml:"attestations"`
}

type rawDataPolicyAttestation struct {
	Provider   string   `yaml:"provider"`
	Endpoint   string   `yaml:"endpoint"`
	Guarantees []string `yaml:"guarantees"`
}

// A misspelled key would otherwise fall back to a default silently, so
// unknown keys are rejected.
func (r *rawDataPolicyConfig) UnmarshalYAML(node *yaml.Node) error {
	if err := rejectUnknownKeys(node, "data-policy", "regions", "training", "retention", "local-retention", "attestations"); err != nil {
		return err
	}
	type alias rawDataPolicyConfig
	return node.Decode((*alias)(r))
}

func (r *rawDataPolicyAttestation) UnmarshalYAML(node *yaml.Node) error {
	if err := rejectUnknownKeys(node, "data-policy attestation", "provider", "endpoint", "guarantees"); err != nil {
		return err
	}
	type alias rawDataPolicyAttestation
	return node.Decode((*alias)(r))
}

func rejectUnknownKeys(node *yaml.Node, label string, known ...string) error {
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("%s must be a mapping", label)
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		key := node.Content[i].Value
		found := false
		for _, k := range known {
			if key == k {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("%s: unknown key %q (known: %s)", label, key, strings.Join(known, ", "))
		}
	}
	return nil
}

var (
	dataPolicyRegionPattern   = regexp.MustCompile(`^[a-z]{2,3}$`)
	dataPolicyProviderPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
)

func parseDataPolicy(raw *rawDataPolicyConfig) (*DataPolicyConfig, error) {
	if raw == nil {
		return nil, nil
	}
	policy := DefaultDataPolicy()
	if raw.Regions != nil {
		regions, err := parseDataPolicyRegions(raw.Regions)
		if err != nil {
			return nil, err
		}
		policy.Regions = regions
	}
	var err error
	if policy.Training, err = dataPolicyChoice("training", raw.Training, DataPolicyNone, DataPolicyAllowed); err != nil {
		return nil, err
	}
	if policy.Retention, err = dataPolicyChoice("retention", raw.Retention, DataPolicyNone, DataPolicyProviderDefault); err != nil {
		return nil, err
	}
	if policy.LocalRetention, err = dataPolicyChoice("local-retention", raw.LocalRetention, DataPolicyNone, DataPolicyAllowed); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for i, rawAtt := range raw.Attestations {
		att, err := parseDataPolicyAttestation(rawAtt)
		if err != nil {
			return nil, fmt.Errorf("attestations[%d]: %w", i, err)
		}
		key := att.Provider + " " + att.Endpoint
		if seen[key] {
			return nil, fmt.Errorf("attestations[%d]: provider %q endpoint %q is attested more than once", i, att.Provider, att.Endpoint)
		}
		seen[key] = true
		policy.Attestations = append(policy.Attestations, att)
	}
	sort.Slice(policy.Attestations, func(i, j int) bool {
		a, b := policy.Attestations[i], policy.Attestations[j]
		if a.Provider != b.Provider {
			return a.Provider < b.Provider
		}
		return a.Endpoint < b.Endpoint
	})
	return policy, nil
}

func dataPolicyChoice(field, value string, allowed ...string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return allowed[0], nil
	}
	for _, a := range allowed {
		if value == a {
			return value, nil
		}
	}
	return "", fmt.Errorf("%s must be one of %s, got %q", field, strings.Join(allowed, ", "), value)
}

func parseDataPolicyRegions(raw []string) ([]string, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("regions must not be empty; omit it for the default %v", DefaultDataPolicyRegions)
	}
	set := map[string]bool{}
	for _, region := range raw {
		region = strings.TrimSpace(region)
		if region != DataPolicyRegionGlobal && !dataPolicyRegionPattern.MatchString(region) {
			return nil, fmt.Errorf("region %q must be a lowercase region code such as us, eu or jp, or %q", region, DataPolicyRegionGlobal)
		}
		set[region] = true
	}
	if set[DataPolicyRegionGlobal] && len(set) > 1 {
		return nil, fmt.Errorf("region %q allows every region and cannot be combined with others", DataPolicyRegionGlobal)
	}
	regions := make([]string, 0, len(set))
	for region := range set {
		regions = append(regions, region)
	}
	sort.Strings(regions)
	return regions, nil
}

func parseDataPolicyAttestation(raw rawDataPolicyAttestation) (DataPolicyAttestation, error) {
	provider := strings.ToLower(strings.TrimSpace(raw.Provider))
	if !dataPolicyProviderPattern.MatchString(provider) {
		return DataPolicyAttestation{}, fmt.Errorf("provider %q is not a provider name", raw.Provider)
	}
	endpoint, err := parseDataPolicyEndpoint(raw.Endpoint)
	if err != nil {
		return DataPolicyAttestation{}, err
	}
	if len(raw.Guarantees) == 0 {
		return DataPolicyAttestation{}, fmt.Errorf("guarantees must not be empty")
	}
	set := map[string]bool{}
	for _, g := range raw.Guarantees {
		g = strings.TrimSpace(g)
		if !validDataPolicyGuarantee(g) {
			return DataPolicyAttestation{}, fmt.Errorf("guarantee %q must be one of %s", g, strings.Join(DataPolicyGuarantees, ", "))
		}
		set[g] = true
	}
	guarantees := make([]string, 0, len(set))
	for g := range set {
		guarantees = append(guarantees, g)
	}
	sort.Strings(guarantees)
	return DataPolicyAttestation{Provider: provider, Endpoint: endpoint, Guarantees: guarantees}, nil
}

func validDataPolicyGuarantee(g string) bool {
	switch g {
	case "zero-data-retention", "no-training":
		return true
	}
	for _, prefix := range []string{"processing:", "storage:"} {
		if region, ok := strings.CutPrefix(g, prefix); ok {
			return dataPolicyRegionPattern.MatchString(region)
		}
	}
	return false
}

// parseDataPolicyEndpoint accepts an https URL, or http for a local or
// in-pod host. It is normalized so cllama can compare it with the actual
// dispatch base URL.
func parseDataPolicyEndpoint(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.RawPath != "" || strings.Contains(u.Path, "%") || strings.Contains(raw, "%") {
		// Fixed text: a rejected endpoint may carry credentials.
		return "", fmt.Errorf("endpoint must be an absolute URL without credentials, encoding, query or fragment")
	}
	scheme := strings.ToLower(u.Scheme)
	host := strings.ToLower(u.Hostname())
	switch {
	case scheme == "https":
	case scheme == "http" && localDataPolicyHost(host):
	default:
		return "", fmt.Errorf("endpoint must use https (http only for a local or in-pod host)")
	}
	u.Scheme = scheme
	u.Host = strings.ToLower(u.Host)
	u.Path = strings.TrimRight(u.Path, "/")
	return u.String(), nil
}

func localDataPolicyHost(host string) bool {
	if host == "localhost" || !strings.Contains(host, ".") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && (ip.IsLoopback() || ip.IsPrivate())
}
