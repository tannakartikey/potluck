// Package config manages the contributor's local Potluck state: a JSON config
// and a secret key file, both under ~/.potluck (override with POTLUCK_HOME).
// Nothing here is ever uploaded — the key stays on the contributor's machine.
package config

import (
	crand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

type Config struct {
	DisplayName   string   `json:"display_name,omitempty"`
	ContributorID string   `json:"contributor_id,omitempty"`
	Topics        []string `json:"topics,omitempty"`
	Model         string   `json:"model,omitempty"`
	Backend       string   `json:"backend,omitempty"`
	BudgetTokens  int      `json:"budget_tokens,omitempty"`
}

// Dir is ~/.potluck (or $POTLUCK_HOME).
func Dir() string {
	if d := os.Getenv("POTLUCK_HOME"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".potluck")
}

func configPath() string { return filepath.Join(Dir(), "config.json") }
func credPath() string   { return filepath.Join(Dir(), "credentials") }

func ensureDir() error { return os.MkdirAll(Dir(), 0o700) }

// Load reads config.json, returning sensible defaults if it doesn't exist.
func Load() (*Config, error) {
	c := &Config{Model: "haiku", Backend: "claude-code", BudgetTokens: 8000}
	b, err := os.ReadFile(configPath())
	if err != nil {
		if os.IsNotExist(err) {
			return c, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(b, c); err != nil {
		return nil, err
	}
	if c.Model == "" {
		c.Model = "haiku"
	}
	if c.Backend == "" {
		c.Backend = "claude-code"
	}
	if c.BudgetTokens == 0 {
		c.BudgetTokens = 8000
	}
	return c, nil
}

func (c *Config) Save() error {
	if err := ensureDir(); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(configPath(), b, 0o600)
}

// GenerateKey returns a fresh high-entropy secret key. The public side is its
// SHA-256, stored server-side by register_contributor; the secret never leaves here.
func GenerateKey() (string, error) {
	buf := make([]byte, 32)
	if _, err := crand.Read(buf); err != nil {
		return "", err
	}
	return "potluck_" + hex.EncodeToString(buf), nil
}

func SaveKey(key string) error {
	if err := ensureDir(); err != nil {
		return err
	}
	return os.WriteFile(credPath(), []byte(key+"\n"), 0o600)
}

func LoadKey() (string, error) {
	b, err := os.ReadFile(credPath())
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

func HasKey() bool {
	_, err := os.Stat(credPath())
	return err == nil
}
