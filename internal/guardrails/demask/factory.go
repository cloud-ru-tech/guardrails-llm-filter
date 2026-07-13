package demask

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/models"
	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/guardrails/regex/registry"
	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/guardrails/regex/scanners/placeholder"
)

// DemaskConfig holds data shared by all Demasker instances from one Factory.
type DemaskConfig struct {
	scan                  func(text string) ([]placeholder.Match, error)
	placeholderToOriginal map[string]string
	exactReplacer         *strings.Replacer
	placeholderToJSON     map[string]string
	jsonReplacer          *strings.Replacer
	maxPending            int
}

// Factory builds Demasker instances that share one DemaskConfig and keep separate pending buffers.
type Factory struct {
	cfg *DemaskConfig
}

func newFactory(cfg *DemaskConfig) *Factory {
	return &Factory{cfg: cfg}
}

// Demasker returns a new stream demasker backed by this factory's shared config.
func (f *Factory) Demasker() *Demasker {
	if f == nil {
		return nil
	}
	return NewDemasker(f.cfg)
}

// JSONDemasker returns a demasker whose restored originals are JSON-escaped
// for insertion into a JSON string context — required when the demasked text
// is a JSON fragment (e.g. Anthropic input_json_delta.partial_json), where an
// original containing a quote or backslash would otherwise corrupt the JSON
// the client accumulates.
func (f *Factory) JSONDemasker() *Demasker {
	if f == nil {
		return nil
	}
	d := NewDemasker(f.cfg)
	d.jsonCtx = true
	return d
}

// ruleResolver is the resolve-once fast path of the placeholder scanner
// (implemented by *placeholder.Scanner). When available, the compiled rules
// are resolved once per stream instead of on every DemaskChunk call — the
// rule set is fixed for the stream's lifetime because its placeholders were
// minted at request time from the same snapshot.
type ruleResolver interface {
	ResolveRules(ruleIDs []string) []registry.CompiledRule
	ScanRules(text string, rules []registry.CompiledRule) ([]placeholder.Match, error)
}

func newDemaskConfig(state models.MaskingState, reg Registry, scanner PlaceholderScanner) *DemaskConfig {
	ruleIDs := append([]string(nil), state.TriggeredRuleIDs...)
	sort.Strings(ruleIDs)

	cfg := &DemaskConfig{}
	cfg.buildLookupIndex(state.Replacements)
	maxPlaceholderLen := reg.GetMaxPlaceholderLenByRuleIDs(ruleIDs...)
	cfg.maxPending = max(cfg.maxPending, maxPlaceholderLen)

	switch {
	case len(ruleIDs) == 0:
		cfg.scan = nil
	case scanner == nil:
		cfg.scan = nil
	default:
		if rr, ok := scanner.(ruleResolver); ok {
			rules := rr.ResolveRules(ruleIDs)
			cfg.scan = func(text string) ([]placeholder.Match, error) {
				return rr.ScanRules(text, rules)
			}
		} else {
			cfg.scan = func(text string) ([]placeholder.Match, error) {
				return scanner.Scan(text, ruleIDs)
			}
		}
	}

	return cfg
}

// buildLookupIndex fills the placeholder->original indexes, in both verbatim
// and JSON-escaped variants, and sets maxPending to the longest placeholder.
func (cfg *DemaskConfig) buildLookupIndex(replacements []models.Replacement) {
	out := make(map[string]string, len(replacements))
	jsonOut := make(map[string]string, len(replacements))
	exactPairs := make([]string, 0, len(replacements)*2)
	jsonPairs := make([]string, 0, len(replacements)*2)

	for _, rep := range replacements {
		if rep.Placeholder == "" {
			continue
		}
		if _, exists := out[rep.Placeholder]; exists {
			continue
		}

		escaped := jsonEscapeString(rep.Original)
		out[rep.Placeholder] = rep.Original
		jsonOut[rep.Placeholder] = escaped
		exactPairs = append(exactPairs, rep.Placeholder, rep.Original)
		jsonPairs = append(jsonPairs, rep.Placeholder, escaped)
		if len(rep.Placeholder) > cfg.maxPending {
			cfg.maxPending = len(rep.Placeholder)
		}
	}

	cfg.placeholderToOriginal = out
	cfg.placeholderToJSON = jsonOut
	cfg.exactReplacer = strings.NewReplacer(exactPairs...)
	cfg.jsonReplacer = strings.NewReplacer(jsonPairs...)
}

// jsonEscapeString returns s encoded for insertion inside a JSON string
// literal (without the surrounding quotes). HTML escaping is disabled so
// restored originals survive byte-for-byte, matching MarshalNoEscape.
func jsonEscapeString(s string) string {
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(s); err != nil {
		return s // unreachable for a plain string
	}
	out := b.String()
	// Encode wraps in quotes and appends a newline.
	return out[1 : len(out)-2]
}
