// Package repositorytest provides a conformance suite that every repository.Store
// implementation must pass. Backend test packages call Run with a factory
// for a fresh, isolated repository.
package repositorytest

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/models"
	"github.com/cloud-ru-tech/guardrails-llm-filter/internal/repository"
	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/guardrails/regex/rule"
)

// Options tunes backend-specific aspects of the suite.
type Options struct {
	// ExpireMaskingState forces entries written with PutMaskingState to be
	// past their TTL (e.g. miniredis FastForward, or constructing the store
	// with a tiny TTL and sleeping). Nil skips the TTL test.
	ExpireMaskingState func(t *testing.T)
	// ExpireAudit forces entries written with PutAuditRecord past the audit
	// TTL. Nil skips the audit TTL test.
	ExpireAudit func(t *testing.T)
}

func sampleState() models.MaskingState {
	return models.MaskingState{
		TriggeredRuleIDs:   []string{"email", "phone_ru"},
		TriggeredDataTypes: []models.DataType{models.DataTypePERSONALDATA, models.DataTypeCUSTOM},
		Replacements: []models.Replacement{
			{RuleID: "email", Original: "user@example.com", Placeholder: "<EMAIL_1>"},
			{RuleID: "phone_ru", Original: "+7 999 123-45-67", Placeholder: "<PHONE_2>"},
		},
	}
}

// sampleAuditRecord builds a record with a microsecond-truncated UTC
// timestamp — the precision every backend guarantees (postgres TIMESTAMPTZ,
// redis ZSET score). Callers pass recent timestamps: the redis index
// self-trims entries older than the audit TTL on every write.
func sampleAuditRecord(requestID string, ts time.Time) models.AuditRecord {
	return models.AuditRecord{
		RequestID:          requestID,
		Timestamp:          ts.UTC().Truncate(time.Microsecond),
		Model:              "gpt-test",
		Path:               "/v1/chat/completions",
		TriggeredRuleIDs:   []string{"pii.email"},
		TriggeredDataTypes: []models.DataType{models.DataTypePERSONALDATA},
		Replacements: []models.AuditReplacement{
			{RuleID: "pii.email", DataType: models.DataTypePERSONALDATA, Placeholder: "<EMAIL_1>"},
		},
	}
}

func sampleRule(id string) rule.Rule {
	return rule.Rule{
		ID:         id,
		Name:       "Sample " + id,
		Group:      "custom_group",
		DataType:   int(models.DataTypeCUSTOM),
		Regex:      `\bsample-[0-9a-f]{8}\b`,
		Keywords:   []string{"sample-"},
		Validators: []rule.ValidatorType{rule.ValidatorEntropy},
		MinLength:  8,
		Entropy:    3.1,
		Banlist:    []string{"sample-00000000"},
		DefaultOn:  true,
		Masking: rule.MaskingConfig{
			CaptureGroups: []int{1},
			Placeholder:   "SAMPLE",
		},
	}
}

