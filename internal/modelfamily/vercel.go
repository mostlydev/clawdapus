package modelfamily

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// VercelOfficialBaseURL is the AI Gateway's API root.
const VercelOfficialBaseURL = "https://ai-gateway.vercel.sh/v1"

type vercelTier struct {
	Cost any `json:"cost"`
}

type vercelPricing struct {
	Input       any          `json:"input"`
	Output      any          `json:"output"`
	InputTiers  []vercelTier `json:"input_tiers"`
	OutputTiers []vercelTier `json:"output_tiers"`
}

// perMillion parses a gateway price, a decimal string in USD per token, and
// returns it per million tokens. Anything else is not a price.
func perMillion(v any) (float64, bool) {
	s, ok := v.(string)
	if !ok || s == "" || len(s) > 32 {
		return 0, false
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) || f < 0 {
		return 0, false
	}
	f *= 1e6
	if math.IsInf(f, 0) || f > 1e9 {
		return 0, false
	}
	return f, true
}

// maxRate is the highest of a base price and every tier. A tier that is not
// a price makes the whole rate unknown: it is never skipped.
func maxRate(base any, tiers []vercelTier) (float64, bool) {
	highest, ok := perMillion(base)
	if !ok {
		return 0, false
	}
	for _, tier := range tiers {
		cost, ok := perMillion(tier.Cost)
		if !ok {
			return 0, false
		}
		highest = math.Max(highest, cost)
	}
	return highest, true
}

func vercelGet(ctx context.Context, client *http.Client, rawURL, apiKey string, into any) error {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" || u.Scheme != "https" && u.Scheme != "http" || u.User != nil {
		return errors.New("gateway endpoint is not a usable URL")
	}
	if apiKey == "" {
		return errors.New("no key for the gateway")
	}
	if client == nil {
		client = &http.Client{}
	}
	bounded := *client
	bounded.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	ctx, cancel := context.WithTimeout(ctx, catalogTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return errors.New("gateway endpoint is not a usable URL")
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Accept", "application/json")
	resp, err := bounded.Do(req)
	if err != nil {
		return errors.New("gateway listing could not be read")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return errors.New("gateway listing was refused or redirected")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxCatalogBytes+1))
	if err != nil || len(body) > maxCatalogBytes {
		return errors.New("gateway listing is too large or was cut short")
	}
	if json.Unmarshal(body, into) != nil {
		return errors.New("gateway listing is not valid JSON")
	}
	return nil
}

// FetchVercelCatalog reads the AI Gateway's model list with the team's
// availability annotations. The members it returns are language models the
// team may use; the price table holds, for each that states a whole price,
// its highest listed input and output rate. A model with a missing or
// malformed price or tier is listed with no price, so it cannot be chosen.
// An annotated listing that the gateway itself calls partial is incomplete.
func FetchVercelCatalog(ctx context.Context, client *http.Client, baseURL, apiKey string) (Catalog, *Prices, error) {
	var listing struct {
		Data []struct {
			ID          any            `json:"id"`
			Type        string         `json:"type"`
			Pricing     *vercelPricing `json:"pricing"`
			Eligibility *struct {
				Status string `json:"status"`
			} `json:"model_eligibility"`
		} `json:"data"`
		AvailabilityStatus string `json:"availability_status"`
		CatalogStatus      string `json:"catalog_status"`
	}
	if err := vercelGet(ctx, client, strings.TrimRight(baseURL, "/")+"/models?include_availability", apiKey, &listing); err != nil {
		return Catalog{}, nil, err
	}
	if listing.Data == nil || listing.AvailabilityStatus != "complete" || listing.CatalogStatus != "complete" {
		return Catalog{}, nil, errors.New("gateway listing is partial")
	}
	if len(listing.Data) > maxCatalogIDs {
		return Catalog{}, nil, errors.New("gateway listing has too many entries")
	}
	seen := map[string]bool{}
	prices := &Prices{entries: map[string]Price{}}
	var ids []string
	for _, item := range listing.Data {
		id, ok := item.ID.(string)
		if !ok || id == "" || len(id) > maxCatalogIDBytes {
			return Catalog{}, nil, errors.New("gateway listing has a malformed entry")
		}
		if seen[id] {
			return Catalog{}, nil, errors.New("gateway listing repeats an entry")
		}
		seen[id] = true
		if item.Type != "language" || item.Eligibility == nil || item.Eligibility.Status != "eligible" {
			continue
		}
		ids = append(ids, id)
		if item.Pricing == nil {
			continue
		}
		in, okIn := maxRate(item.Pricing.Input, item.Pricing.InputTiers)
		out, okOut := maxRate(item.Pricing.Output, item.Pricing.OutputTiers)
		if !okIn || !okOut {
			continue
		}
		prices.entries["vercel\x00"+id] = Price{Provider: "vercel", Model: id, Input: in, Output: out, MaxInput: in, MaxOutput: out, ContextTiers: ContextTiersAll}
	}
	return Catalog{Complete: true, IDs: ids}, prices, nil
}

// FetchVercelEndpointMax returns the highest prompt and completion price,
// per million tokens, among the providers that serve a gateway model. The
// gateway may route to any of them. A listing with no endpoint, or with one
// whose price is missing or malformed, yields no price.
func FetchVercelEndpointMax(ctx context.Context, client *http.Client, baseURL, apiKey, id string) (maxInput, maxOutput float64, err error) {
	var listing struct {
		Data struct {
			ID        string `json:"id"`
			Endpoints []struct {
				Pricing *struct {
					Prompt          any          `json:"prompt"`
					Completion      any          `json:"completion"`
					PromptTiers     []vercelTier `json:"prompt_tiers"`
					CompletionTiers []vercelTier `json:"completion_tiers"`
				} `json:"pricing"`
			} `json:"endpoints"`
		} `json:"data"`
	}
	if err := vercelGet(ctx, client, strings.TrimRight(baseURL, "/")+"/models/"+id+"/endpoints", apiKey, &listing); err != nil {
		return 0, 0, err
	}
	if listing.Data.ID != id || len(listing.Data.Endpoints) == 0 || len(listing.Data.Endpoints) > 256 {
		return 0, 0, errors.New("gateway endpoint listing is incomplete")
	}
	for _, endpoint := range listing.Data.Endpoints {
		if endpoint.Pricing == nil {
			return 0, 0, errors.New("gateway endpoint listing has an endpoint with no price")
		}
		in, okIn := maxRate(endpoint.Pricing.Prompt, endpoint.Pricing.PromptTiers)
		out, okOut := maxRate(endpoint.Pricing.Completion, endpoint.Pricing.CompletionTiers)
		if !okIn || !okOut {
			return 0, 0, errors.New("gateway endpoint listing has a malformed price")
		}
		maxInput, maxOutput = math.Max(maxInput, in), math.Max(maxOutput, out)
	}
	return maxInput, maxOutput, nil
}

// Raise lifts an entry's maximum rates to at least the given figures. It is
// how a higher endpoint or export price replaces a lower listed one.
func (p *Prices) Raise(provider, id string, maxInput, maxOutput float64) {
	key := provider + "\x00" + id
	if price, ok := p.entries[key]; ok {
		price.MaxInput, price.MaxOutput = math.Max(price.MaxInput, maxInput), math.Max(price.MaxOutput, maxOutput)
		p.entries[key] = price
	}
}

// Drop removes an entry, so the model is treated as unpriced.
func (p *Prices) Drop(provider, id string) { delete(p.entries, provider+"\x00"+id) }
