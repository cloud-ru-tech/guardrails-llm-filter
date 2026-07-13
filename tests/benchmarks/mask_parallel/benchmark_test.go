// Package maskparallel benchmarks the mask use case's text-level scan
// concurrency on the real shipped ruleset. A request body carries several text
// fields (chat messages, tool calls, system prompt); the scan of each field is
// independent, so the use case can fan out across fields. These benchmarks
// compare the sequential path (WithScanConcurrency(1)) against the parallel path
// (WithScanConcurrency(GOMAXPROCS)) across body shapes, to quantify the speedup
// and confirm it does not regress the few-large-fields shape (where the existing
// per-rule parallelism inside a single scan already saturates the CPU).
//
// Run:
//
//	go test -bench . -benchmem -count=6 ./tests/benchmarks/mask_parallel | tee out.txt
//	benchstat -col /workers out.txt   # seq vs par per fixture
package maskparallel

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/models"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/usecases/guardrails/mask"
	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/guardrails/regex/registry"
	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/guardrails/regex/rule"
	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/guardrails/regex/scanners/sensitive"
	"github.com/cloud-ru-tech/guardrails-llm-filter/tests/testutil"
)

// builtinDataTypes covers the five built-in data types (all built-in rules).
var builtinDataTypes = []models.DataType{
	models.DataTypeCREDENTIALS,
	models.DataTypeAPIKEYS,
	models.DataTypeACCESSTOKENS,
	models.DataTypeIPADDRESSES,
	models.DataTypePERSONALDATA,
}

func loadRegistry(tb testing.TB) *registry.Registry {
	tb.Helper()
	root := testutil.RepoRoot(tb)
	_, rules, err := rule.LoadAllFromFiles(
		filepath.Join(root, "configs/guardrails_regex_rules.gitleaks.generated.yaml"),
		filepath.Join(root, "configs/guardrails_regex_rules.yaml"),
	)
	if err != nil {
		tb.Fatalf("LoadAllFromFiles: %v", err)
	}
	reg := registry.NewRegistry()
	reg.Register(rules...)
	return reg
}

// manySmall returns n fields, each one realistic dataset prompt (~100-250 B),
// cycling through the corpus. This is the shape parallelism should win on: many
// short scans, each of which under-utilizes the per-rule fan-out.
func manySmall(corpus []string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = corpus[i%len(corpus)]
	}
	return out
}

// fewLarge returns n fields, each grown to at least minBytes by concatenating
// dataset prompts. This is the shape where the per-rule parallelism inside a
// single scan already saturates the CPU, so text-level parallelism should add
// little — the benchmark confirms it does not regress.
func fewLarge(corpus []string, n, minBytes int) []string {
	out := make([]string, n)
	for i := range out {
		var b strings.Builder
		j := i
		for b.Len() < minBytes {
			b.WriteString(corpus[j%len(corpus)])
			b.WriteByte('\n')
			j++
		}
		out[i] = b.String()
	}
	return out
}

func totalBytes(texts []string) int {
	total := 0
	for _, t := range texts {
		total += len(t)
	}
	return total
}

func BenchmarkMask(b *testing.B) {
	reg := loadRegistry(b)
	// Self-contained synthetic corpus (no external data files) of realistic
	// secret-bearing prompts, used as the building blocks for the body shapes.
	corpus := testutil.SyntheticCorpus(200)

	// Stable order so benchstat rows line up run-to-run.
	fixtures := []struct {
		name  string
		texts []string
	}{
		{"many_small_120", manySmall(corpus, 120)},
		{"few_large_3x50k", fewLarge(corpus, 3, 50*1024)},
		{"mixed_40small_2x50k", append(manySmall(corpus, 40), fewLarge(corpus, 2, 50*1024)...)},
	}

	workerModes := []struct {
		name    string
		workers int
	}{
		{"seq", 1},
		{"par", runtime.GOMAXPROCS(0)},
	}

	for _, fx := range fixtures {
		bytes := int64(totalBytes(fx.texts))
		for _, wm := range workerModes {
			uc := mask.New(
				mask.Deps{Registry: reg, Scanner: sensitive.New(reg)},
				mask.WithScanConcurrency(wm.workers),
			)
			cmd := mask.Command{DataTypes: builtinDataTypes, Texts: fx.texts}

			b.Run(fmt.Sprintf("fixture=%s/workers=%s", fx.name, wm.name), func(b *testing.B) {
				b.ReportAllocs()
				b.SetBytes(bytes)
				for b.Loop() {
					if _, err := uc.Handle(b.Context(), cmd); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}
