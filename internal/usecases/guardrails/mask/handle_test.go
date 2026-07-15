package mask_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/models"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/usecases/guardrails/mask"
	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/guardrails/regex/registry"
	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/guardrails/regex/rule"
	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/guardrails/regex/scanners/sensitive"
	"github.com/cloud-ru-tech/guardrails-llm-filter/tests/testutil"
)

// stubRules is a non-empty compiled-rule set returned by the mocked registry;
// its content is irrelevant because the scanner is mocked. The same slice value
// is handed to both ResolveForDataTypes and ScanRules so gomock matches it.
func stubRules() []registry.CompiledRule { return []registry.CompiledRule{{}} }

func TestUseCase_Handle_NoWork(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		cmd       mask.Command
		setup     func(reg *MockRegistry, scanner *MockSensitiveScanner)
		wantEmpty bool
	}{
		{
			name: "empty data types does not call dependencies",
			cmd: mask.Command{
				Texts: []string{"token=secret"},
			},
			setup:     func(*MockRegistry, *MockSensitiveScanner) {},
			wantEmpty: true,
		},
		{
			name: "registry returns no rule ids and scanner is not called",
			cmd: mask.Command{
				DataTypes: []models.DataType{models.DataTypeCREDENTIALS},
				Texts:     []string{"token=secret"},
			},
			setup: func(reg *MockRegistry, _ *MockSensitiveScanner) {
				reg.EXPECT().ResolveForDataTypes([]uint32{uint32(models.DataTypeCREDENTIALS)}).Return(nil, nil)
			},
			wantEmpty: true,
		},
		{
			name: "no texts returns empty response after resolving rules",
			cmd: mask.Command{
				DataTypes: []models.DataType{models.DataTypeCREDENTIALS},
			},
			setup: func(reg *MockRegistry, _ *MockSensitiveScanner) {
				reg.EXPECT().
					ResolveForDataTypes([]uint32{uint32(models.DataTypeCREDENTIALS)}).
					Return([]string{"credentials.rule"}, stubRules())
			},
			wantEmpty: true,
		},
		{
			name: "no scanner matches returns empty response",
			cmd: mask.Command{
				DataTypes: []models.DataType{models.DataTypeCREDENTIALS},
				Texts:     []string{"nothing sensitive", "still clean"},
			},
			setup: func(reg *MockRegistry, scanner *MockSensitiveScanner) {
				rules := stubRules()
				reg.EXPECT().
					ResolveForDataTypes([]uint32{uint32(models.DataTypeCREDENTIALS)}).
					Return([]string{"credentials.rule"}, rules)
				scanner.EXPECT().ScanRules("nothing sensitive", rules).Return(nil, nil)
				scanner.EXPECT().ScanRules("still clean", rules).Return(nil, nil)
			},
			wantEmpty: true,
		},
		{
			name: "only skippable matches returns empty response",
			cmd: mask.Command{
				DataTypes: []models.DataType{models.DataTypeCREDENTIALS},
				Texts:     []string{"token=secret"},
			},
			setup: func(reg *MockRegistry, scanner *MockSensitiveScanner) {
				rules := stubRules()
				reg.EXPECT().
					ResolveForDataTypes([]uint32{uint32(models.DataTypeCREDENTIALS)}).
					Return([]string{"credentials.rule"}, rules)
				scanner.EXPECT().ScanRules("token=secret", rules).Return([]sensitive.Match{
					{
						RuleID:      "credentials.rule",
						DataType:    int(models.DataTypeCREDENTIALS),
						Start:       len("token="),
						End:         len("token=secret"),
						FullText:    "",
						Placeholder: "SECRET",
					},
					{
						RuleID:      "credentials.rule",
						DataType:    int(models.DataTypeCREDENTIALS),
						Start:       len("token="),
						End:         len("token=secret"),
						FullText:    "secret",
						Placeholder: "",
					},
				}, nil)
			},
			wantEmpty: true,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctrl := gomock.NewController(t)
			reg := NewMockRegistry(ctrl)
			scanner := NewMockSensitiveScanner(ctrl)
			tt.setup(reg, scanner)
			uc := mask.New(mask.Deps{Registry: reg, Scanner: scanner})

			got, err := uc.Handle(context.Background(), tt.cmd)

			require.NoError(t, err)
			if tt.wantEmpty {
				assert.Empty(t, got.MaskedTexts)
				assert.True(t, got.MaskingState.IsEmpty())
			}
		})
	}
}

