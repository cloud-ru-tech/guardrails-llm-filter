// Package rules coordinates the rule-management use cases exposed by the
// configuration API: create/update/delete a custom rule, enable/disable any
// rule, and read/list built-in and custom rules. Each scenario lives in its
// own subpackage (command pattern: contract/usecase/handle); this coordinator
// constructs them from shared dependencies and exposes each as its
// CommandHandler interface. Built-in rules come from YAML files at startup and
// are immutable via the API (except disabling); custom rules persist in the
// store. After every mutation the scenario asks the reloader
// (internal/service/rulesreload) to rebuild and atomically swap the compiled
// registry snapshot.
//
// Sentinel errors live in the errors subpackage so the controller can map them
// to HTTP statuses without importing every scenario.
package rules

import (
	"context"

	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/repository"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/usecases/rules/builtins"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/usecases/rules/create"
	deleterule "github.com/cloud-ru-tech/guardrails-llm-filter/internal/usecases/rules/delete"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/usecases/rules/get"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/usecases/rules/list"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/usecases/rules/setenabled"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/usecases/rules/update"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/usecases/rules/validation"
)

// Reloader rebuilds the registry snapshot from built-in + stored rules;
// implemented by internal/service/rulesreload.
type Reloader interface {
	Reload(ctx context.Context) error
}

// Deps are the shared dependencies of every rule scenario.
type Deps struct {
	// Store persists custom rules and the disabled set.
	Store repository.RuleStore
	// Builtins indexes the immutable built-in rules loaded at startup.
	Builtins *builtins.Index
	// Reloader rebuilds and swaps the compiled registry snapshot after
	// mutations.
	Reloader Reloader
	// MaxCustomRules bounds the number of custom rules (0 = unlimited).
	MaxCustomRules int
	// MaxPatternLen bounds a custom rule's regex length (0 = unlimited).
	MaxPatternLen int
}

// UseCase is the coordinator over the individual rule scenarios.
type UseCase struct {
	create     *create.UseCase
	update     *update.UseCase
	delete     *deleterule.UseCase
	setEnabled *setenabled.UseCase
	get        *get.UseCase
	list       *list.UseCase
}

// NewUseCase constructs every rule scenario from the shared dependencies.
func NewUseCase(d Deps) *UseCase {
	limits := validation.Limits{MaxPatternLen: d.MaxPatternLen}
	return &UseCase{
		create:     create.NewUseCase(d.Store, d.Builtins, d.Reloader, limits, d.MaxCustomRules),
		update:     update.NewUseCase(d.Store, d.Builtins, d.Reloader, limits),
		delete:     deleterule.NewUseCase(d.Store, d.Builtins, d.Reloader),
		setEnabled: setenabled.NewUseCase(d.Store, d.Builtins, d.Reloader),
		get:        get.NewUseCase(d.Store, d.Builtins),
		list:       list.NewUseCase(d.Store, d.Builtins),
	}
}

// Create returns the create-rule handler.
func (uc *UseCase) Create() create.CommandHandler { return uc.create }

// Update returns the update-rule handler.
func (uc *UseCase) Update() update.CommandHandler { return uc.update }

// Delete returns the delete-rule handler.
func (uc *UseCase) Delete() deleterule.CommandHandler { return uc.delete }

// SetEnabled returns the enable/disable-rule handler.
func (uc *UseCase) SetEnabled() setenabled.CommandHandler { return uc.setEnabled }

// Get returns the get-rule handler.
func (uc *UseCase) Get() get.CommandHandler { return uc.get }

// List returns the list-rules handler.
func (uc *UseCase) List() list.CommandHandler { return uc.list }
