package common

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// DemaskJSONArguments demasks a tool-call arguments JSON string, keeping the
// result valid JSON even when a restored original contains a JSON metacharacter
// (", \, control char) that would break a raw-text substitution.
//
// It first tries a fast raw substitution with a fresh demasker; if that yields
// invalid JSON it falls back to a structural demask (parse -> demask each string
// leaf -> re-marshal with MarshalNoEscape), which re-escapes each restored value
// for its JSON-string context. Object key order may change (Go marshals map keys
// sorted), which is semantically irrelevant for tool arguments.
//
// Returns (demasked, true) on success, or ("", false) when even the structural
// fallback fails — callers keep the masked value in that case (fail-open on
// content we cannot safely rewrite, rather than leaking a placeholder). A fresh
// demasker is created for each pass because DemaskChunk(flush=true) consumes the
// instance's buffered state.
//
// This is the single source of truth shared by the full-body demask path and the
// Responses SSE processor so stream and non-stream tool arguments demask
// identically.
func DemaskJSONArguments(ctx context.Context, newDemasker DemaskerFactoryFn, masked string) (string, bool) {
	if masked == "" {
		return "", false
	}

	naive, err := newDemasker().DemaskChunk(ctx, masked, true)
	if err == nil && json.Valid([]byte(naive)) {
		return naive, true
	}

	structural, serr := demaskArgumentsStructural(ctx, newDemasker(), masked)
	if serr != nil {
		return "", false
	}
	return structural, true
}

// demaskArgumentsStructural demasks a tool-call arguments object without risking
// invalid JSON. The MASKED arguments are always valid JSON (placeholders are
// JSON-safe), so this parses them, demasks each decoded string value, and
// re-marshals — re-marshaling escapes a restored original containing a quote,
// backslash or control char for its JSON-string context. Numbers are decoded as
// json.Number so their literal form is preserved.
func demaskArgumentsStructural(ctx context.Context, d Demasker, maskedArgs string) (string, error) {
	dec := json.NewDecoder(strings.NewReader(maskedArgs))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return "", fmt.Errorf("parse masked arguments: %w", err)
	}

	demaskValue := func(s string) (string, error) {
		// flush=true consumes the whole value, leaving no pending tail to leak
		// into the next value processed by this demasker.
		return d.DemaskChunk(ctx, s, true)
	}

	v, err := demaskJSONStringLeaves(v, demaskValue)
	if err != nil {
		return "", err
	}
	out, err := MarshalNoEscape(v)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// demaskJSONStringLeaves walks a decoded JSON value and demasks every string
// leaf (placeholders only ever sit inside string values). Objects and arrays are
// recursed; numbers, booleans and null are returned unchanged.
func demaskJSONStringLeaves(v any, demask func(string) (string, error)) (any, error) {
	switch t := v.(type) {
	case string:
		return demask(t)
	case []any:
		for i, e := range t {
			de, err := demaskJSONStringLeaves(e, demask)
			if err != nil {
				return nil, err
			}
			t[i] = de
		}
		return t, nil
	case map[string]any:
		for k, e := range t {
			de, err := demaskJSONStringLeaves(e, demask)
			if err != nil {
				return nil, err
			}
			t[k] = de
		}
		return t, nil
	default:
		return v, nil
	}
}