func TestUseCase_Handle_MasksSingleText(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	reg := NewMockRegistry(ctrl)
	scanner := NewMockSensitiveScanner(ctrl)
	rules := stubRules()
	text := `{"token":"secret"}`

	reg.EXPECT().
		ResolveForDataTypes([]uint32{uint32(models.DataTypeCREDENTIALS)}).
		Return([]string{"credentials.token"}, rules)
	scanner.EXPECT().ScanRules(text, rules).Return([]sensitive.Match{
		{
			RuleID:      "credentials.token",
			DataType:    int(models.DataTypeCREDENTIALS),
			Start:       len(`{"token":"`),
			End:         len(`{"token":"secret`),
			FullText:    "secret",
			Placeholder: "SECRET",
		},
	}, nil)

	uc := mask.New(mask.Deps{Registry: reg, Scanner: scanner})
	got, err := uc.Handle(context.Background(), mask.Command{
		DataTypes: []models.DataType{models.DataTypeCREDENTIALS},
		Texts:     []string{text},
	})

	require.NoError(t, err)
	assert.Equal(t, []string{`{"token":"<SECRET_1>"}`}, got.MaskedTexts)
	assert.Equal(t, []string{"credentials.token"}, got.MaskingState.TriggeredRuleIDs)
	assert.Equal(t, []models.DataType{models.DataTypeCREDENTIALS}, got.MaskingState.TriggeredDataTypes)
	assert.Equal(t, []models.Replacement{
		{
			RuleID:      "credentials.token",
			Original:    "secret",
			Placeholder: "<SECRET_1>",
		},
	}, got.MaskingState.Replacements)
}

func TestUseCase_Handle_MasksMultipleTextsWithSharedState(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	reg := NewMockRegistry(ctrl)
	scanner := NewMockSensitiveScanner(ctrl)
	rules := stubRules()

	reg.EXPECT().
		ResolveForDataTypes([]uint32{uint32(models.DataTypeCREDENTIALS), uint32(models.DataTypeAPIKEYS)}).
		Return([]string{"credentials.token", "api.key"}, rules)
	scanner.EXPECT().ScanRules("first secret", rules).Return([]sensitive.Match{
		match("credentials.token", models.DataTypeCREDENTIALS, "first secret", "secret", "SECRET"),
	}, nil)
	scanner.EXPECT().ScanRules("second secret and api", rules).Return([]sensitive.Match{
		match("credentials.token", models.DataTypeCREDENTIALS, "second secret and api", "secret", "SECRET"),
		match("api.key", models.DataTypeAPIKEYS, "second secret and api", "api", "API_KEY"),
	}, nil)
	scanner.EXPECT().ScanRules("clean", rules).Return(nil, nil)

	uc := mask.New(mask.Deps{Registry: reg, Scanner: scanner})
	got, err := uc.Handle(context.Background(), mask.Command{
		DataTypes: []models.DataType{models.DataTypeCREDENTIALS, models.DataTypeAPIKEYS},
		Texts:     []string{"first secret", "second secret and api", "clean"},
	})

	require.NoError(t, err)
	assert.Equal(t, []string{"first <SECRET_1>", "second <SECRET_1> and <API_KEY_1>", "clean"}, got.MaskedTexts)
	assert.ElementsMatch(t, []string{"credentials.token", "api.key"}, got.MaskingState.TriggeredRuleIDs)
	assert.Equal(t, []models.DataType{models.DataTypeCREDENTIALS, models.DataTypeAPIKEYS}, got.MaskingState.TriggeredDataTypes)
	assert.Equal(t, []models.Replacement{
		{
			RuleID:      "credentials.token",
			Original:    "secret",
			Placeholder: "<SECRET_1>",
		},
		{
			RuleID:      "api.key",
			Original:    "api",
			Placeholder: "<API_KEY_1>",
		},
	}, got.MaskingState.Replacements)
}

func TestUseCase_Handle_ScannerError(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	reg := NewMockRegistry(ctrl)
	scanner := NewMockSensitiveScanner(ctrl)
	rules := stubRules()
	scanErr := errors.New("scanner failed")

	reg.EXPECT().
		ResolveForDataTypes([]uint32{uint32(models.DataTypeCREDENTIALS)}).
		Return([]string{"credentials.rule"}, rules)
	// Sequential path (tiny bodies, auto mode) stops at the first error: "ok" and
	// "bad" are scanned, "not scanned" is never reached. The use case surfaces the
	// first failure by index and returns nothing.
	scanner.EXPECT().ScanRules("ok", rules).Return(nil, nil)
	scanner.EXPECT().ScanRules("bad", rules).Return(nil, scanErr)

	uc := mask.New(mask.Deps{Registry: reg, Scanner: scanner})
	got, err := uc.Handle(context.Background(), mask.Command{
		DataTypes: []models.DataType{models.DataTypeCREDENTIALS},
		Texts:     []string{"ok", "bad", "not scanned"},
	})

	require.Error(t, err)
	assert.ErrorIs(t, err, scanErr)
	assert.Contains(t, err.Error(), "scan text[1]")
	assert.Empty(t, got.MaskedTexts)
	assert.True(t, got.MaskingState.IsEmpty())
}

