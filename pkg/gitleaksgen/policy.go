package gitleaksgen

import ()

// Policy configures import behavior from imported gitleaks rules.
type Policy struct {
	DataType          int
	GroupPriority     int
	Name              string
	DisplayName       string
	Description       string
	Placeholder       string
	PlaceholderByID   bool
	PlaceholderPrefix string
	ExcludeRuleIDs    []string
	FailOnBadRegex    bool
	IncludeKeywords   bool
	IncludeEntropy    bool
	DefaultMinLength  int
	Groups            []Group
	DefaultGroupKey   string
}

// DefaultGitleaksPolicy returns built-in import policy for gitleaks.
// It is used when CLI import runs without external policy file.
func DefaultGitleaksPolicy() Policy {
	p := Policy{
		PlaceholderByID: true,
		IncludeKeywords: true,
		IncludeEntropy:  true,
		DefaultGroupKey: "api_tokens",
		Groups: []Group{
			{
				Key:           "credentials",
				DataType:      1,
				GroupPriority: 80,
				Name:          "CREDENTIALS",
				DisplayName:   "Учетные данные",
				Description:   "Пароли, client credentials, OAuth/session артефакты",
			},
			{
				Key:           "api_tokens",
				DataType:      2,
				GroupPriority: 100,
				Name:          "API_KEYS",
				DisplayName:   "API-ключи",
				Description:   "Правила gitleaks с маркером api (api-key/api-token и т.п.)",
			},
			{
				Key:           "access_keys",
				DataType:      3,
				GroupPriority: 90,
				Name:          "ACCESS_TOKENS",
				DisplayName:   "Токены доступа",
				Description:   "Ключи доступа, токены/секреты",
			},
		},
		ExcludeRuleIDs: []string{"generic-api-key"},
	}
	return p
}
