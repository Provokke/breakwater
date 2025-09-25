package proxy

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParseConfigDefaults(t *testing.T) {
	cfg, err := ParseConfig([]byte(`{"upstreams": ["http://localhost:9000"], "rate_per_second": 5}`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != ":8080" || cfg.HealthPath != "/" {
		t.Fatalf("defaults not applied: %+v", cfg)
	}
	if cfg.HealthInterval.Duration != 5*time.Second || cfg.HealthTimeout.Duration != 2*time.Second {
		t.Fatalf("health defaults wrong: %+v", cfg)
	}
	if cfg.Burst != 5 {
		t.Fatalf("burst = %d, want 5", cfg.Burst)
	}
}

func TestParseConfigDurations(t *testing.T) {
	cfg, err := ParseConfig([]byte(`{"upstreams": ["http://x:1"], "health_interval": "750ms"}`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HealthInterval.Duration != 750*time.Millisecond {
		t.Fatalf("interval = %v", cfg.HealthInterval.Duration)
	}
}

func TestParseConfigErrors(t *testing.T) {
	cases := map[string]string{
		"no upstreams":     `{}`,
		"unknown field":    `{"upstreams": ["http://x:1"], "bogus": 1}`,
		"negative rate":    `{"upstreams": ["http://x:1"], "rate_per_second": -1}`,
		"bad duration":     `{"upstreams": ["http://x:1"], "health_interval": "soon"}`,
		"numeric duration": `{"upstreams": ["http://x:1"], "health_interval": 5}`,
		"not json":         `upstreams`,
	}
	for name, body := range cases {
		if _, err := ParseConfig([]byte(body)); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestLoadConfigFromFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.json")
	if err := os.WriteFile(path, []byte(`{"upstreams": ["http://x:1"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil || len(cfg.Upstreams) != 1 {
		t.Fatalf("LoadConfig = %+v, %v", cfg, err)
	}
	if _, err := LoadConfig(path + ".missing"); err == nil {
		t.Fatal("expected an error for a missing file")
	}
}
