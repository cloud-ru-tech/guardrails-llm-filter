package gitleaksgen

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"

	tomlsrc "github.com/cloud-ru-tech/guardrails-llm-filter/pkg/gitleaksgen/toml"
)

const (
	groupAPI   = "api_tokens"
	groupKeys  = "access_keys"
	groupCreds = "credentials"

	gitleaksRuleIDSuffix = ".gl"

	standardGitleaksBoundary = `(?:[\x60'"\s;]|\\[nr]|$)`
	promptTokenBoundary      = `(?:[\x60'"\s,;:!?()\[\]{}]|\\[nr]|$)`
)

var apiWordRe = regexp.MustCompile(`(^|[^a-z0-9])api([^a-z0-9]|$)`)

var gitleaksRegexOverrides = map[string]string{
	// Upstream matches the whole cookie assignment. Mask only the session value,
	// leaving the cookie name and header syntax intact.
	"gitlab-session-cookie": `_gitlab_session=([0-9a-z]{32})`,
	// Upstream captures the surrounding HCL quotes as part of the password.
	"hashicorp-tf-password": `(?i)[\w.-]{0,50}?(?:administrator_login_password|password)(?:[ \t\w.-]{0,20})[\s'"]{0,3}(?:=|>|:{1,3}=|\|\||:|=>|\?=|,)[\x60'"\s=]{0,5}\\?"([a-z0-9=_\-]{8,20})\\?"(?:[\x60'"\s;]|\\[nr]|$)`,
	// Upstream only accepts whitespace/EOF after the -u value. Prompts often put
	// curl snippets before commas or closing brackets; keep punctuation outside
	// the semantic capture groups.
	"curl-auth-user": `\bcurl\b(?:.*|.*(?:[\r\n]{1,2}.*){1,5})[ \t\n\r](?:-u|--user)(?:=|[ \t]{0,5})("(:[^"]{3,}|[^:"]{3,}:|[^:"]{3,}:[^"]{3,})"|'([^:']{3,}:[^']{3,})'|((?:"[^"]{3,}"|'[^']{3,}'|[\w$@.-]+):(?:"[^"]{3,}"|'[^']{3,}'|[\w${}@.-]+)))(?:[\x60'"\s,;:!?()\[\]{}]|\\[nr]|\z)`,
	// The upstream generated regexp allows escaped backslashes inside JWT segments,
	// which makes JSON-string argument masking overmatch. Keep the local narrowed form.
	"jwt": `\b(ey[a-zA-Z0-9]{17,}\.ey[a-zA-Z0-9\/_-]{17,}\.(?:[a-zA-Z0-9\/_-]{10,}={0,2})?)(?:[\x60'"\s;]|\\[nr]|$)`,
	// Upstream captures "key: value" and masking it breaks YAML structure. Keep
	// the Kubernetes Secret context, but select only the scalar data value.
	"kubernetes-secret-yaml": `(?i)(?:\bkind:[ \t]*["']?\bsecret\b["']?(?s:.){0,200}?\bdata:(?s:.){0,100}?\s+[\w.-]+:(?:[ \t]*(?:\||>[-+]?)\s+)?[ \t]*["']?([a-z0-9+/]{10,}={0,3})["']?|\bdata:(?s:.){0,100}?\s+[\w.-]+:(?:[ \t]*(?:\||>[-+]?)\s+)?[ \t]*["']?([a-z0-9+/]{10,}={0,3})["']?(?s:.){0,200}?\bkind:[ \t]*["']?\bsecret\b["']?)`,
	// Upstream has no capture group and consumes one trailing non-base64 delimiter.
	"sentry-org-token": `\b(sntrys_eyJpYXQiO[a-zA-Z0-9+/]{10,200}(?:LCJyZWdpb25fdXJs|InJlZ2lvbl91cmwi|cmVnaW9uX3VybCI6)[a-zA-Z0-9+/]{10,200}={0,2}_[a-zA-Z0-9+/]{43})(?:[^a-zA-Z0-9+/]|\z)`,
	// Upstream captures only the user:password pair. The rule represents a
	// sensitive URL, so mask the full URL authority without the right delimiter.
	"sidekiq-sensitive-url": `(?i)\b(https?://[a-f0-9]{8}:[a-f0-9]{8}@(?:gems.contribsys.com|enterprise.contribsys.com))(?:[\/#?:\x60'"\s,;.!()\[\]{}]|$)`,
}

