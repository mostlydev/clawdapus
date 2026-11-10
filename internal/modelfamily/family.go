// Package modelfamily resolves a version-free model family to one concrete
// model ID at compile time. It decides; it performs no I/O. The catalog, the
// price table and the data-policy check are supplied by the caller.
package modelfamily

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Family is an operator's statement of which models a slot may use.
type Family struct {
	Name     string
	Provider string
	// Pattern is a literal model ID with exactly one "*" where the version is.
	Pattern string
	// MaxInput and MaxOutput are absolute ceilings in USD per million tokens
	// on the listed standard rates, long-context tier included.
	MaxInput  float64
	MaxOutput float64
}

var (
	nameRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
	// literalRe is what may surround the version in a pattern, and so what
	// a member ID may consist of besides its version.
	literalRe = regexp.MustCompile(`^[A-Za-z0-9._/-]*$`)
	// versionRe is a dotted decimal version: one to three components, each
	// one to four digits with no leading zero unless the component is 0.
	versionRe = regexp.MustCompile(`^(0|[1-9][0-9]{0,3})(\.(0|[1-9][0-9]{0,3})){0,2}$`)
)

// PlanProvider reports whether a provider is a plan sign-in route, served
// from a subscription and not billed per token.
func PlanProvider(provider string) bool { return provider == "chatgpt" }

// Validate checks a family declaration.
func (f Family) Validate() error {
	if !nameRe.MatchString(f.Name) {
		return fmt.Errorf("model family name %q must be lowercase letters, digits and dashes", f.Name)
	}
	if strings.TrimSpace(f.Provider) == "" || f.Provider != strings.ToLower(strings.TrimSpace(f.Provider)) || !literalRe.MatchString(f.Provider) || strings.Contains(f.Provider, "/") {
		return fmt.Errorf("model family %q: provider %q is not a provider name", f.Name, f.Provider)
	}
	if _, _, err := splitPattern(f.Pattern); err != nil {
		return fmt.Errorf("model family %q: %w", f.Name, err)
	}
	if PlanProvider(f.Provider) {
		// A plan sign-in is not billed per token, so there is no price to
		// bound. A ceiling here would claim a limit nothing enforces.
		if f.MaxInput != 0 || f.MaxOutput != 0 {
			return fmt.Errorf("model family %q: a plan route has no per-token price; omit max-input and max-output", f.Name)
		}
		return nil
	}
	for label, v := range map[string]float64{"max-input": f.MaxInput, "max-output": f.MaxOutput} {
		if math.IsNaN(v) || math.IsInf(v, 0) || v <= 0 {
			return fmt.Errorf("model family %q: %s must be a positive price per million tokens", f.Name, label)
		}
	}
	return nil
}

func splitPattern(pattern string) (prefix, suffix string, err error) {
	if strings.Count(pattern, "*") != 1 {
		return "", "", fmt.Errorf("pattern %q must contain exactly one *", pattern)
	}
	prefix, suffix, _ = strings.Cut(pattern, "*")
	if !literalRe.MatchString(prefix) || !literalRe.MatchString(suffix) || prefix+suffix == "" {
		return "", "", fmt.Errorf("pattern %q may contain only letters, digits, '.', '_', '/' and '-' around the *", pattern)
	}
	// A digit or dot touching the * would make the version boundary ambiguous.
	if prefix != "" && strings.ContainsAny(prefix[len(prefix)-1:], "0123456789.") {
		return "", "", fmt.Errorf("pattern %q: the text before * must not end in a digit or dot", pattern)
	}
	if suffix != "" && strings.ContainsAny(suffix[:1], "0123456789.") {
		return "", "", fmt.Errorf("pattern %q: the text after * must not start with a digit or dot", pattern)
	}
	return prefix, suffix, nil
}

// Version is a parsed dotted version. Missing components are zero.
type Version [3]int

