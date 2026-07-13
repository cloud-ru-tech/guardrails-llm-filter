package models

// Replacement is one unique original->synthetic mapping produced by masking.
type Replacement struct {
	RuleID      string `json:"rule_id"`
	Original    string `json:"original"`
	Placeholder string `json:"placeholder"` // placeholder in <NAME_N> format
}

// MaskingState is the state of the masking process for a request.
// JSON tags define the wire format used by external masking-state stores
// (redis/postgres) and must stay stable across versions.
type MaskingState struct {
	TriggeredRuleIDs   []string      `json:"triggered_rule_ids"`
	TriggeredDataTypes []DataType    `json:"triggered_data_types"`
	Replacements       []Replacement `json:"replacements"`
	// Format is the API wire format resolved in the request phase. It is
	// persisted so a replica that only runs the response phase (cross-replica
	// store fallback) can select the correct demask/SSE processor instead of
	// defaulting to chat-completions and leaking placeholders.
	Format APIFormat `json:"format,omitempty"`
}

// IsEmpty reports whether any masking occurred. Format is metadata only and
// does not, by itself, make the state non-empty.
func (s *MaskingState) IsEmpty() bool {
	return len(s.TriggeredRuleIDs) == 0 && len(s.TriggeredDataTypes) == 0 && len(s.Replacements) == 0
}
