package mask

import (
	"runtime"
	"strings"
	"testing"
)

// TestScanWorkers pins the text-level scan-concurrency decision deterministically
// (no scanner, no goroutines), covering the production GUARDRAILS_MASK_PARALLEL_
// MIN_BYTES knob (WithParallelMinBytes), the explicit override (WithScanConcurrency),
// the single-text short-circuit and the size-threshold boundary. The auto-parallel
// cases assert the exact min(GOMAXPROCS, len) formula, so they hold on any core
// count; the WithScanConcurrency cases exercise a >1 worker count independently of
// GOMAXPROCS.
func TestScanWorkers(t *testing.T) {
	t.Parallel()

	gomaxprocs := runtime.GOMAXPROCS(0)

	// text builds a string of exactly n bytes (ASCII), for precise threshold math.
	text := func(n int) string { return strings.Repeat("x", n) }

	tests := []struct {
		name string
		opts []Option
		// texts is described by per-field byte sizes.
		sizes []int
		want  int
	}{
		{
			name:  "no texts scans inline",
			sizes: nil,
			want:  1,
		},
		{
			name:  "single text scans inline regardless of concurrency",
			opts:  []Option{WithScanConcurrency(8)},
			sizes: []int{100000},
			want:  1,
		},
		{
			name:  "explicit concurrency capped by text count",
			opts:  []Option{WithScanConcurrency(8)},
			sizes: []int{1, 1, 1},
			want:  3,
		},
		{
			name:  "explicit concurrency below text count is honored, bypassing size gate",
			opts:  []Option{WithScanConcurrency(2)},
			sizes: []int{1, 1, 1, 1},
			want:  2,
		},
		{
			name:  "auto stays sequential below the default gate",
			sizes: []int{100, 100},
			want:  1,
		},
		{
			name:  "auto goes parallel at or above the default gate",
			sizes: []int{defaultParallelMinBytes, 1},
			want:  min(gomaxprocs, 2),
		},
		{
			name:  "configured gate: below stays sequential",
			opts:  []Option{WithParallelMinBytes(10)},
			sizes: []int{4, 5}, // total 9 < 10
			want:  1,
		},
		{
			name:  "configured gate: exactly at threshold goes parallel",
			opts:  []Option{WithParallelMinBytes(10)},
			sizes: []int{5, 5}, // total 10 == 10
			want:  min(gomaxprocs, 2),
		},
		{
			name:  "non-positive configured gate falls back to the default gate (below -> sequential)",
			opts:  []Option{WithParallelMinBytes(0)},
			sizes: []int{100, 100},
			want:  1,
		},
		{
			name:  "non-positive configured gate falls back to the default gate (above -> parallel)",
			opts:  []Option{WithParallelMinBytes(-5)},
			sizes: []int{defaultParallelMinBytes, 1},
			want:  min(gomaxprocs, 2),
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			texts := make([]string, len(tt.sizes))
			for i, n := range tt.sizes {
				texts[i] = text(n)
			}

			uc := New(Deps{}, tt.opts...)
			if got := uc.scanWorkers(texts); got != tt.want {
				t.Fatalf("scanWorkers(%d texts) = %d, want %d (GOMAXPROCS=%d)", len(texts), got, tt.want, gomaxprocs)
			}
		})
	}
}