func match(ruleID string, dataType models.DataType, text, fullText, placeholder string) sensitive.Match {
	start := indexOrPanic(text, fullText)
	return sensitive.Match{
		RuleID:      ruleID,
		DataType:    int(dataType),
		Start:       start,
		End:         start + len(fullText),
		FullText:    fullText,
		Placeholder: placeholder,
	}
}

func indexOrPanic(s, substr string) int {
	if idx := strings.Index(s, substr); idx >= 0 {
		return idx
	}
	panic("substring not found")
}

// TestUseCase_Handle_ParallelScannerError forces the concurrent scan path and
// checks that, when several texts fail, the use case surfaces the lowest-index
// failure (deterministic despite concurrent completion order) and returns an
// empty response — the fail-open contract holds under parallelism.
func TestUseCase_Handle_ParallelScannerError(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	reg := NewMockRegistry(ctrl)
	scanner := NewMockSensitiveScanner(ctrl)
	rules := stubRules()
	errAt1 := errors.New("boom at 1")
	errAt3 := errors.New("boom at 3")

	reg.EXPECT().
		ResolveForDataTypes([]uint32{uint32(models.DataTypeCREDENTIALS)}).
		Return([]string{"credentials.rule"}, rules)
	// Every text is scanned concurrently; two of them fail.
	scanner.EXPECT().ScanRules("t0", rules).Return(nil, nil)
	scanner.EXPECT().ScanRules("t1", rules).Return(nil, errAt1)
	scanner.EXPECT().ScanRules("t2", rules).Return(nil, nil)
	scanner.EXPECT().ScanRules("t3", rules).Return(nil, errAt3)

	uc := mask.New(
		mask.Deps{Registry: reg, Scanner: scanner},
		mask.WithScanConcurrency(4), // force the parallel path
	)
	got, err := uc.Handle(context.Background(), mask.Command{
		DataTypes: []models.DataType{models.DataTypeCREDENTIALS},
		Texts:     []string{"t0", "t1", "t2", "t3"},
	})

	require.Error(t, err)
	assert.ErrorIs(t, err, errAt1, "lowest-index error must win")
	assert.NotErrorIs(t, err, errAt3)
	assert.Contains(t, err.Error(), "scan text[1]")
	assert.Empty(t, got.MaskedTexts)
	assert.True(t, got.MaskingState.IsEmpty())
}

// builtinDataTypes are the five built-in data types every built-in rule maps to.
var builtinDataTypes = []models.DataType{
	models.DataTypeCREDENTIALS,
	models.DataTypeAPIKEYS,
	models.DataTypeACCESSTOKENS,
	models.DataTypeIPADDRESSES,
	models.DataTypePERSONALDATA,
}

// loadRealRegistry builds a registry from the shipped rule files, so the
// equivalence test exercises the exact scanner the service runs.
func loadRealRegistry(tb testing.TB) *registry.Registry {
	tb.Helper()
	root := testutil.RepoRoot(tb)
	_, rules, err := rule.LoadAllFromFiles(
		filepath.Join(root, "configs/guardrails_regex_rules.gitleaks.generated.yaml"),
		filepath.Join(root, "configs/guardrails_regex_rules.yaml"),
	)
	require.NoError(tb, err)
	reg := registry.NewRegistry()
	reg.Register(rules...)
	return reg
}

// TestUseCase_Handle_AvoidsPlaceholderCollision guards the demask round-trip
// against a user text that already contains a literal <TYPE_N> matching the
// placeholder the masker would generate. The masker must skip that counter value
// so the literal survives unchanged (demask replaces only genuinely-generated
// placeholders).
func TestUseCase_Handle_AvoidsPlaceholderCollision(t *testing.T) {
	t.Parallel()

	reg := loadRealRegistry(t)
	uc := mask.New(mask.Deps{Registry: reg, Scanner: sensitive.New(reg)})

	resp, err := uc.Handle(context.Background(), mask.Command{
		DataTypes: []models.DataType{models.DataTypePERSONALDATA},
		Texts:     []string{"есть <FIO_1> и звонил Иванов Иван Петрович"},
	})
	require.NoError(t, err)
	require.Len(t, resp.MaskedTexts, 1)

	// The name must NOT reuse <FIO_1> (the literal already in the text); it takes
	// the next free counter value, leaving the literal intact.
	assert.Equal(t, "есть <FIO_1> и звонил <FIO_2>", resp.MaskedTexts[0])

	var namePlaceholder string
	for _, r := range resp.MaskingState.Replacements {
		if r.Original == "Иванов Иван Петрович" {
			namePlaceholder = r.Placeholder
		}
	}
	assert.Equal(t, "<FIO_2>", namePlaceholder,
		"placeholder must skip the literal <FIO_1> present in the input")
}

