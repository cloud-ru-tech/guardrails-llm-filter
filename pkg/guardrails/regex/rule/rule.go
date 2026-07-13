package rule

// ValidatorType lists supported candidate validators.
type ValidatorType string

const (
	ValidatorLuhn        ValidatorType = "luhn"
	ValidatorSNILS       ValidatorType = "snils"
	ValidatorINNPerson   ValidatorType = "inn_person"
	ValidatorINNOrg      ValidatorType = "inn_org"
	ValidatorOGRN        ValidatorType = "ogrn"
	ValidatorOGRNIP      ValidatorType = "ogrnip"
	ValidatorIBANMod97   ValidatorType = "iban_mod97"
	ValidatorEmailASCII  ValidatorType = "email_ascii"
	ValidatorPaymentCard ValidatorType = "payment_card"
	ValidatorEntropy     ValidatorType = "entropy"
	ValidatorBanlist     ValidatorType = "banlist"
	ValidatorIPv4        ValidatorType = "ip_v4"
	ValidatorIPv6        ValidatorType = "ip_v6"
	ValidatorIPPublic    ValidatorType = "ip_public"
	ValidatorIPPrivate   ValidatorType = "ip_private"
)

// MaskingConfig describes how to replace a detected value during masking.
type MaskingConfig struct {
	CaptureGroups []int  `yaml:"capture_groups,omitempty,flow" json:"capture_groups,omitempty"` // empty -> full match; otherwise first matched group wins
	Placeholder   string `yaml:"placeholder" json:"placeholder"`                                // non-empty → placeholder mode <NAME_N>
}

// DataType is a user-facing toggle category defined in guardrails_regex_rules.yaml.
type DataType struct {
	DataType      int    `yaml:"data_type"`
	GroupPriority int    `yaml:"group_priority"`
	Name          string `yaml:"name"`
	DisplayName   string `yaml:"display_name"`
	Description   string `yaml:"description"`
}

// Rule is a single detection rule loaded from guardrails_regex_rules.yaml
// or created via the configuration API.
//
// In YAML, Group and DataType come from the parent group and are set by
// LoadAll. In JSON (rule stores, configuration API) they are serialized
// explicitly because a stored rule must round-trip on its own.
type Rule struct {
	ID         string          `yaml:"rule_id" json:"rule_id"`
	Name       string          `yaml:"name" json:"name"`
	Group      string          `yaml:"-" json:"group,omitempty"`
	DataType   int             `yaml:"-" json:"data_type"`
	Regex      string          `yaml:"regex" json:"regex"`
	Keywords   []string        `yaml:"keywords" json:"keywords,omitempty"`
	Validators []ValidatorType `yaml:"validators" json:"validators,omitempty"`
	MinLength  int             `yaml:"min_length" json:"min_length,omitempty"`
	Entropy    float64         `yaml:"entropy" json:"entropy,omitempty"`
	Banlist    []string        `yaml:"banlist" json:"banlist,omitempty"`
	DefaultOn  bool            `yaml:"default_on" json:"default_on"`
	Masking    MaskingConfig   `yaml:"masking" json:"masking"`
}
