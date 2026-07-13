package list

import (
	"context"
	"fmt"
	"sort"

	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/guardrails/regex/rule"
)

// Handle returns the requested rule sources, each annotated with its source
// and enabled state. Custom rules are sorted by ID.
func (uc *UseCase) Handle(ctx context.Context, cmd Command) (CommandResponse, error) {
	disabled, err := uc.store.ListDisabledRuleIDs(ctx)
	if err != nil {
		return CommandResponse{}, fmt.Errorf("list disabled rule ids: %w", err)
	}
	off := make(map[string]struct{}, len(disabled))
	for _, id := range disabled {
		off[id] = struct{}{}
	}

	views := make([]RuleView, 0)
	if cmd.IncludeBuiltin {
		for _, r := range uc.builtins.List() {
			views = append(views, toView(r, true, off))
		}
	}
	if cmd.IncludeCustom {
		custom, err := uc.store.ListRules(ctx)
		if err != nil {
			return CommandResponse{}, fmt.Errorf("list custom rules: %w", err)
		}
		sort.Slice(custom, func(i, j int) bool { return custom[i].ID < custom[j].ID })
		for _, r := range custom {
			views = append(views, toView(r, false, off))
		}
	}
	return CommandResponse{Rules: views}, nil
}

func toView(r rule.Rule, builtin bool, off map[string]struct{}) RuleView {
	_, disabled := off[r.ID]
	return RuleView{Rule: r, Builtin: builtin, Enabled: !disabled}
}