var gitleaksCaptureGroupOverrides = map[string][]int{
	"atlassian-api-token":          {1, 2},
	"curl-auth-header":             {1, 2, 3, 4, 5, 6, 7, 8},
	"curl-auth-user":               {2, 3, 4},
	"dropbox-long-lived-api-token": {1},
	"facebook-access-token":        {1},
	"heroku-api-key-v2":            {1},
	"kubernetes-secret-yaml":       {1, 2},
	"lob-api-key":                  {1},
	"lob-pub-api-key":              {1},
	"sourcegraph-access-token":     {1},
}

var gitleaksForceFullMatch = map[string]struct{}{
	"jwt-base64":              {},
	"microsoft-teams-webhook": {},
}

func GenerateFMGuardrailsRegexRulesFromGitleaks(gitleaksTomlPath, outputPath string) (Stats, error) {
	if strings.TrimSpace(gitleaksTomlPath) == "" {
		return Stats{}, fmt.Errorf("gitleaks TOML path is empty")
	}
	if strings.TrimSpace(outputPath) == "" {
		return Stats{}, fmt.Errorf("output path is empty")
	}

	cfg, err := tomlsrc.Load(gitleaksTomlPath)
	if err != nil {
		return Stats{}, err
	}

	gen, stats, err := buildFMGuardrailsRegexRulesFromConfig(cfg, DefaultGitleaksPolicy())
	if err != nil {
		return Stats{}, err
	}
	yamlBytes, err := MarshalOutput(gen)
	if err != nil {
		return Stats{}, fmt.Errorf("marshal generated rules: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return Stats{}, fmt.Errorf("create output dir: %w", err)
	}
	if err := os.WriteFile(outputPath, yamlBytes, 0o644); err != nil {
		return Stats{}, fmt.Errorf("write output file %s: %w", outputPath, err)
	}
	return stats, nil
}

// buildFMGuardrailsRegexRulesFromConfig builds one or more guardrails groups from gitleaks rules.
func buildFMGuardrailsRegexRulesFromConfig(cfg tomlsrc.Config, policy Policy) (OutputFile, Stats, error) {
	stats := Stats{TotalRules: len(cfg.Rules)}

	excluded := make(map[string]struct{}, len(policy.ExcludeRuleIDs))
	for _, id := range policy.ExcludeRuleIDs {
		excluded[strings.TrimSpace(id)] = struct{}{}
	}

	// Counts occurrences per stem "data_type.base_id" for disambiguation suffixes (_2, _3, …).
	usedRuleStems := make(map[string]int, len(cfg.Rules))
	dataTypes, groupIndex := buildGroups(policy)

	for _, src := range cfg.Rules {
		if src.ID == "" || strings.TrimSpace(src.Regex) == "" {
			stats.SkippedInvalidRule++
			continue
		}
		if _, ok := excluded[src.ID]; ok {
			stats.SkippedExcludedID++
			continue
		}

		trimmedRegex := gitleaksRegexForRule(src)
		re, err := regexp.Compile("(?m)" + trimmedRegex)
		if err != nil {
			if policy.FailOnBadRegex {
				return OutputFile{}, stats, fmt.Errorf("rule %q regex is not RE2-compatible: %w", src.ID, err)
			}
			stats.SkippedInvalidRule++
			continue
		}
		captureGroups := captureGroupsForGitleaksRule(src, re)
		trimmedRegex, _, err = normalizeGitleaksRegexForMasking(src, trimmedRegex, captureGroups, re)
		if err != nil {
			if policy.FailOnBadRegex {
				return OutputFile{}, stats, err
			}
			stats.SkippedInvalidRule++
			continue
		}

		baseID := gitleaksRuleIDSegment(src.ID)
		if baseID == "" {
			stats.SkippedInvalidRule++
			continue
		}

		idx := 0
		if len(groupIndex) > 0 {
			key := classifyGroup(src)
			matched, ok := groupIndex[key]
			if !ok {
				matched, ok = groupIndex[policy.DefaultGroupKey]
				if !ok {
					matched = 0
				}
			}
			idx = matched
		}
		dtPrefix := dataTypeNameForRuleID(dataTypes[idx])
		fullID := allocateGitleaksRuleID(dtPrefix, baseID, usedRuleStems)

		r := OutputRule{
			RuleID:  fullID,
			Name:    fullID,
			Regex:   trimmedRegex,
			Masking: maskingForGitleaksRule(src, captureGroups, policy),
		}
		if policy.DefaultMinLength > 0 {
			r.MinLength = policy.DefaultMinLength
		}
		if policy.IncludeKeywords && len(src.Keywords) > 0 {
			r.Keywords = append([]string(nil), src.Keywords...)
		}
		if policy.IncludeEntropy && src.Entropy > 0 {
			r.Entropy = src.Entropy
		}

		dataTypes[idx].Rules = append(dataTypes[idx].Rules, r)
	}

	total := 0
	filtered := make([]OutputDataType, 0, len(dataTypes))
	for _, dt := range dataTypes {
		sort.Slice(dt.Rules, func(i, j int) bool {
			return dt.Rules[i].RuleID < dt.Rules[j].RuleID
		})
		if len(dt.Rules) == 0 {
			continue
		}
		total += len(dt.Rules)
		filtered = append(filtered, dt)
	}

	stats.GeneratedRules = total
	return OutputFile{DataTypes: filtered}, stats, nil
}

func gitleaksRegexForRule(src tomlsrc.Rule) string {
	if override, ok := gitleaksRegexOverrides[src.ID]; ok {
		return override
	}
	return strings.TrimSpace(src.Regex)
}

func normalizeGitleaksRegexForMasking(src tomlsrc.Rule, pattern string, captureGroups []int, re *regexp.Regexp) (string, *regexp.Regexp, error) {
	if len(captureGroups) == 0 || !strings.HasSuffix(pattern, standardGitleaksBoundary) {
		return pattern, re, nil
	}

	normalized := strings.TrimSuffix(pattern, standardGitleaksBoundary) + promptTokenBoundary
	compiled, err := regexp.Compile("(?m)" + normalized)
	if err != nil {
		return "", nil, fmt.Errorf("rule %q normalized regex is not RE2-compatible: %w", src.ID, err)
	}
	return normalized, compiled, nil
}

func maskingForGitleaksRule(src tomlsrc.Rule, captureGroups []int, policy Policy) OutputMasking {
	return OutputMasking{
		CaptureGroups: append([]int(nil), captureGroups...),
		Placeholder:   rulePlaceholder(src.ID, policy),
	}
}

func captureGroupsForGitleaksRule(src tomlsrc.Rule, re *regexp.Regexp) []int {
	if src.SecretGroup > 0 {
		return []int{src.SecretGroup}
	}
	if groups, ok := gitleaksCaptureGroupOverrides[src.ID]; ok {
		return append([]int(nil), groups...)
	}
	if _, ok := gitleaksForceFullMatch[src.ID]; ok {
		return nil
	}
	if re.NumSubexp() == 1 {
		return []int{1}
	}
	return nil
}

func sanitizeID(s string) string {
	return sanitizeIDWithSep(s, '_')
}

// gitleaksRuleIDSegment normalizes a gitleaks rule id for use in rule_id after the data_type prefix (hyphens, like upstream gitleaks ids).
func gitleaksRuleIDSegment(s string) string {
	return sanitizeIDWithSep(s, '-')
}

func sanitizeIDWithSep(s string, sep byte) string {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(s))
	prevSep := false
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			prevSep = false
			continue
		}
		if prevSep {
			continue
		}
		b.WriteByte(sep)
		prevSep = true
	}
	cut := string(sep)
	out := strings.Trim(b.String(), cut)
	dup := cut + cut
	for strings.Contains(out, dup) {
		out = strings.ReplaceAll(out, dup, cut)
	}
	return out
}

