package proxy

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"
)

type Duration struct{ time.Duration }

func (d *Duration) UnmarshalJSON(data []byte) error {
	var text string
	if err := json.Unmarshal(data, &text); err != nil {
		return fmt.Errorf("duration must be a string like \"5s\"")
	}
	parsed, err := time.ParseDuration(text)
	if err != nil {
		return err
	}
	d.Duration = parsed
	return nil
}

type Config struct {
	Listen         string   `json:"listen"`
	Upstreams      []string `json:"upstreams"`
	RatePerSecond  float64  `json:"rate_per_second"`
	Burst          int      `json:"burst"`
	TrustForwarded bool     `json:"trust_forwarded"`
	HealthPath     string   `json:"health_path"`
	HealthInterval Duration `json:"health_interval"`
	HealthTimeout  Duration `json:"health_timeout"`
}

func (c *Config) applyDefaults() {
	if c.Listen == "" {
		c.Listen = ":8080"
	}
	if c.HealthPath == "" {
		c.HealthPath = "/"
	}
	if c.HealthInterval.Duration == 0 {
		c.HealthInterval.Duration = 5 * time.Second
	}
	if c.HealthTimeout.Duration == 0 {
		c.HealthTimeout.Duration = 2 * time.Second
	}
	if c.RatePerSecond > 0 && c.Burst == 0 {
		c.Burst = int(c.RatePerSecond)
		if c.Burst < 1 {
			c.Burst = 1
		}
	}
}

func (c *Config) Validate() error {
	if len(c.Upstreams) == 0 {
		return errors.New("config needs at least one upstream")
	}
	if c.RatePerSecond < 0 || c.Burst < 0 {
		return errors.New("rate_per_second and burst must not be negative")
	}
	if c.HealthInterval.Duration < 0 || c.HealthTimeout.Duration < 0 {
		return errors.New("health durations must not be negative")
	}
	return nil
}

func ParseConfig(data []byte) (Config, error) {
	var cfg Config
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("reading config: %w", err)
	}
	cfg.applyDefaults()
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func LoadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	return ParseConfig(data)
}

