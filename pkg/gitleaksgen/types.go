package gitleaksgen

import (
	"github.com/cloud-ru-tech/guardrails-llm-filter/pkg/guardrails/regex/rule"
	"github.com/goccy/go-yaml"
)

// Stats summarizes import result.
type Stats struct {
	TotalRules         int
	GeneratedRules     int
	SkippedExcludedID  int
	SkippedInvalidRule int
}

// Group configures one generated data_type section in output YAML.
type Group struct {
	Key           string `yaml:"key"`
	DataType      int    `yaml:"data_type"`
	GroupPriority int    `yaml:"group_priority"`
	Name          string `yaml:"name"`
	DisplayName   string `yaml:"display_name"`
	Description   string `yaml:"description"`
}

// OutputFile matches guardrails rules YAML top-level schema.
type OutputFile struct {
	DataTypes []OutputDataType `yaml:"guardrails_regex_rules"`
}

// MarshalOutput encodes generated output into guardrails YAML.
func MarshalOutput(f OutputFile) ([]byte, error) {
	return yaml.MarshalWithOptions(f, yaml.IndentSequence(true))
}

// OutputDataType is one group entry in generated YAML.
type OutputDataType struct {
	DataType      int          `yaml:"data_type"`
	GroupPriority int          `yaml:"group_priority"`
	Name          string       `yaml:"name"`
	DisplayName   string       `yaml:"display_name"`
	Description   string       `yaml:"description"`
	Rules         []OutputRule `yaml:"rules"`
}

// OutputRule is one generated guardrails rule.
type OutputRule struct {
	RuleID     string               `yaml:"rule_id"`
	Name       string               `yaml:"name"`
	Regex      string               `yaml:"regex"`
	Keywords   []string             `yaml:"keywords,omitempty,flow"`
	MinLength  int                  `yaml:"min_length,omitempty"`
	Entropy    float64              `yaml:"entropy,omitempty"`
	Masking    OutputMasking        `yaml:"masking"`
	Validators []rule.ValidatorType `yaml:"validators,omitempty"`
}

type OutputMasking struct {
	CaptureGroups []int  `yaml:"capture_groups,omitempty,flow"`
	Placeholder   string `yaml:"placeholder,omitempty"`
}
