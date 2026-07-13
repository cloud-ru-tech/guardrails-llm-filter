package main

import (
	"flag"
	"fmt"
	"os"

	gitleaksgen "github.com/cloud-ru-tech/guardrails-llm-filter/pkg/gitleaksgen"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	switch os.Args[1] {
	case "import-gitleaks":
		if err := runImportGitleaks(os.Args[2:]); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "rulesgen import-gitleaks failed: %v\n", err)
			os.Exit(1)
		}
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	_, _ = fmt.Fprintln(os.Stderr, "Usage:")
	_, _ = fmt.Fprintln(os.Stderr, "  rulesgen import-gitleaks --in <configs/gitleaks.toml> --out <configs/guardrails_regex_rules.gitleaks.generated.yaml>")
}

func runImportGitleaks(args []string) error {
	fs := flag.NewFlagSet("import-gitleaks", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	in := fs.String("in", "./configs/gitleaks.toml", "path to gitleaks TOML")
	out := fs.String("out", "./configs/guardrails_regex_rules.gitleaks.generated.yaml", "path to generated guardrails rules YAML")
	if err := fs.Parse(args); err != nil {
		return err
	}

	stats, err := gitleaksgen.GenerateFMGuardrailsRegexRulesFromGitleaks(*in, *out)
	if err != nil {
		return err
	}

	_, _ = fmt.Fprintf(
		os.Stdout,
		"generated=%d total=%d skipped_excluded=%d skipped_invalid=%d out=%s\n",
		stats.GeneratedRules, stats.TotalRules, stats.SkippedExcludedID, stats.SkippedInvalidRule, *out,
	)
	return nil
}
