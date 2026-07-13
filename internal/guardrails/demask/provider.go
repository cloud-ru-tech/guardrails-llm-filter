package demask

import (
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/models"
	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/guardrails/regex/scanners/placeholder"
)

//go:generate mockgen -source=provider.go -destination=mock_test.go -package=demask_test

// PlaceholderScanner finds placeholder matches for response demasking.
type PlaceholderScanner interface {
	Scan(text string, ruleIDs []string) ([]placeholder.Match, error)
}

// Registry resolves placeholder policy for rule IDs.
type Registry interface {
	GetMaxPlaceholderLenByRuleIDs(ruleIDs ...string) int
}

// Provider creates request-scoped demasker factories from app-scoped dependencies.
type Provider struct {
	reg Registry
	sc  PlaceholderScanner
}

// NewProvider creates a demasker provider from app-scoped dependencies.
func NewProvider(reg Registry, sc PlaceholderScanner) *Provider {
	return &Provider{
		reg: reg,
		sc:  sc,
	}
}

// NewFactory builds a request-scoped demasker factory from masking state.
func (p *Provider) NewFactory(state models.MaskingState) *Factory {
	if p == nil {
		return nil
	}
	return newFactory(newDemaskConfig(state, p.reg, p.sc))
}
