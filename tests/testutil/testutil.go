// Package testutil holds helpers shared across the tests/ tree.
package testutil

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// RepoRoot returns the repository root directory, located by walking up from
// the caller's source file until a go.mod is found. Being depth-independent it
// works from any package under tests/ without a hard-coded "../.." offset that
// silently breaks when the test layout is reorganized.
func RepoRoot(tb testing.TB) string {
	tb.Helper()

	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		tb.Fatal("runtime.Caller failed")
	}

	dir := filepath.Dir(filename)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			tb.Fatal("testutil.RepoRoot: go.mod not found above " + filepath.Dir(filename))
		}
		dir = parent
	}
}

// SyntheticCorpus returns n synthetic secret-bearing prompt strings for masking
// tests and benchmarks. Each entry embeds real-looking secrets (email, IPv4,
// Stripe/npm keys, passwords) that trigger the shipped built-in rules across all
// five built-in data types, varied by index so distinct entries yield distinct
// placeholders while repeats exercise cross-text dedup. It is self-contained (no
// external data files), so masking tests stay green on any clean checkout.
func SyntheticCorpus(n int) []string {
	out := make([]string, n)
	for i := range out {
		switch i % 5 {
		case 0:
			out[i] = fmt.Sprintf("Пользователь user%d@example.com зашёл с адреса 10.0.0.%d вчера вечером.", i, i%199+1)
		case 1:
			out[i] = fmt.Sprintf("Ключи биллинга: sk_live_%024d и npm_%036d лежат в секрете.", i, i)
		case 2:
			out[i] = fmt.Sprintf("Установи password=Secret%dPass и напиши на admin%d@corp.io для доступа.", i, i)
		case 3:
			out[i] = fmt.Sprintf("Сервер 192.168.%d.%d ответил; логи шли на ops%d@example.org всё утро.", i%200, i%199+1, i)
		default:
			out[i] = fmt.Sprintf("Смени токен npm_%036d; старый password=OldPass%dValue отзови немедленно.", i, i)
		}
	}
	return out
}
