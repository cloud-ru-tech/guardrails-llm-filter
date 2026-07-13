package toml

// Config is a subset of gitleaks TOML schema required for guardrails import.
type Config struct {
	Title string `toml:"title"`
	Rules []Rule `toml:"rules"`
}

// Rule describes one gitleaks detection rule.
type Rule struct {
	ID          string   `toml:"id"`
	Description string   `toml:"description"`
	Regex       string   `toml:"regex"`
	SecretGroup int      `toml:"secretGroup"`
	Keywords    []string `toml:"keywords"`
	Entropy     float64  `toml:"entropy"`
}
