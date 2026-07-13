package mask

// Deps are dependencies for the mask use case.
type Deps struct {
	// Registry resolves rule IDs and masking policies.
	Registry Registry
	// Scanner finds sensitive values in text.
	Scanner SensitiveScanner
}

// UseCase masks request texts using guardrails regex rules.
type UseCase struct {
	registry Registry
	scanner  SensitiveScanner

	// maxScanWorkers overrides the text-level scan concurrency. 0 means auto
	// (parallelize by GOMAXPROCS only when the combined text size clears the
	// threshold); 1 forces the sequential path; >1 forces that many workers
	// regardless of the size threshold. Used for tuning and benchmarks.
	maxScanWorkers int

	// parallelMinBytes is the combined-text-size gate (bytes) for auto text-level
	// parallel scanning. 0 falls back to defaultParallelMinBytes.
	parallelMinBytes int
}

// Option configures a UseCase.
type Option func(*UseCase)

// WithScanConcurrency overrides the text-level scan concurrency. It is meant for
// tuning and benchmarking; production wiring should leave it unset (auto).
func WithScanConcurrency(n int) Option {
	return func(uc *UseCase) { uc.maxScanWorkers = n }
}

// WithParallelMinBytes sets the combined-text-size gate (bytes) at or above which
// auto mode fans the scan out across text fields. n <= 0 keeps the built-in
// default. Wired from GUARDRAILS_MASK_PARALLEL_MIN_BYTES.
func WithParallelMinBytes(n int) Option {
	return func(uc *UseCase) { uc.parallelMinBytes = n }
}

// New creates a new UseCase with explicit runtime dependencies.
func New(d Deps, opts ...Option) *UseCase {
	uc := &UseCase{
		registry: d.Registry,
		scanner:  d.Scanner,
	}
	for _, opt := range opts {
		opt(uc)
	}
	return uc
}
