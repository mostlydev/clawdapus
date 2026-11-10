package modelfamily

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
)

// ContextTiersAll marks a price entry that encodes every context-length tier
// the table knows, so its maximum rates can bound a choice.
const ContextTiersAll = "all"

// MaxPriceExportBytes bounds the price export read from cllama.
const MaxPriceExportBytes = 1 << 20

// Price is one exact entry of cllama's compiled price table.
type Price struct {
	Provider     string  `json:"provider"`
	Model        string  `json:"model"`
	Input        float64 `json:"input"`
	Output       float64 `json:"output"`
	MaxInput     float64 `json:"max_input"`
	MaxOutput    float64 `json:"max_output"`
	ContextTiers string  `json:"context_tiers"`
}

// Prices is the price table exported by the pinned cllama.
type Prices struct {
	Version  string
	Revision string
	entries  map[string]Price
}

// Exact returns the entry whose model is exactly id. A prefix match is how
// cllama estimates cost; it is not a price for this purpose.
func (p *Prices) Exact(provider, id string) (Price, bool) {
	if p == nil {
		return Price{}, false
	}
	price, ok := p.entries[provider+"\x00"+id]
	return price, ok
}

// ParsePrices reads the output of "cllama prices --json". It accepts only
// the schema and scope this package bounds against, and refuses a table with
// an invalid rate or two entries for one model.
func ParsePrices(raw []byte) (*Prices, error) {
	if len(raw) > MaxPriceExportBytes {
		return nil, errors.New("price export is too large")
	}
	var export struct {
		Schema int `json:"schema"`
		Cllama struct {
			Version  string `json:"version"`
			Revision string `json:"revision"`
		} `json:"cllama"`
		Currency string `json:"currency"`
		Unit     string `json:"unit"`
		Scope    struct {
			ServiceTier string `json:"service_tier"`
			Tokens      string `json:"tokens"`
		} `json:"scope"`
		Entries []struct {
			Price
			LongContextThresholdTokens int `json:"long_context_threshold_tokens"`
		} `json:"entries"`
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := dec.Decode(&export); err != nil {
		return nil, errors.New("price export is not valid JSON")
	}
	if export.Schema != 1 || export.Currency != "USD" || export.Unit != "per_million_tokens" {
		return nil, errors.New("price export has an unsupported schema, currency or unit")
	}
	if export.Scope.ServiceTier != "standard" || export.Scope.Tokens != "uncached_text" {
		return nil, errors.New("price export does not cover standard uncached text rates")
	}
	out := &Prices{Version: export.Cllama.Version, Revision: export.Cllama.Revision, entries: map[string]Price{}}
	for _, e := range export.Entries {
		if e.Provider == "" || e.Model == "" {
			return nil, errors.New("price export has an entry without a provider or model")
		}
		for _, v := range []float64{e.Input, e.Output, e.MaxInput, e.MaxOutput} {
			if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
				return nil, fmt.Errorf("price export entry %s/%s has an invalid rate", e.Provider, e.Model)
			}
		}
		if e.MaxInput < e.Input || e.MaxOutput < e.Output {
			return nil, fmt.Errorf("price export entry %s/%s has a maximum below its base rate", e.Provider, e.Model)
		}
		key := e.Provider + "\x00" + e.Model
		if _, dup := out.entries[key]; dup {
			return nil, fmt.Errorf("price export lists %s/%s twice", e.Provider, e.Model)
		}
		out.entries[key] = e.Price
	}
	return out, nil
}
