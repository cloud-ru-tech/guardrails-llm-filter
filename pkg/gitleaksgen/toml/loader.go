package toml

import (
	"fmt"
	"os"

	burnttoml "github.com/BurntSushi/toml"
)

// Load reads gitleaks TOML config from file.
func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read gitleaks file %s: %w", path, err)
	}
	return Parse(data)
}

// Parse decodes gitleaks TOML config from bytes.
func Parse(data []byte) (Config, error) {
	var cfg Config
	if err := burnttoml.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse gitleaks TOML: %w", err)
	}
	return cfg, nil
}
