// Package builtins provides an immutable index over the built-in rules loaded
// from the YAML files at startup. The rule use cases consume the narrow slice
// of it they need (IsBuiltin / List / Get) via their own dependency interfaces;
// the concrete Index satisfies them implicitly.
package builtins

import "github.com/cloud-ru-tech/guardrails-llm-filter/pkg/guardrails/regex/rule"

// Index is a read-only lookup over the built-in rules. It is safe for
// concurrent use: it is built once at startup and never mutated.
type Index struct {
	rules []rule.Rule
	byID  map[string]rule.Rule
}

// New builds an Index over the given built-in rules.
func New(fileRules []rule.Rule) *Index {
	byID := make(map[string]rule.Rule, len(fileRules))
	for _, r := range fileRules {
		byID[r.ID] = r
	}
	return &Index{rules: fileRules, byID: byID}
}

// IsBuiltin reports whether the ID belongs to a built-in rule.
func (i *Index) IsBuiltin(id string) bool {
	_, ok := i.byID[id]
	return ok
}

// List returns a copy of the built-in rules.
func (i *Index) List() []rule.Rule {
	out := make([]rule.Rule, len(i.rules))
	copy(out, i.rules)
	return out
}

// Get returns the built-in rule with the given ID, or ok=false if none.
func (i *Index) Get(id string) (rule.Rule, bool) {
	r, ok := i.byID[id]
	return r, ok
}
