package mask

import (
	"slices"
	"strings"

	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/models"
	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/guardrails/regex/placeholderfmt"
	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/guardrails/regex/scanners/sensitive"
)

// masker encapsulates per-request masking state: dedup maps and placeholder
// counters. Created once per Handle() call, used across all texts, then
// discarded after extracting results.
type masker struct {
	originalToPlaceholder map[string]string
	reps                  []models.Replacement
	placeholderCounters   map[string]int

	seenRules     map[string]struct{}
	seenDataTypes map[models.DataType]struct{}
}

func newMasker() *masker {
	return &masker{
		originalToPlaceholder: make(map[string]string),
		placeholderCounters:   make(map[string]int),
		seenRules:             make(map[string]struct{}),
		seenDataTypes:         make(map[models.DataType]struct{}),
	}
}

func (m *masker) maskText(text string, matches []sensitive.Match) string {
	if len(matches) == 0 {
		return text
	}

	var b strings.Builder
	b.Grow(len(text))
	pos := 0

	for _, match := range matches {
		if match.Start < pos || match.FullText == "" || match.Placeholder == "" {
			continue
		}

		original := match.FullText
		placeholder, created := m.placeholderForOriginal(original, match.Placeholder)

		if created {
			m.reps = append(m.reps, models.Replacement{
				RuleID:      match.RuleID,
				Original:    original,
				Placeholder: placeholder,
			})
		}

		m.seenRules[match.RuleID] = struct{}{}
		dataType := models.DataType(match.DataType)
		if dataType.IsValid() && dataType != models.DataTypeUNSPECIFIED {
			m.seenDataTypes[dataType] = struct{}{}
		}

		b.WriteString(text[pos:match.Start])
		b.WriteString(placeholder)
		pos = match.End
	}

	b.WriteString(text[pos:])
	return b.String()
}

// replacements returns the collected unique replacements.
func (m *masker) replacements() []models.Replacement {
	return m.reps
}

func (m *masker) triggeredRules() []string {
	triggered := make([]string, 0, len(m.seenRules))
	for id := range m.seenRules {
		triggered = append(triggered, id)
	}
	slices.Sort(triggered)
	return triggered
}

func (m *masker) triggeredDataTypes() []models.DataType {
	triggered := make([]models.DataType, 0, len(m.seenDataTypes))
	for dataType := range m.seenDataTypes {
		triggered = append(triggered, dataType)
	}
	slices.Sort(triggered)
	return triggered
}

func (m *masker) placeholderForOriginal(original, placeholderType string) (string, bool) {
	if placeholder, ok := m.originalToPlaceholder[original]; ok {
		return placeholder, false
	}

	placeholder := m.nextPlaceholder(placeholderType)
	m.originalToPlaceholder[original] = placeholder
	return placeholder, true
}

func (m *masker) nextPlaceholder(placeholderType string) string {
	m.placeholderCounters[placeholderType]++
	return placeholderfmt.Format(placeholderType, m.placeholderCounters[placeholderType])
}