func dataTypeNameForRuleID(dt OutputDataType) string {
	s := strings.TrimSpace(dt.Name)
	if s == "" {
		return "unspecified"
	}
	return strings.ToLower(s)
}

// allocateGitleaksRuleID builds rule_id as "{data_type}.{gitleaks_id}.gl", with _2, _3, … before ".gl" if the stem repeats.
// gitleaks_id uses hyphen separators to mirror gitleaks rule ids.
func allocateGitleaksRuleID(dataTypePrefix, baseID string, used map[string]int) string {
	stem := dataTypePrefix + "." + baseID
	n := used[stem]
	var fullID string
	if n == 0 {
		fullID = stem + gitleaksRuleIDSuffix
	} else {
		fullID = fmt.Sprintf("%s_%d%s", stem, n+1, gitleaksRuleIDSuffix)
	}
	used[stem]++
	return fullID
}

func rulePlaceholder(srcRuleID string, policy Policy) string {
	if policy.PlaceholderByID {
		base := sanitizeID(srcRuleID)
		if base == "" {
			base = "RULE"
		}
		base = strings.ToUpper(base)
		prefix := strings.TrimSpace(policy.PlaceholderPrefix)
		prefix = strings.Trim(prefix, "_")
		if prefix == "" {
			return base
		}
		return strings.ToUpper(prefix) + "_" + base
	}
	return policy.Placeholder
}