// Compare orders versions numerically by component.
func (v Version) Compare(o Version) int {
	for i := range v {
		if v[i] != o[i] {
			if v[i] < o[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

// Member reports whether id belongs to the family and returns its version.
// An ID is a member only if it is the pattern's prefix, a version and the
// pattern's suffix, in full. A dated snapshot or any other variant is not.
func (f Family) Member(id string) (Version, bool) {
	prefix, suffix, err := splitPattern(f.Pattern)
	if err != nil || !strings.HasPrefix(id, prefix) || !strings.HasSuffix(id, suffix) || len(id) < len(prefix)+len(suffix) {
		return Version{}, false
	}
	raw := id[len(prefix) : len(id)-len(suffix)]
	if !versionRe.MatchString(raw) {
		return Version{}, false
	}
	var v Version
	for i, part := range strings.Split(raw, ".") {
		v[i], _ = strconv.Atoi(part)
	}
	return v, true
}

// Catalog is a provider's model listing as read by the caller.
type Catalog struct {
	// Complete is true only for one successful read of the whole listing.
	Complete bool
	IDs      []string
}

// Reasons a candidate is passed over or a family cannot be resolved.
const (
	ReasonCatalogIncomplete = "catalog_incomplete"
	ReasonNoMember          = "no_member_in_catalog"
	ReasonVersionTie        = "version_tie"
	ReasonUnpriced          = "unpriced"
	ReasonPriceEstimate     = "price_not_a_ceiling"
	ReasonOverInput         = "input_price_over_ceiling"
	ReasonOverOutput        = "output_price_over_ceiling"
	ReasonPolicy            = "data_policy"
	ReasonNoEligible        = "no_eligible_member"
)

// Skipped is a family member that was not chosen, and why.
type Skipped struct {
	ID     string
	Reason string
}

// Decision is the outcome of resolving one family.
type Decision struct {
	// ID is the chosen model, empty when nothing could be chosen.
	ID string
	// Price is the exact entry the choice was bounded with.
	Price Price
	// Reason says why nothing was chosen.
	Reason string
	// Skipped lists members with a higher version than the choice, or every
	// member when nothing was chosen.
	Skipped []Skipped
}

// Eligible reports why a member may not be used, or "" when it may. It is
// the single rule applied to a catalog candidate and to a reused lock.
func (f Family) Eligible(id string, prices *Prices, policy func(id string) error) string {
	if _, ok := f.Member(id); !ok {
		return ReasonNoMember
	}
	if PlanProvider(f.Provider) {
		// Eligibility on a plan route is membership of the account's own
		// model list, which the caller supplies as the catalog.
		if policy != nil && policy(id) != nil {
			return ReasonPolicy
		}
		return ""
	}
	price, ok := prices.Exact(f.Provider, id)
	if !ok {
		return ReasonUnpriced
	}
	if price.ContextTiers != ContextTiersAll {
		return ReasonPriceEstimate
	}
	if price.MaxInput > f.MaxInput {
		return ReasonOverInput
	}
	if price.MaxOutput > f.MaxOutput {
		return ReasonOverOutput
	}
	if policy != nil && policy(id) != nil {
		return ReasonPolicy
	}
	return ""
}

// Choose picks the highest-versioned member of the family in the catalog
// that is exactly priced within the ceilings and passes the data policy. It
// chooses nothing from an incomplete catalog, and nothing when two members
// share a version.
func (f Family) Choose(catalog Catalog, prices *Prices, policy func(id string) error) Decision {
	if !catalog.Complete {
		return Decision{Reason: ReasonCatalogIncomplete}
	}
	type member struct {
		id string
		v  Version
	}
	var members []member
	seen := map[string]bool{}
	for _, id := range catalog.IDs {
		if seen[id] {
			continue
		}
		seen[id] = true
		if v, ok := f.Member(id); ok {
			members = append(members, member{id, v})
		}
	}
	if len(members) == 0 {
		return Decision{Reason: ReasonNoMember}
	}
	sort.Slice(members, func(i, j int) bool {
		if c := members[i].v.Compare(members[j].v); c != 0 {
			return c > 0
		}
		return members[i].id < members[j].id
	})
	for i := 1; i < len(members); i++ {
		if members[i].v.Compare(members[i-1].v) == 0 {
			return Decision{Reason: ReasonVersionTie, Skipped: []Skipped{{members[i-1].id, ReasonVersionTie}, {members[i].id, ReasonVersionTie}}}
		}
	}
	var skipped []Skipped
	for _, m := range members {
		reason := f.Eligible(m.id, prices, policy)
		if reason == "" {
			price, _ := prices.Exact(f.Provider, m.id)
			return Decision{ID: m.id, Price: price, Skipped: skipped}
		}
		skipped = append(skipped, Skipped{m.id, reason})
	}
	return Decision{Reason: ReasonNoEligible, Skipped: skipped}
}
