package registry

import (
	"sync/atomic"

	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/guardrails/regex/rule"
)

// Reloadable is a swappable Registry snapshot with lock-free reads.
//
// Readers always see a complete, immutable Registry; writers build a fresh
// Registry (see Build) and atomically publish it with Swap. Callers that need
// rule IDs and their compiled rules consistent within one request must use
// ResolveForDataTypes, which pins a single snapshot; combining separate
// GetRuleIDsByDataTypes and GetCompiledRulesByRuleIDs calls can straddle a Swap
// and drop a rule. Lookups by unknown IDs are silently skipped by Registry, so
// even a straddle never panics.
type Reloadable struct {
	cur atomic.Pointer[Registry]
}

// NewReloadable wraps an initial Registry snapshot.
func NewReloadable(initial *Registry) *Reloadable {
	r := &Reloadable{}
	r.cur.Store(initial)
	return r
}

// Swap atomically publishes a new Registry snapshot.
func (r *Reloadable) Swap(reg *Registry) {
	r.cur.Store(reg)
}

// Snapshot returns the current Registry snapshot.
func (r *Reloadable) Snapshot() *Registry {
	return r.cur.Load()
}

// The methods below delegate to the current snapshot so *Reloadable
// satisfies the same read interfaces as *Registry.

func (r *Reloadable) GetRulesByIDs(ruleIDs ...string) []rule.Rule {
	return r.Snapshot().GetRulesByIDs(ruleIDs...)
}

func (r *Reloadable) GetRuleIDsByDataTypes(dataTypes ...uint32) []string {
	return r.Snapshot().GetRuleIDsByDataTypes(dataTypes...)
}

func (r *Reloadable) GetRulesByDataTypes(dataTypes ...uint32) []rule.Rule {
	return r.Snapshot().GetRulesByDataTypes(dataTypes...)
}

func (r *Reloadable) GetMaskingPlaceholderByRuleID(ruleID string) string {
	return r.Snapshot().GetMaskingPlaceholderByRuleID(ruleID)
}

func (r *Reloadable) GetMaxPlaceholderLenByRuleIDs(ruleIDs ...string) int {
	return r.Snapshot().GetMaxPlaceholderLenByRuleIDs(ruleIDs...)
}

func (r *Reloadable) HasRulesForDataTypes(dataTypes []uint32) bool {
	return r.Snapshot().HasRulesForDataTypes(dataTypes)
}

func (r *Reloadable) GetCompiledRulesByDataTypes(dataTypes []uint32) ([]CompiledRule, bool) {
	return r.Snapshot().GetCompiledRulesByDataTypes(dataTypes)
}

func (r *Reloadable) GetCompiledRulesByRuleIDs(ruleIDs []string) []CompiledRule {
	return r.Snapshot().GetCompiledRulesByRuleIDs(ruleIDs)
}

// ResolveForDataTypes pins one snapshot and resolves rule IDs and their
// compiled rules against it, so a concurrent Swap cannot drop a rule between
// the two lookups.
func (r *Reloadable) ResolveForDataTypes(dataTypes []uint32) ([]string, []CompiledRule) {
	return r.Snapshot().ResolveForDataTypes(dataTypes)
}

func (r *Reloadable) PrefilterIneligibleRuleIDs() []string {
	return r.Snapshot().PrefilterIneligibleRuleIDs()
}
