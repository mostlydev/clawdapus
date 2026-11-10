package modelfamily

import (
	"errors"
	"strings"
	"testing"
)

const exportHead = `{"schema":1,"cllama":{"version":"v0.16.0","revision":"abc"},"currency":"USD","unit":"per_million_tokens","scope":{"service_tier":"standard","tokens":"uncached_text"},"entries":[`

func entry(provider, model string, in, out, maxIn, maxOut float64, tiers string) string {
	return `{"provider":"` + provider + `","model":"` + model + `","input":` + f(in) + `,"output":` + f(out) + `,"max_input":` + f(maxIn) + `,"max_output":` + f(maxOut) + `,"context_tiers":"` + tiers + `"}`
}

func f(v float64) string { return strings.TrimRight(strings.TrimRight(strconvFormat(v), "0"), ".") }

func prices(t *testing.T, entries ...string) *Prices {
	t.Helper()
	p, err := ParsePrices([]byte(exportHead + strings.Join(entries, ",") + "]}"))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func terra() Family {
	return Family{Name: "terra", Provider: "openai", Pattern: "gpt-*-terra", MaxInput: 4, MaxOutput: 18}
}

func table(t *testing.T) *Prices {
	return prices(t,
		entry("openai", "gpt-5.6-terra", 2, 12, 4, 18, "all"),
		entry("openai", "gpt-6.1-terra", 1, 5, 2, 7.5, "all"),
		entry("openai", "gpt-5.6", 5, 30, 10, 45, "all"), // a prefix catch-all
		entry("google", "gemini-2.5-pro", 1.25, 10, 1.25, 10, "base_only"),
	)
}

func TestMembershipIsExactAndNumeric(t *testing.T) {
	fam := terra()
	for id, want := range map[string]bool{
		"gpt-5.6-terra":            true,
		"gpt-6-terra":              true,
		"gpt-6.10.2-terra":         true,
		"gpt-6.1-terra-2026-10-01": false, // dated snapshot
		"gpt-6.1-terra-preview":    false,
		"gpt-6.1-sol":              false,
		"xgpt-6.1-terra":           false,
		"gpt-06.1-terra":           false, // leading zero
		"gpt-6.1.2.3-terra":        false, // four components
		"gpt-6.-terra":             false,
		"gpt--terra":               false,
		"gpt-6.12345-terra":        false, // component too long
		"gpt-6.1-terra:online":     false, // a route option
		"gpt-6.1-terra/../x":       false,
		"vercel/gpt-6.1-terra":     false,
		"gpt-v6-terra":             false,
	} {
		if _, got := fam.Member(id); got != want {
			t.Errorf("Member(%q) = %v, want %v", id, got, want)
		}
	}
	a, _ := fam.Member("gpt-6.10-terra")
	b, _ := fam.Member("gpt-6.9-terra")
	c, _ := fam.Member("gpt-6-terra")
	d, _ := fam.Member("gpt-6.0-terra")
	if a.Compare(b) <= 0 || b.Compare(c) <= 0 || c.Compare(d) != 0 {
		t.Fatalf("ordering: 6.10=%v 6.9=%v 6=%v 6.0=%v", a, b, c, d)
	}
}

func TestPatternGrammar(t *testing.T) {
	for pattern, ok := range map[string]bool{
		"gpt-*-terra":      true,
		"claude-sonnet-*":  true,
		"*-terra":          true,
		"gpt-terra":        false, // no *
		"gpt-*-*":          false,
		"*":                false,
		"gpt-5.*-terra":    false, // version boundary touches a dot
		"gpt-*6-terra":     false,
		"gpt-*-terra:fast": false,
		"gpt-* terra":      false,
		"gpt-?-terra*":     false,
	} {
		fam := Family{Name: "x", Provider: "openai", Pattern: pattern, MaxInput: 1, MaxOutput: 1}
		if err := fam.Validate(); (err == nil) != ok {
			t.Errorf("pattern %q: err=%v, want ok=%v", pattern, err, ok)
		}
	}
	for name, fam := range map[string]Family{
		"bad name":     {Name: "Terra", Provider: "openai", Pattern: "gpt-*-terra", MaxInput: 1, MaxOutput: 1},
		"bad provider": {Name: "terra", Provider: "openai/eu", Pattern: "gpt-*-terra", MaxInput: 1, MaxOutput: 1},
		"no ceiling":   {Name: "terra", Provider: "openai", Pattern: "gpt-*-terra", MaxOutput: 1},
		"negative":     {Name: "terra", Provider: "openai", Pattern: "gpt-*-terra", MaxInput: -1, MaxOutput: 1},
	} {
		if fam.Validate() == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestChoose(t *testing.T) {
	full := func(ids ...string) Catalog { return Catalog{Complete: true, IDs: ids} }
	cases := map[string]struct {
		fam     Family
		catalog Catalog
		policy  func(string) error
		wantID  string
		reason  string
		skipped []Skipped
	}{
		"moves to the priced successor": {
			fam: terra(), catalog: full("gpt-5.6-terra", "gpt-6.1-terra", "gpt-6.1-sol", "gpt-6.1-terra-2026-10-01"),
			wantID: "gpt-6.1-terra"},
		"an unpriced newer member is passed over": {
			fam: terra(), catalog: full("gpt-5.6-terra", "gpt-6.1-terra", "gpt-7-terra"),
			wantID: "gpt-6.1-terra", skipped: []Skipped{{"gpt-7-terra", ReasonUnpriced}}},
		"a prefix catch-all is not a price": {
			fam:     Family{Name: "any", Provider: "openai", Pattern: "gpt-5.6-nova-*", MaxInput: 100, MaxOutput: 100},
			catalog: full("gpt-5.6-nova-2"), reason: ReasonNoEligible, skipped: []Skipped{{"gpt-5.6-nova-2", ReasonUnpriced}}},
		"input over the ceiling": {
			fam:     Family{Name: "terra", Provider: "openai", Pattern: "gpt-*-terra", MaxInput: 1.5, MaxOutput: 18},
			catalog: full("gpt-5.6-terra", "gpt-6.1-terra"), reason: ReasonNoEligible,
			skipped: []Skipped{{"gpt-6.1-terra", ReasonOverInput}, {"gpt-5.6-terra", ReasonOverInput}}},
		"the long-context output rate is the one bounded": {
			fam:     Family{Name: "terra", Provider: "openai", Pattern: "gpt-*-terra", MaxInput: 4, MaxOutput: 7},
			catalog: full("gpt-6.1-terra"), reason: ReasonNoEligible, skipped: []Skipped{{"gpt-6.1-terra", ReasonOverOutput}}},
		"a base-tier entry is not a ceiling": {
			fam:     Family{Name: "pro", Provider: "google", Pattern: "gemini-*-pro", MaxInput: 100, MaxOutput: 100},
			catalog: full("gemini-2.5-pro"), reason: ReasonNoEligible, skipped: []Skipped{{"gemini-2.5-pro", ReasonPriceEstimate}}},
		"data policy refuses the newer member": {
			fam: terra(), catalog: full("gpt-5.6-terra", "gpt-6.1-terra"),
			policy: func(id string) error {
				if id == "gpt-6.1-terra" {
					return errors.New("no")
				}
				return nil
			},
			wantID: "gpt-5.6-terra", skipped: []Skipped{{"gpt-6.1-terra", ReasonPolicy}}},
		"an incomplete catalog moves nothing": {
			fam: terra(), catalog: Catalog{Complete: false, IDs: []string{"gpt-6.1-terra"}}, reason: ReasonCatalogIncomplete},
		"no member": {fam: terra(), catalog: full("gpt-6.1-sol"), reason: ReasonNoMember},
		"equal versions are a tie": {
			fam: terra(), catalog: full("gpt-5.6-terra", "gpt-6-terra", "gpt-6.0-terra"), reason: ReasonVersionTie,
			skipped: []Skipped{{"gpt-6-terra", ReasonVersionTie}, {"gpt-6.0-terra", ReasonVersionTie}}},
		"a repeated id is one member": {
			fam: terra(), catalog: full("gpt-6.1-terra", "gpt-6.1-terra"), wantID: "gpt-6.1-terra"},
		"a hostile id is not a member": {
			fam: terra(), catalog: full("gpt-9-terra:online", "anthropic/gpt-9-terra", "gpt-9-terra\n", "gpt-5.6-terra"),
			wantID: "gpt-5.6-terra"},
		"another provider's price does not count": {
			fam:     Family{Name: "terra", Provider: "vercel", Pattern: "gpt-*-terra", MaxInput: 4, MaxOutput: 18},
			catalog: full("gpt-6.1-terra"), reason: ReasonNoEligible, skipped: []Skipped{{"gpt-6.1-terra", ReasonUnpriced}}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := tc.fam.Choose(tc.catalog, table(t), tc.policy)
			if got.ID != tc.wantID || got.Reason != tc.reason {
				t.Fatalf("decision = %+v", got)
			}
			if len(got.Skipped) != len(tc.skipped) {
				t.Fatalf("skipped = %+v, want %+v", got.Skipped, tc.skipped)
			}
			for i := range tc.skipped {
				if got.Skipped[i] != tc.skipped[i] {
					t.Fatalf("skipped = %+v, want %+v", got.Skipped, tc.skipped)
				}
			}
			if got.ID != "" && (got.Price.Model != got.ID || got.Price.MaxInput > tc.fam.MaxInput || got.Price.MaxOutput > tc.fam.MaxOutput) {
				t.Fatalf("chosen price = %+v", got.Price)
			}
		})
	}
}

func TestParsePricesRefusesWhatItCannotBound(t *testing.T) {
	good := entry("openai", "gpt-6.1-terra", 1, 5, 2, 7.5, "all")
	for name, raw := range map[string]string{
		"not json":         `{`,
		"another schema":   strings.Replace(exportHead, `"schema":1`, `"schema":2`, 1) + good + "]}",
		"another currency": strings.Replace(exportHead, "USD", "EUR", 1) + good + "]}",
		"another unit":     strings.Replace(exportHead, "per_million_tokens", "per_token", 1) + good + "]}",
		"another tier":     strings.Replace(exportHead, `"standard"`, `"priority"`, 1) + good + "]}",
		"cached tokens":    strings.Replace(exportHead, "uncached_text", "cached_text", 1) + good + "]}",
		"negative rate":    exportHead + entry("openai", "m", -1, 5, 2, 7.5, "all") + "]}",
		"maximum below":    exportHead + entry("openai", "m", 3, 5, 2, 7.5, "all") + "]}",
		"duplicate model":  exportHead + good + "," + entry("openai", "gpt-6.1-terra", 9, 9, 9, 9, "all") + "]}",
		"no model":         exportHead + entry("openai", "", 1, 5, 2, 7.5, "all") + "]}",
		"too large":        exportHead + good + "]}" + strings.Repeat(" ", MaxPriceExportBytes),
		"trailing value":   exportHead + good + "]}" + exportHead + good + "]}",
		"trailing text":    exportHead + good + "]} x",
		"missing rate":     exportHead + `{"provider":"openai","model":"m","input":1,"output":5,"max_input":2,"context_tiers":"all"}` + "]}",
		"null rate":        exportHead + `{"provider":"openai","model":"m","input":null,"output":5,"max_input":2,"max_output":7,"context_tiers":"all"}` + "]}",
		"bad threshold":    exportHead + `{"provider":"openai","model":"m","input":1,"output":5,"max_input":2,"max_output":7,"context_tiers":"all","long_context_threshold_tokens":-1}` + "]}",
	} {
		if p, err := ParsePrices([]byte(raw)); err == nil {
			t.Errorf("%s: accepted %+v", name, p)
		}
	}
	p := prices(t, good)
	if p.Version != "v0.16.0" || p.Revision != "abc" {
		t.Fatalf("build = %q %q", p.Version, p.Revision)
	}
	if _, ok := p.Exact("openai", "gpt-6.1-terra-2026-10-01"); ok {
		t.Fatal("a longer id matched an entry")
	}
}
