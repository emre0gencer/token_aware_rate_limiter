// Package config loads gateway settings + rules + pricing from YAML.
// Durations are written as strings ("60s", "1s") and parsed here.
package config

import (
	"fmt"
	"os"
	"time"

	"github.com/egencer/distributed-rate-limiter-gateway/internal/cost"
	"github.com/egencer/distributed-rate-limiter-gateway/internal/rules"
	"gopkg.in/yaml.v3"
)

type Config struct {
	Server   Server
	Redis    Redis
	Upstream Upstream
	Pricing  cost.PriceTable
	Rules    []rules.Rule
}

type Server struct {
	Addr string // listen address, e.g. ":8080"
}

type Redis struct {
	Addr      string
	Timeout   time.Duration // per-op deadline
}

type Upstream struct {
	BaseURL         string // LLM provider base, e.g. https://api.openai.com
	DefaultMaxTokens int   // fallback completion bound when request omits max_tokens
}

// raw mirrors the YAML shape, with string durations, before conversion.
type raw struct {
	Server struct {
		Addr string `yaml:"addr"`
	} `yaml:"server"`
	Redis struct {
		Addr      string `yaml:"addr"`
		TimeoutMS int    `yaml:"timeout_ms"`
	} `yaml:"redis"`
	Upstream struct {
		BaseURL          string `yaml:"base_url"`
		DefaultMaxTokens int    `yaml:"default_max_tokens"`
	} `yaml:"upstream"`
	Pricing cost.PriceTable `yaml:"pricing"`
	Rules   []rawRule       `yaml:"rules"`
}

type rawRule struct {
	ID        string   `yaml:"id"`
	Scope     string   `yaml:"scope"`
	Algorithm string   `yaml:"algorithm"`
	Unit      string   `yaml:"unit"`
	Limit     float64  `yaml:"limit"`
	Burst     float64  `yaml:"burst"`
	Window    string   `yaml:"window"`
	FailOpen  bool     `yaml:"fail_open"`
	Models    []string `yaml:"models"`
}

// Load reads and validates a config file.
func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var r raw
	if err := yaml.Unmarshal(b, &r); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}

	cfg := &Config{
		Server:   Server{Addr: orDefault(r.Server.Addr, ":8080")},
		Redis:    Redis{Addr: orDefault(r.Redis.Addr, "localhost:6379"), Timeout: msOrDefault(r.Redis.TimeoutMS, 50)},
		Upstream: Upstream{BaseURL: r.Upstream.BaseURL, DefaultMaxTokens: intOrDefault(r.Upstream.DefaultMaxTokens, 512)},
		Pricing:  r.Pricing,
	}

	for _, rr := range r.Rules {
		win, err := time.ParseDuration(orDefault(rr.Window, "1s"))
		if err != nil {
			return nil, fmt.Errorf("config: rule %q bad window: %w", rr.ID, err)
		}
		cfg.Rules = append(cfg.Rules, rules.Rule{
			ID:        rr.ID,
			Scope:     rr.Scope,
			Algorithm: rules.Algorithm(rr.Algorithm),
			Unit:      rules.Unit(rr.Unit),
			Limit:     rr.Limit,
			Burst:     rr.Burst,
			Window:    win,
			FailOpen:  rr.FailOpen,
			Models:    rr.Models,
		})
	}
	if cfg.Upstream.BaseURL == "" {
		return nil, fmt.Errorf("config: upstream.base_url is required")
	}
	return cfg, nil
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
func intOrDefault(v, def int) int {
	if v == 0 {
		return def
	}
	return v
}
func msOrDefault(ms, def int) time.Duration {
	if ms == 0 {
		ms = def
	}
	return time.Duration(ms) * time.Millisecond
}
