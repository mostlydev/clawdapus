package modelfamily

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const vercelListing = `{"object":"list","availability_status":"complete","catalog_status":"complete","data":[
 {"id":"openai/gpt-6.1-terra","type":"language","model_eligibility":{"status":"eligible"},
  "pricing":{"input":"0.000001","output":"0.000005","input_tiers":[{"cost":"0.000001","min":0,"max":272000},{"cost":"0.000002","min":272000}],"output_tiers":[{"cost":"0.0000075","min":272000}]}},
 {"id":"openai/gpt-5.6-terra","type":"language","model_eligibility":{"status":"eligible"},"pricing":{"input":"0.000002","output":"0.000012"}},
 {"id":"openai/gpt-7-terra","type":"language","model_eligibility":{"status":"ineligible","category":"policy"},"pricing":{"input":"0.000001","output":"0.000001"}},
 {"id":"openai/gpt-8-terra","type":"language","model_eligibility":{"status":"unknown"},"pricing":{"input":"0.000001","output":"0.000001"}},
 {"id":"openai/gpt-9-terra","type":"language","model_eligibility":{"status":"eligible"},"pricing":{"input":"0.000001","output":"0.000001","input_tiers":[{"cost":"cheap"}]}},
 {"id":"openai/gpt-10-terra","type":"language","model_eligibility":{"status":"eligible"},"pricing":{"input":0.000001,"output":"0.000001"}},
 {"id":"openai/gpt-11-terra","type":"language","model_eligibility":{"status":"eligible"}},
 {"id":"openai/text-embed-3","type":"embedding","model_eligibility":{"status":"eligible"},"pricing":{"input":"0.0000001"}}
]}`

func TestFetchVercelCatalog(t *testing.T) {
	reply, status := vercelListing, 200
	var query, auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query, auth = r.URL.RawQuery, r.Header.Get("Authorization")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, reply)
	}))
	defer srv.Close()

	catalog, prices, err := FetchVercelCatalog(context.Background(), nil, srv.URL+"/v1", "gw-key")
	if err != nil || !catalog.Complete {
		t.Fatalf("catalog: %+v %v", catalog, err)
	}
	if query != "include_availability" || auth != "Bearer gw-key" {
		t.Fatalf("request: query=%q auth=%q", query, auth)
	}
	// Members are language models the team may use. Ineligible, unknown and
	// non-language entries are not members.
	if got := strings.Join(catalog.IDs, ","); got != "openai/gpt-6.1-terra,openai/gpt-5.6-terra,openai/gpt-9-terra,openai/gpt-10-terra,openai/gpt-11-terra" {
		t.Fatalf("members = %s", got)
	}
	// The highest tier is the price, per million tokens.
	p, ok := prices.Exact("vercel", "openai/gpt-6.1-terra")
	if !ok || p.MaxInput != 2 || p.MaxOutput != 7.5 || p.ContextTiers != ContextTiersAll {
		t.Fatalf("6.1 terra = %+v %v", p, ok)
	}
	if p, ok := prices.Exact("vercel", "openai/gpt-5.6-terra"); !ok || p.MaxInput != 2 || p.MaxOutput != 12 {
		t.Fatalf("5.6 terra = %+v %v", p, ok)
	}
	// A malformed tier, a non-string price and a missing price are no price.
	for _, id := range []string{"openai/gpt-9-terra", "openai/gpt-10-terra", "openai/gpt-11-terra", "openai/gpt-7-terra"} {
		if _, ok := prices.Exact("vercel", id); ok {
			t.Errorf("%s has a price", id)
		}
	}

	fam := Family{Name: "terra", Provider: "vercel", Pattern: "openai/gpt-*-terra", MaxInput: 4, MaxOutput: 18}
	d := fam.Choose(catalog, prices, nil)
	if d.ID != "openai/gpt-6.1-terra" || len(d.Skipped) != 3 {
		t.Fatalf("decision = %+v", d)
	}

	for name, tc := range map[string]struct {
		status int
		reply  string
	}{
		"availability partial": {200, strings.Replace(vercelListing, `"availability_status":"complete"`, `"availability_status":"partial"`, 1)},
		"catalog partial":      {200, strings.Replace(vercelListing, `"catalog_status":"complete"`, `"catalog_status":"partial"`, 1)},
		"no annotations":       {200, `{"object":"list","data":[{"id":"openai/gpt-6.1-terra","type":"language"}]}`},
		"unauthorized":         {401, vercelListing},
		"rate limited":         {429, vercelListing},
		"repeated id":          {200, strings.Replace(vercelListing, "openai/gpt-5.6-terra", "openai/gpt-6.1-terra", 1)},
		"not json":             {200, "<html>"},
	} {
		reply, status = tc.reply, tc.status
		catalog, prices, err := FetchVercelCatalog(context.Background(), nil, srv.URL+"/v1", "gw-key")
		if err == nil || catalog.Complete || prices != nil {
			t.Errorf("%s: %+v %v", name, catalog, err)
		}
		if err != nil && strings.Contains(err.Error(), "gw-key") {
			t.Errorf("%s: error carries the key", name)
		}
	}
}