func buildGroups(policy Policy) ([]OutputDataType, map[string]int) {
	if len(policy.Groups) == 0 {
		return []OutputDataType{{
			DataType:      policy.DataType,
			GroupPriority: policy.GroupPriority,
			Name:          policy.Name,
			DisplayName:   policy.DisplayName,
			Description:   policy.Description,
		}}, nil
	}

	out := make([]OutputDataType, 0, len(policy.Groups))
	index := make(map[string]int, len(policy.Groups))
	for _, g := range policy.Groups {
		if _, exists := index[g.Key]; exists {
			continue
		}
		index[g.Key] = len(out)
		out = append(out, OutputDataType{
			DataType:      g.DataType,
			GroupPriority: g.GroupPriority,
			Name:          g.Name,
			DisplayName:   g.DisplayName,
			Description:   g.Description,
		})
	}
	if len(out) == 0 {
		return []OutputDataType{{
			DataType:      policy.DataType,
			GroupPriority: policy.GroupPriority,
			Name:          policy.Name,
			DisplayName:   policy.DisplayName,
			Description:   policy.Description,
		}}, nil
	}
	return out, index
}

func classifyGroup(src tomlsrc.Rule) string {
	id := strings.ToLower(strings.TrimSpace(src.ID))
	if id == "" {
		return groupKeys
	}

	if hasAny(id,
		"-password",
		"_password",
		"client-id",
		"client-secret",
		"oauth",
		"refresh-token",
		"session-cookie",
		"cookie",
		"jwt",
		"auth-header",
		"auth-user",
		"sensitive-url",
		"access-id",
		"user-api-id",
	) {
		return groupCreds
	}
	if strings.Contains(id, "secret") && !strings.Contains(id, "secret-key") {
		return groupCreds
	}

	if hasAPIWord(src) {
		return groupAPI
	}

	return groupKeys
}

func hasAny(s string, needles ...string) bool {
	for _, n := range needles {
		if strings.Contains(s, n) {
			return true
		}
	}
	return false
}

func hasAPIWord(src tomlsrc.Rule) bool {
	if apiWordRe.MatchString(strings.ToLower(src.ID)) {
		return true
	}
	if apiWordRe.MatchString(strings.ToLower(src.Description)) {
		return true
	}
	for _, kw := range src.Keywords {
		if apiWordRe.MatchString(strings.ToLower(kw)) {
			return true
		}
	}
	return false
}
