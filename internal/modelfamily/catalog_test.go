package modelfamily

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFetchCatalog(t *testing.T) {
	var hits, elsewhere int
	var auth string
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		elsewhere++
		auth = r.Header.Get("Authorization")
		_, _ = io.WriteString(w, `{"data":[{"id":"gpt-9-terra"}]}`)
	}))
	defer other.Close()
	reply := `{"object":"list","data":[{"id":"gpt-5.6-terra"},{"id":"gpt-6.1-terra"}]}`
	status := 200
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.URL.Path != "/v1/models" || r.Header.Get("Authorization") != "Bearer sk-catalog-key" {
			http.Error(w, "no", 401)
			return
		}
		if status == 302 {
			http.Redirect(w, r, other.URL+"/v1/models", status)
			return
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, reply)
	}))
	defer srv.Close()

	got, err := FetchCatalog(context.Background(), nil, srv.URL+"/v1/", "sk-catalog-key")
	if err != nil || !got.Complete || strings.Join(got.IDs, ",") != "gpt-5.6-terra,gpt-6.1-terra" {
		t.Fatalf("catalog = %+v, %v", got, err)
	}

	big := `{"data":[` + strings.Repeat(`{"id":"`+strings.Repeat("m", 200)+`"},`, 30000)
	many := make([]string, 0, maxCatalogIDs+1)
	for i := 0; i <= maxCatalogIDs; i++ {
		many = append(many, fmt.Sprintf(`{"id":"m-%d"}`, i))
	}
	for name, tc := range map[string]struct {
		status int
		reply  string
	}{
		"redirect":          {302, ""},
		"server error":      {500, reply},
		"unauthorized":      {401, reply},
		"paged":             {200, `{"data":[{"id":"gpt-6.1-terra"}],"has_more":true}`},
		"paging not a bool": {200, `{"data":[{"id":"gpt-6.1-terra"}],"has_more":"no"}`},
		"repeated id":       {200, `{"data":[{"id":"gpt-6.1-terra"},{"id":"gpt-6.1-terra"}]}`},
		"id not a string":   {200, `{"data":[{"id":7}]}`},
		"empty id":          {200, `{"data":[{"id":""}]}`},
		"id too long":       {200, `{"data":[{"id":"` + strings.Repeat("m", maxCatalogIDBytes+1) + `"}]}`},
		"not a list":        {200, `{"models":[]}`},
		"states prices":     {200, `{"data":[{"id":"gpt-6.1-terra","pricing":{"input":"0.000009"}}]}`},
		"states a price":    {200, `{"data":[{"id":"gpt-6.1-terra","price":null},{"id":"x","price":3}]}`},
		"not json":          {200, `<html>`},
		"cut short":         {200, `{"data":[{"id":"gpt-6.1-terra"}`},
		"too large":         {200, big},
		"too many":          {200, `{"data":[` + strings.Join(many, ",") + `]}`},
	} {
		status, reply = tc.status, tc.reply
		got, err := FetchCatalog(context.Background(), nil, srv.URL+"/v1", "sk-catalog-key")
		if err == nil || got.Complete || len(got.IDs) != 0 {
			t.Errorf("%s: catalog = %+v, %v", name, got, err)
		}
		if err != nil && (strings.Contains(err.Error(), "sk-catalog-key") || strings.Contains(err.Error(), "gpt-")) {
			t.Errorf("%s: error carries the key or reply text: %v", name, err)
		}
	}
	if elsewhere != 0 || auth != "" {
		t.Fatalf("the key followed a redirect: hits=%d auth=%q", elsewhere, auth)
	}

	for name, base := range map[string]string{"no host": "https:///v1", "bad scheme": "ftp://x/v1", "userinfo": "https://u:p@x/v1", "empty": ""} {
		if got, err := FetchCatalog(context.Background(), nil, base, "k"); err == nil || got.Complete {
			t.Errorf("%s: accepted", name)
		}
	}
	before := hits
	if got, err := FetchCatalog(context.Background(), nil, srv.URL+"/v1", ""); err == nil || got.Complete || hits != before {
		t.Fatal("a listing was requested without a key")
	}
}

func TestLockRoundTripAndReuse(t *testing.T) {
	path := filepath.Join(t.TempDir(), LockFileName)
	lock, err := LoadLock(path)
	if err != nil || len(lock.Families) != 0 {
		t.Fatalf("missing lock: %+v %v", lock, err)
	}
	fam := terra()
	at := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	entry := LockEntry{Provider: "openai", Pattern: "gpt-*-terra", ID: "gpt-6.1-terra", PreviousID: "gpt-5.6-terra",
		MaxInput: 2, MaxOutput: 7.5, CatalogAt: at, CllamaTag: "v0.16.0", CllamaImageID: "sha256:abc"}
	lock.Families[LockKey("openai", "terra")] = entry
	if err := lock.Save(path); err != nil {
		t.Fatal(err)
	}
	again, err := LoadLock(path)
	if err != nil || again.Families["openai/@terra"] != entry {
		t.Fatalf("round trip: %+v %v", again, err)
	}

	p := table(t)
	if reason := fam.Reuse(entry, p, nil); reason != "" {
		t.Fatalf("a valid lock was refused: %s", reason)
	}
	// Only catalog freshness is waived. Everything else is judged as now.
	for name, tc := range map[string]struct {
		fam    Family
		entry  LockEntry
		policy func(string) error
		want   string
	}{
		"tightened input ceiling":  {Family{Name: "terra", Provider: "openai", Pattern: "gpt-*-terra", MaxInput: 1.5, MaxOutput: 18}, entry, nil, ReasonOverInput},
		"tightened output ceiling": {Family{Name: "terra", Provider: "openai", Pattern: "gpt-*-terra", MaxInput: 4, MaxOutput: 7}, entry, nil, ReasonOverOutput},
		"another pattern":          {Family{Name: "terra", Provider: "openai", Pattern: "gpt-*-sol", MaxInput: 4, MaxOutput: 18}, entry, nil, "family_changed"},
		"another provider":         {Family{Name: "terra", Provider: "vercel", Pattern: "gpt-*-terra", MaxInput: 4, MaxOutput: 18}, entry, nil, "family_changed"},
		"policy now refuses":       {fam, entry, func(string) error { return fmt.Errorf("no") }, ReasonPolicy},
		"no longer priced":         {fam, LockEntry{Provider: "openai", Pattern: "gpt-*-terra", ID: "gpt-7-terra"}, nil, ReasonUnpriced},
		"edited to a non-member":   {fam, LockEntry{Provider: "openai", Pattern: "gpt-*-terra", ID: "gpt-5.6"}, nil, ReasonNoMember},
		"empty":                    {fam, LockEntry{}, nil, ReasonNoMember},
	} {
		if got := tc.fam.Reuse(tc.entry, p, tc.policy); got != tc.want {
			t.Errorf("%s: reuse = %q, want %q", name, got, tc.want)
		}
	}
}