func TestFetchVercelEndpointMax(t *testing.T) {
	reply := `{"data":{"id":"openai/gpt-6.1-terra","endpoints":[
	 {"provider_name":"openai","pricing":{"prompt":"0.000001","completion":"0.000005"}},
	 {"provider_name":"azure","pricing":{"prompt":"0.0000015","completion":"0.000004"}}]}}`
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		_, _ = io.WriteString(w, reply)
	}))
	defer srv.Close()
	in, out, err := FetchVercelEndpointMax(context.Background(), nil, srv.URL+"/v1", "gw-key", "openai/gpt-6.1-terra")
	if err != nil || in != 1.5 || out != 5 || path != "/v1/models/openai/gpt-6.1-terra/endpoints" {
		t.Fatalf("max = %v %v %v path=%s", in, out, err, path)
	}
	for name, bad := range map[string]string{
		"no endpoints":           `{"data":{"id":"openai/gpt-6.1-terra","endpoints":[]}}`,
		"endpoint without price": `{"data":{"id":"openai/gpt-6.1-terra","endpoints":[{"pricing":{"prompt":"0.000001","completion":"0.000005"}},{"provider_name":"x"}]}}`,
		"missing completion":     `{"data":{"id":"openai/gpt-6.1-terra","endpoints":[{"pricing":{"prompt":"0.000001"}}]}}`,
		"malformed price":        `{"data":{"id":"openai/gpt-6.1-terra","endpoints":[{"pricing":{"prompt":"free","completion":"0.000005"}}]}}`,
		"negative price":         `{"data":{"id":"openai/gpt-6.1-terra","endpoints":[{"pricing":{"prompt":"-0.000001","completion":"0.000005"}}]}}`,
		"overflowing price":      `{"data":{"id":"openai/gpt-6.1-terra","endpoints":[{"pricing":{"prompt":"1e400","completion":"0.000005"}}]}}`,
		"another model":          `{"data":{"id":"openai/gpt-5.6-terra","endpoints":[{"pricing":{"prompt":"0.000001","completion":"0.000005"}}]}}`,
	} {
		reply = bad
		if in, out, err := FetchVercelEndpointMax(context.Background(), nil, srv.URL+"/v1", "gw-key", "openai/gpt-6.1-terra"); err == nil {
			t.Errorf("%s: accepted %v %v", name, in, out)
		}
	}
}

func TestReuseRecorded(t *testing.T) {
	fam := Family{Name: "terra", Provider: "vercel", Pattern: "openai/gpt-*-terra", MaxInput: 4, MaxOutput: 18}
	entry := LockEntry{Provider: "vercel", Pattern: "openai/gpt-*-terra", ID: "openai/gpt-6.1-terra", MaxInput: 2, MaxOutput: 7.5, PriceSource: "catalog"}
	if got := fam.ReuseRecorded(entry); got != "" {
		t.Fatalf("valid entry: %s", got)
	}
	tight := fam
	tight.MaxInput = 1.5
	noSource := entry
	noSource.PriceSource = ""
	other := entry
	other.ID = "openai/gpt-6.1-sol"
	for name, tc := range map[string]struct {
		fam   Family
		entry LockEntry
		want  string
	}{
		"ceiling lowered":  {tight, entry, ReasonOverInput},
		"no catalog price": {fam, noSource, ReasonUnpriced},
		"not a member":     {fam, other, ReasonNoMember},
		"empty":            {fam, LockEntry{}, ReasonNoMember},
	} {
		if got := tc.fam.ReuseRecorded(tc.entry); got != tc.want {
			t.Errorf("%s: %q, want %q", name, got, tc.want)
		}
	}
}
