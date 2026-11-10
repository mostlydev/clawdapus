package modelfamily

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	maxCatalogBytes   = 4 << 20
	maxCatalogIDs     = 5000
	maxCatalogIDBytes = 256
	catalogTimeout    = 20 * time.Second
)

// FetchCatalog reads a provider's model listing from its OpenAI-compatible
// "models" endpoint. The key is sent to that endpoint only: redirects are
// not followed. The read is bounded in time and size. Anything short of one
// whole, well-formed listing returns an incomplete catalog, which moves
// nothing; the error says why and never carries the key or reply text.
func FetchCatalog(ctx context.Context, client *http.Client, baseURL, apiKey string) (Catalog, error) {
	incomplete := func(msg string) (Catalog, error) { return Catalog{}, errors.New(msg) }
	base, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil || base.Host == "" || (base.Scheme != "https" && base.Scheme != "http") || base.User != nil {
		return incomplete("provider endpoint is not a usable URL")
	}
	if apiKey == "" {
		return incomplete("no key for the provider")
	}
	if client == nil {
		client = &http.Client{}
	}
	bounded := *client
	bounded.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	ctx, cancel := context.WithTimeout(ctx, catalogTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base.String()+"/models", nil)
	if err != nil {
		return incomplete("provider endpoint is not a usable URL")
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Accept", "application/json")
	resp, err := bounded.Do(req)
	if err != nil {
		return incomplete("model listing could not be read")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return incomplete("model listing was refused or redirected")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxCatalogBytes+1))
	if err != nil {
		return incomplete("model listing was cut short")
	}
	if len(body) > maxCatalogBytes {
		return incomplete("model listing is too large")
	}
	var listing struct {
		Data []struct {
			ID any `json:"id"`
			// A listing that states prices is one this reader does not
			// bound against. It is refused, never read as if it had none.
			Pricing json.RawMessage `json:"pricing"`
			Price   json.RawMessage `json:"price"`
		} `json:"data"`
		HasMore any `json:"has_more"`
	}
	if json.Unmarshal(body, &listing) != nil || listing.Data == nil {
		return incomplete("model listing is not a list")
	}
	// A paged listing is not read further: one page is not the whole catalog.
	if more, ok := listing.HasMore.(bool); listing.HasMore != nil && (!ok || more) {
		return incomplete("model listing is paged")
	}
	if len(listing.Data) > maxCatalogIDs {
		return incomplete("model listing has too many entries")
	}
	seen := map[string]bool{}
	ids := make([]string, 0, len(listing.Data))
	for _, item := range listing.Data {
		id, ok := item.ID.(string)
		if !ok || id == "" || len(id) > maxCatalogIDBytes {
			return incomplete("model listing has a malformed entry")
		}
		if seen[id] {
			return incomplete("model listing repeats an entry")
		}
		if item.Pricing != nil || item.Price != nil {
			return incomplete("model listing states prices, which this version does not bound against")
		}
		seen[id] = true
		ids = append(ids, id)
	}
	return Catalog{Complete: true, IDs: ids}, nil
}