// TestUseCase_Handle_ParallelEqualsSequential verifies that the parallel scan
// path produces byte-for-byte identical results to the sequential path, across
// bodies below and above the size threshold. Combined with `go test -race`, this
// also proves the parallel scan is race-free. This is the core correctness
// guarantee: parallelizing the scan must not perturb placeholder numbering,
// cross-text dedup, or triggered rule/type aggregation (all owned by the
// sequential masking phase).
func TestUseCase_Handle_ParallelEqualsSequential(t *testing.T) {
	t.Parallel()

	reg := loadRealRegistry(t)
	// A self-contained synthetic corpus (no external data files) sized well past
	// the default 8 KiB auto-parallel gate, so "many_fields" exercises the auto
	// fan-out decision as well as the forced-parallel path.
	contents := testutil.SyntheticCorpus(200)

	bodies := map[string][]string{
		// Below threshold: two short fields — auto stays sequential, but the
		// forced-parallel run must still match.
		"tiny_two_fields": contents[:2],
		// Above threshold: many fields, the realistic multi-field body.
		"many_fields": contents,
		// A field repeated many times: stresses cross-text dedup (identical
		// originals must collapse to the same placeholder regardless of path).
		"repeated_field": repeat(contents[0], 64),
	}

	cmd := func(texts []string) mask.Command {
		return mask.Command{DataTypes: builtinDataTypes, Texts: texts}
	}

	newUC := func(workers int) *mask.UseCase {
		return mask.New(
			mask.Deps{Registry: reg, Scanner: sensitive.New(reg)},
			mask.WithScanConcurrency(workers),
		)
	}

	seq := newUC(1)   // forced sequential
	par := newUC(8)   // forced parallel, bypasses the size threshold
	auto := mask.New( // auto: threshold + GOMAXPROCS
		mask.Deps{Registry: reg, Scanner: sensitive.New(reg)},
	)

	for name, texts := range bodies {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			want, err := seq.Handle(context.Background(), cmd(texts))
			require.NoError(t, err)
			// Guard against a corpus that silently stops triggering rules, which
			// would make the equivalence assertions pass trivially on empty output.
			require.NotEmpty(t, want.MaskingState.Replacements, "corpus produced no masking")

			gotPar, err := par.Handle(context.Background(), cmd(texts))
			require.NoError(t, err)
			assertResponsesEqual(t, want, gotPar)

			gotAuto, err := auto.Handle(context.Background(), cmd(texts))
			require.NoError(t, err)
			assertResponsesEqual(t, want, gotAuto)
		})
	}
}

func assertResponsesEqual(t *testing.T, want, got mask.CommandResponse) {
	t.Helper()
	assert.Equal(t, want.MaskedTexts, got.MaskedTexts, "masked texts diverged")
	assert.Equal(t, want.MaskingState.TriggeredRuleIDs, got.MaskingState.TriggeredRuleIDs, "triggered rule IDs diverged")
	assert.Equal(t, want.MaskingState.TriggeredDataTypes, got.MaskingState.TriggeredDataTypes, "triggered data types diverged")
	assert.Equal(t, want.MaskingState.Replacements, got.MaskingState.Replacements, "replacements diverged")
}

func repeat(text string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = text
	}
	return out
}

// TestUseCase_Handle_ScanPanicFailsOpen verifies that a panic in the parallel
// scan path is recovered and turned into an error (Handle fails open) rather
// than crashing the replica. WithScanConcurrency forces the goroutine path
// where the panic happens off the handler goroutine.
func TestUseCase_Handle_ScanPanicFailsOpen(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	reg := NewMockRegistry(ctrl)
	scanner := NewMockSensitiveScanner(ctrl)

	rules := stubRules()
	reg.EXPECT().
		ResolveForDataTypes([]uint32{uint32(models.DataTypeCREDENTIALS)}).
		Return([]string{"credentials.rule"}, rules)
	scanner.EXPECT().
		ScanRules(gomock.Any(), rules).
		DoAndReturn(func(string, []registry.CompiledRule) ([]sensitive.Match, error) {
			panic("scan boom")
		}).
		AnyTimes()

	uc := mask.New(mask.Deps{Registry: reg, Scanner: scanner}, mask.WithScanConcurrency(2))

	require.NotPanics(t, func() {
		_, err := uc.Handle(context.Background(), mask.Command{
			DataTypes: []models.DataType{models.DataTypeCREDENTIALS},
			Texts:     []string{"first text", "second text"},
		})
		assert.Error(t, err, "a scan panic must fail open as an error, not crash")
	})
}