// Run executes the conformance suite against stores produced by newStore.
func Run(t *testing.T, newStore func(t *testing.T) repository.Store, opts Options) {
	t.Helper()
	ctx := context.Background()

	t.Run("masking state round trip", func(t *testing.T) {
		s := newStore(t)
		st := sampleState()

		require.NoError(t, s.PutMaskingState(ctx, "req-1", st))
		got, err := s.GetMaskingState(ctx, "req-1")
		require.NoError(t, err)
		assert.Equal(t, st, got)
	})

	t.Run("masking state not found", func(t *testing.T) {
		s := newStore(t)
		_, err := s.GetMaskingState(ctx, "missing")
		require.ErrorIs(t, err, repository.ErrNotFound)
	})

	t.Run("masking state overwrite", func(t *testing.T) {
		s := newStore(t)
		st := sampleState()
		require.NoError(t, s.PutMaskingState(ctx, "req-1", st))

		st2 := st
		st2.TriggeredRuleIDs = []string{"email"}
		require.NoError(t, s.PutMaskingState(ctx, "req-1", st2))

		got, err := s.GetMaskingState(ctx, "req-1")
		require.NoError(t, err)
		assert.Equal(t, st2, got)
	})

	t.Run("masking state delete", func(t *testing.T) {
		s := newStore(t)
		require.NoError(t, s.PutMaskingState(ctx, "req-1", sampleState()))
		require.NoError(t, s.DeleteMaskingState(ctx, "req-1"))
		_, err := s.GetMaskingState(ctx, "req-1")
		require.ErrorIs(t, err, repository.ErrNotFound)

		// Deleting a missing entry is not an error (best-effort cleanup).
		require.NoError(t, s.DeleteMaskingState(ctx, "req-1"))
	})

	if opts.ExpireMaskingState != nil {
		t.Run("masking state TTL expiry", func(t *testing.T) {
			s := newStore(t)
			require.NoError(t, s.PutMaskingState(ctx, "req-ttl", sampleState()))
			opts.ExpireMaskingState(t)
			_, err := s.GetMaskingState(ctx, "req-ttl")
			require.ErrorIs(t, err, repository.ErrNotFound)
		})
	}

	t.Run("rule round trip preserves all fields", func(t *testing.T) {
		s := newStore(t)
		r := sampleRule("custom_rule_1")

		require.NoError(t, s.SaveRule(ctx, r))
		got, err := s.GetRule(ctx, "custom_rule_1")
		require.NoError(t, err)
		assert.Equal(t, r, got)
	})

	t.Run("rule not found", func(t *testing.T) {
		s := newStore(t)
		_, err := s.GetRule(ctx, "missing")
		require.ErrorIs(t, err, repository.ErrNotFound)
	})

	t.Run("rule upsert", func(t *testing.T) {
		s := newStore(t)
		r := sampleRule("custom_rule_1")
		require.NoError(t, s.SaveRule(ctx, r))

		r.Regex = `\bupdated-[0-9]{4}\b`
		require.NoError(t, s.SaveRule(ctx, r))

		got, err := s.GetRule(ctx, "custom_rule_1")
		require.NoError(t, err)
		assert.Equal(t, r.Regex, got.Regex)

		rules, err := s.ListRules(ctx)
		require.NoError(t, err)
		assert.Len(t, rules, 1)
	})

	t.Run("create rule is create-if-absent", func(t *testing.T) {
		s := newStore(t)
		r := sampleRule("custom_rule_1")
		require.NoError(t, s.CreateRule(ctx, r))

		dup := r
		dup.Regex = `\bshould-not-win-[0-9]{4}\b`
		err := s.CreateRule(ctx, dup)
		require.ErrorIs(t, err, repository.ErrAlreadyExists)

		got, err := s.GetRule(ctx, "custom_rule_1")
		require.NoError(t, err)
		assert.Equal(t, r.Regex, got.Regex, "the first creator's rule must survive")
	})

	t.Run("create rule is atomic under concurrency", func(t *testing.T) {
		s := newStore(t)
		const creators = 8
		var wins int64
		var wg sync.WaitGroup
		for i := range creators {
			wg.Add(1)
			go func(n int) {
				defer wg.Done()
				r := sampleRule("race_rule")
				r.Regex = fmt.Sprintf(`\bvariant-%d-[0-9]{4}\b`, n)
				if err := s.CreateRule(ctx, r); err == nil {
					atomic.AddInt64(&wins, 1)
				} else {
					assert.ErrorIs(t, err, repository.ErrAlreadyExists)
				}
			}(i)
		}
		wg.Wait()
		assert.Equal(t, int64(1), atomic.LoadInt64(&wins), "exactly one creator may win")

		rules, err := s.ListRules(ctx)
		require.NoError(t, err)
		assert.Len(t, rules, 1)
	})

	t.Run("rule list is order independent", func(t *testing.T) {
		s := newStore(t)
		a, b := sampleRule("rule_a"), sampleRule("rule_b")
		require.NoError(t, s.SaveRule(ctx, a))
		require.NoError(t, s.SaveRule(ctx, b))

		rules, err := s.ListRules(ctx)
		require.NoError(t, err)
		assert.ElementsMatch(t, []rule.Rule{a, b}, rules)
	})

	t.Run("rule delete", func(t *testing.T) {
		s := newStore(t)
		require.NoError(t, s.SaveRule(ctx, sampleRule("rule_a")))
		require.NoError(t, s.DeleteRule(ctx, "rule_a"))
		_, err := s.GetRule(ctx, "rule_a")
		require.ErrorIs(t, err, repository.ErrNotFound)

		require.ErrorIs(t, s.DeleteRule(ctx, "rule_a"), repository.ErrNotFound)
	})

	t.Run("disabled rule ids empty by default", func(t *testing.T) {
		s := newStore(t)
		ids, err := s.ListDisabledRuleIDs(ctx)
		require.NoError(t, err)
		assert.Empty(t, ids)
	})

	t.Run("set rule disabled round trip", func(t *testing.T) {
		s := newStore(t)
		require.NoError(t, s.SetRuleDisabled(ctx, "credentials.url_with_creds", true))
		require.NoError(t, s.SetRuleDisabled(ctx, "custom_rule_1", true))

		ids, err := s.ListDisabledRuleIDs(ctx)
		require.NoError(t, err)
		assert.ElementsMatch(t, []string{"credentials.url_with_creds", "custom_rule_1"}, ids)

		require.NoError(t, s.SetRuleDisabled(ctx, "custom_rule_1", false))
		ids, err = s.ListDisabledRuleIDs(ctx)
		require.NoError(t, err)
		assert.ElementsMatch(t, []string{"credentials.url_with_creds"}, ids)
	})

	t.Run("set rule disabled idempotent", func(t *testing.T) {
		s := newStore(t)
		require.NoError(t, s.SetRuleDisabled(ctx, "rule_x", true))
		require.NoError(t, s.SetRuleDisabled(ctx, "rule_x", true))

		ids, err := s.ListDisabledRuleIDs(ctx)
		require.NoError(t, err)
		assert.ElementsMatch(t, []string{"rule_x"}, ids)

		// Re-enabling, twice, is not an error either.
		require.NoError(t, s.SetRuleDisabled(ctx, "rule_x", false))
		require.NoError(t, s.SetRuleDisabled(ctx, "rule_x", false))
		ids, err = s.ListDisabledRuleIDs(ctx)
		require.NoError(t, err)
		assert.Empty(t, ids)
	})

	t.Run("disabled set independent of rule records", func(t *testing.T) {
		// Builtin rules never live in the store; disabling an ID with no
		// stored rule record must work.
		s := newStore(t)
		require.NoError(t, s.SetRuleDisabled(ctx, "api_keys.stripe-key.gl", true))
		_, err := s.GetRule(ctx, "api_keys.stripe-key.gl")
		require.ErrorIs(t, err, repository.ErrNotFound)

		ids, err := s.ListDisabledRuleIDs(ctx)
		require.NoError(t, err)
		assert.ElementsMatch(t, []string{"api_keys.stripe-key.gl"}, ids)
	})

	t.Run("settings never set returns nil", func(t *testing.T) {
		s := newStore(t)
		got, err := s.GetSettings(ctx)
		require.NoError(t, err)
		assert.Nil(t, got)
	})

	t.Run("settings round trip", func(t *testing.T) {
		s := newStore(t)
		gs := models.GuardrailsSettings{
			Enabled: true,
			DataTypes: []models.DataType{
				models.DataTypeCREDENTIALS,
				models.DataTypePERSONALDATA,
			},
			Mode: models.ModeDetect,
		}
		require.NoError(t, s.SaveSettings(ctx, gs))

		got, err := s.GetSettings(ctx)
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, gs, *got)

		gs.Enabled = false
		require.NoError(t, s.SaveSettings(ctx, gs))
		got, err = s.GetSettings(ctx)
		require.NoError(t, err)
		assert.False(t, got.Enabled)
	})

	t.Run("save settings if absent seeds once", func(t *testing.T) {
		s := newStore(t)
		first := models.GuardrailsSettings{Enabled: true, Mode: models.ModeEnforce}
		second := models.GuardrailsSettings{Enabled: false, Mode: models.ModeDetect}

		written, err := s.SaveSettingsIfAbsent(ctx, first)
		require.NoError(t, err)
		assert.True(t, written, "first seed must write")

		written, err = s.SaveSettingsIfAbsent(ctx, second)
		require.NoError(t, err)
		assert.False(t, written, "second seed must be a no-op")

		got, err := s.GetSettings(ctx)
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.True(t, got.Enabled, "the first seed's value must survive")
	})

	t.Run("save settings if absent is atomic under concurrency", func(t *testing.T) {
		s := newStore(t)
		const writers = 8
		var wins int64
		var wg sync.WaitGroup
		for i := range writers {
			wg.Add(1)
			go func(n int) {
				defer wg.Done()
				gs := models.GuardrailsSettings{Enabled: n%2 == 0, Mode: models.ModeEnforce}
				written, err := s.SaveSettingsIfAbsent(ctx, gs)
				assert.NoError(t, err)
				if written {
					atomic.AddInt64(&wins, 1)
				}
			}(i)
		}
		wg.Wait()
		assert.Equal(t, int64(1), atomic.LoadInt64(&wins), "exactly one writer may win the seed race")

		got, err := s.GetSettings(ctx)
		require.NoError(t, err)
		require.NotNil(t, got)
	})

	t.Run("audit round trip preserves all fields", func(t *testing.T) {
		s := newStore(t)
		rec := sampleAuditRecord("req-1", time.Now())
		rec.MaskedTexts = []string{"My email is <EMAIL_1>", "token <API_KEY_1>"}

		require.NoError(t, s.PutAuditRecord(ctx, rec))
		got, err := s.GetAuditRecord(ctx, "req-1")
		require.NoError(t, err)
		assert.Equal(t, rec, got)
	})

	t.Run("audit round trip preserves replacement originals", func(t *testing.T) {
		s := newStore(t)
		rec := sampleAuditRecord("req-1", time.Now())
		for i := range rec.Replacements {
			rec.Replacements[i].Original = "secret-" + rec.Replacements[i].Placeholder
		}

		require.NoError(t, s.PutAuditRecord(ctx, rec))
		got, err := s.GetAuditRecord(ctx, "req-1")
		require.NoError(t, err)
		assert.Equal(t, rec, got)
	})

	t.Run("set audit response texts by request id", func(t *testing.T) {
		s := newStore(t)
		rec := sampleAuditRecord("req-1", time.Now())
		require.NoError(t, s.PutAuditRecord(ctx, rec))

		texts := []string{"The answer is <EMAIL_1>", "see <API_KEY_1>"}
		require.NoError(t, s.SetAuditResponseTexts(ctx, "req-1", texts))

		got, err := s.GetAuditRecord(ctx, "req-1")
		require.NoError(t, err)
		assert.Equal(t, texts, got.MaskedResponseTexts)
		// The rest of the record is unchanged.
		rec.MaskedResponseTexts = texts
		assert.Equal(t, rec, got)
	})

	t.Run("set audit response texts missing record", func(t *testing.T) {
		s := newStore(t)
		err := s.SetAuditResponseTexts(ctx, "missing", []string{"x"})
		require.ErrorIs(t, err, repository.ErrNotFound)
	})

	t.Run("audit not found", func(t *testing.T) {
		s := newStore(t)
		_, err := s.GetAuditRecord(ctx, "missing")
		require.ErrorIs(t, err, repository.ErrNotFound)
	})

	t.Run("audit upsert by request id", func(t *testing.T) {
		s := newStore(t)
		rec := sampleAuditRecord("req-1", time.Now())
		require.NoError(t, s.PutAuditRecord(ctx, rec))

		rec2 := rec
		rec2.TriggeredRuleIDs = []string{"pii.phone"}
		require.NoError(t, s.PutAuditRecord(ctx, rec2))

		got, err := s.GetAuditRecord(ctx, "req-1")
		require.NoError(t, err)
		assert.Equal(t, rec2, got)

		page, err := s.ListAuditRecords(ctx, repository.AuditQuery{})
		require.NoError(t, err)
		assert.Len(t, page.Records, 1)
	})

	t.Run("audit list newest first", func(t *testing.T) {
		s := newStore(t)
		base := time.Now().Add(-time.Minute)
		for i, id := range []string{"req-a", "req-b", "req-c"} {
			require.NoError(t, s.PutAuditRecord(ctx, sampleAuditRecord(id, base.Add(time.Duration(i)*time.Second))))
		}

		page, err := s.ListAuditRecords(ctx, repository.AuditQuery{})
		require.NoError(t, err)
		require.Len(t, page.Records, 3)
		assert.Equal(t, "req-c", page.Records[0].RequestID)
		assert.Equal(t, "req-b", page.Records[1].RequestID)
		assert.Equal(t, "req-a", page.Records[2].RequestID)
		assert.Empty(t, page.NextCursor)
	})

	t.Run("audit list pagination", func(t *testing.T) {
		s := newStore(t)
		base := time.Now().Add(-time.Minute)
		const total = 5
		for i := range total {
			id := "req-" + string(rune('a'+i))
			require.NoError(t, s.PutAuditRecord(ctx, sampleAuditRecord(id, base.Add(time.Duration(i)*time.Second))))
		}

		var seen []string
		cursor := ""
		for range total { // bounded walk: must exhaust in <= total pages
			page, err := s.ListAuditRecords(ctx, repository.AuditQuery{Limit: 2, Cursor: cursor})
			require.NoError(t, err)
			for _, r := range page.Records {
				seen = append(seen, r.RequestID)
			}
			cursor = page.NextCursor
			if cursor == "" {
				break
			}
		}
		assert.Equal(t, []string{"req-e", "req-d", "req-c", "req-b", "req-a"}, seen,
			"cursor walk must return every record exactly once, newest first")
		assert.Empty(t, cursor)
	})

	t.Run("audit list filters", func(t *testing.T) {
		s := newStore(t)
		base := time.Now().Add(-time.Minute)

		r1 := sampleAuditRecord("req-1", base)
		r1.Model, r1.Path = "gpt-a", "/v1/chat/completions"
		r1.TriggeredRuleIDs = []string{"pii.email"}
		r1.TriggeredDataTypes = []models.DataType{models.DataTypePERSONALDATA}

		r2 := sampleAuditRecord("req-2", base.Add(time.Second))
		r2.Model, r2.Path = "gpt-b", "/v1/messages"
		r2.TriggeredRuleIDs = []string{"generic.api_key"}
		r2.TriggeredDataTypes = []models.DataType{models.DataTypeAPIKEYS}

		require.NoError(t, s.PutAuditRecord(ctx, r1))
		require.NoError(t, s.PutAuditRecord(ctx, r2))

		cases := []struct {
			name string
			q    repository.AuditQuery
			want []string
		}{
			{"by model", repository.AuditQuery{Model: "gpt-a"}, []string{"req-1"}},
			{"by path", repository.AuditQuery{Path: "/v1/messages"}, []string{"req-2"}},
			{"by rule id", repository.AuditQuery{RuleID: "pii.email"}, []string{"req-1"}},
			{"by data type", repository.AuditQuery{DataType: models.DataTypeAPIKEYS}, []string{"req-2"}},
			{"since", repository.AuditQuery{Since: base.Add(500 * time.Millisecond)}, []string{"req-2"}},
			{"until", repository.AuditQuery{Until: base.Add(500 * time.Millisecond)}, []string{"req-1"}},
			{"no match", repository.AuditQuery{Model: "absent"}, nil},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				page, err := s.ListAuditRecords(ctx, tc.q)
				require.NoError(t, err)
				var ids []string
				for _, r := range page.Records {
					ids = append(ids, r.RequestID)
				}
				assert.Equal(t, tc.want, ids)
			})
		}
	})

	t.Run("audit list default limit", func(t *testing.T) {
		s := newStore(t)
		base := time.Now().Add(-time.Hour)
		for i := range repository.DefaultAuditPageSize + 1 {
			id := fmt.Sprintf("req-%03d", i)
			require.NoError(t, s.PutAuditRecord(ctx, sampleAuditRecord(id, base.Add(time.Duration(i)*time.Second))))
		}

		page, err := s.ListAuditRecords(ctx, repository.AuditQuery{})
		require.NoError(t, err)
		assert.Len(t, page.Records, repository.DefaultAuditPageSize)
		require.NotEmpty(t, page.NextCursor)

		rest, err := s.ListAuditRecords(ctx, repository.AuditQuery{Cursor: page.NextCursor})
		require.NoError(t, err)
		assert.Len(t, rest.Records, 1)
		assert.Empty(t, rest.NextCursor)
	})

	t.Run("audit bad cursor", func(t *testing.T) {
		s := newStore(t)
		_, err := s.ListAuditRecords(ctx, repository.AuditQuery{Cursor: "not-a-cursor!!!"})
		require.ErrorIs(t, err, repository.ErrBadCursor)
	})

	if opts.ExpireAudit != nil {
		t.Run("audit TTL expiry", func(t *testing.T) {
			s := newStore(t)
			require.NoError(t, s.PutAuditRecord(ctx, sampleAuditRecord("req-ttl", time.Now())))
			opts.ExpireAudit(t)

			_, err := s.GetAuditRecord(ctx, "req-ttl")
			require.ErrorIs(t, err, repository.ErrNotFound)

			page, err := s.ListAuditRecords(ctx, repository.AuditQuery{})
			require.NoError(t, err)
			assert.Empty(t, page.Records)
		})
	}

	t.Run("ping", func(t *testing.T) {
		s := newStore(t)
		ctxTimeout, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		require.NoError(t, s.Ping(ctxTimeout))
	})
}
