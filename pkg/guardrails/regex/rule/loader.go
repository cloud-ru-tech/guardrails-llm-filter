package rule

import (
	"fmt"
	"os"
	"strings"

	"github.com/goccy/go-yaml"
)

// rulesFile mirrors the top-level structure of guardrails_regex_rules.yaml.
type rulesFile struct {
	DataTypes []groupEntry `yaml:"guardrails_regex_rules"`
}

// groupEntry decodes one YAML list item under guardrails_regex_rules (rules nested under it).
// Rules are decoded directly into []Rule; parent name and rule DataType are set in LoadAll
// because they are inherited from the parent entry, not stored per-rule.
type groupEntry struct {
	DataType      int    `yaml:"data_type"`
	GroupPriority int    `yaml:"group_priority"`
	Name          string `yaml:"name"`
	DisplayName   string `yaml:"display_name"`
	Description   string `yaml:"description"`
	Rules         []Rule `yaml:"rules"`
}

// LoadAll loads and parses data-type entries and rules from the given YAML file path.
//
// Each parsed Rule has its Group (name) and DataType fields set from the parent entry.
// Keywords and banlist entries are lower-cased for case-insensitive matching.
func LoadAll(path string) ([]DataType, []Rule, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("read rules file %s: %w", path, err)
	}

	var f rulesFile
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, nil, fmt.Errorf("parse rules YAML %s: %w", path, err)
	}

	dataTypes := make([]DataType, 0, len(f.DataTypes))
	var rules []Rule

	for _, g := range f.DataTypes {
		dataTypes = append(dataTypes, DataType{
			DataType:      g.DataType,
			GroupPriority: g.GroupPriority,
			Name:          g.Name,
			DisplayName:   g.DisplayName,
			Description:   g.Description,
		})

		for _, r := range g.Rules {
			r.Group = g.Name
			r.DataType = g.DataType
			r.Keywords = lowerStrings(r.Keywords)
			r.Banlist = lowerStrings(r.Banlist)
			rules = append(rules, r)
		}
	}

	return dataTypes, rules, nil
}

// LoadAllFromFiles loads and merges rules from multiple YAML files.
//
// Empty paths are skipped. Duplicate paths are loaded once.
// Returns an error when no files are provided or when duplicate rule_id appears
// across loaded files.
func LoadAllFromFiles(paths ...string) ([]DataType, []Rule, error) {
	seenPaths := make(map[string]struct{}, len(paths))
	seenRuleIDs := make(map[string]string)

	allDataTypes := make([]DataType, 0)
	allRules := make([]Rule, 0)
	loadedFiles := 0

	for _, rawPath := range paths {
		path := strings.TrimSpace(rawPath)
		if path == "" {
			continue
		}
		if _, exists := seenPaths[path]; exists {
			continue
		}
		seenPaths[path] = struct{}{}

		dataTypes, rules, err := LoadAll(path)
		if err != nil {
			return nil, nil, err
		}
		loadedFiles++
		allDataTypes = append(allDataTypes, dataTypes...)

		for _, rl := range rules {
			if prevPath, exists := seenRuleIDs[rl.ID]; exists {
				return nil, nil, fmt.Errorf("duplicate guardrails rule_id %q in files %s and %s", rl.ID, prevPath, path)
			}
			seenRuleIDs[rl.ID] = path
			allRules = append(allRules, rl)
		}
	}

	if loadedFiles == 0 {
		return nil, nil, fmt.Errorf("no guardrails rules files configured")
	}

	return allDataTypes, allRules, nil
}

func lowerStrings(values []string) []string {
	for i, value := range values {
		values[i] = strings.ToLower(value)
	}
	return values
}
