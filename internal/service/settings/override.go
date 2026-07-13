package settings

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/models"
)

// OverrideNone is the special override header value that disables guardrails
// for a single request.
const OverrideNone = "none"

// Effective resolves the per-request settings from the global settings and
// the optional override header value.
//
// The override is NARROW-ONLY: the result is the intersection of the global
// data types and the header's data types. A header can never enable a data
// type the operator disabled globally, nor turn guardrails on when they are
// globally disabled. The special value "none" skips guardrails for the
// request.
//
// The header is trusted gateway input, not client input: Envoy MUST be
// configured to strip it from incoming client requests
// (request_headers_to_remove). On any parse error the whole header is
// ignored and the full global settings apply — garbage fails toward more
// protection, never less.
func Effective(global models.GuardrailsSettings, headerValue string) models.EffectiveSettings {
	// Mode is global-only: the header can narrow data types or skip the
	// request, but never flip detect/enforce.
	eff := models.EffectiveSettings(global)
	if !global.Enabled {
		return models.EffectiveSettings{Enabled: false, Mode: global.Mode}
	}

	headerValue = strings.TrimSpace(headerValue)
	if headerValue == "" {
		return eff
	}

	if strings.EqualFold(headerValue, OverrideNone) {
		return models.EffectiveSettings{Enabled: false, Mode: global.Mode}
	}

	requested, err := ParseDataTypes(headerValue)
	if err != nil {
		// Unparsable override → ignore it entirely, keep full protection.
		return eff
	}

	inGlobal := make(map[models.DataType]bool, len(global.DataTypes))
	for _, dt := range global.DataTypes {
		inGlobal[dt] = true
	}

	var narrowed []models.DataType
	for _, dt := range requested {
		if inGlobal[dt] {
			narrowed = append(narrowed, dt)
		}
	}

	if len(narrowed) == 0 {
		// Intersection is empty: nothing to scan for this request.
		return models.EffectiveSettings{Enabled: false, Mode: global.Mode}
	}

	eff.DataTypes = narrowed
	return eff
}

// ParseDataTypes parses a comma-separated list of data types, accepting both
// numeric enum values ("1,4") and names ("credentials,ip_addresses"),
// case-insensitively. It returns an error on the first unknown token.
func ParseDataTypes(s string) ([]models.DataType, error) {
	tokens := strings.Split(s, ",")
	out := make([]models.DataType, 0, len(tokens))
	seen := make(map[models.DataType]bool, len(tokens))

	for _, token := range tokens {
		token = strings.TrimSpace(token)
		if token == "" {
			continue
		}

		var dt models.DataType
		if n, err := strconv.ParseUint(token, 10, 32); err == nil {
			dt = models.DataType(n)
			if !dt.IsValid() || dt == models.DataTypeUNSPECIFIED {
				return nil, fmt.Errorf("unknown data type %q", token)
			}
		} else {
			parsed, err := models.ParseDataType(strings.ToUpper(token))
			if err != nil || parsed == models.DataTypeUNSPECIFIED {
				return nil, fmt.Errorf("unknown data type %q", token)
			}
			dt = parsed
		}

		if !seen[dt] {
			seen[dt] = true
			out = append(out, dt)
		}
	}

	if len(out) == 0 {
		return nil, fmt.Errorf("no data types in %q", s)
	}
	return out, nil
}
