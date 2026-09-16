package config

import (
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/exemt/placitum-shared/loglevel"
)

type Config struct {
	Country        string
	ASN            string
	HTTP           string
	GRPC           string
	NatsURL        string
	ControllerURL  string
	FetchDir       string
	ServiceName    string
	HeartbeatEvery time.Duration
	ReloadEvery    time.Duration
	LogLevel       slog.Level
}

func Load() (*Config, error) {
	c := &Config{
		Country:       env("WAF_GEO_COUNTRY", "./data/country"),
		ASN:           env("WAF_GEO_ASN", "./data/asn"),
		HTTP:          envKeep("WAF_GEO_HTTP", ":8092"),
		GRPC:          envKeep("WAF_GEO_GRPC", ":50051"),
		NatsURL:       envKeep("WAF_NATS_URL", "nats://127.0.0.1:4222"),
		ControllerURL: strings.TrimRight(envKeep("WAF_GEO_CONTROLLER_URL", ""), "/"),
		FetchDir:      env("WAF_GEO_FETCH_DIR", "./data/fetched"),
		ServiceName:   env("WAF_SERVICE_NAME", "geo"),
	}

	var err error

	if c.LogLevel, err = parseLevel(env("WAF_GEO_LOG", "info")); err != nil {
		return nil, err
	}

	if c.ReloadEvery, err = envDuration("WAF_GEO_RELOAD_EVERY", time.Second); err != nil {
		return nil, err
	}

	if c.HeartbeatEvery, err = envDuration("WAF_HEARTBEAT_EVERY", 4*time.Second); err != nil {
		return nil, err
	}

	return c, c.validate()
}

func (c *Config) validate() error {
	if c.HTTP == "" && c.GRPC == "" {
		return fmt.Errorf("WAF_GEO_HTTP and WAF_GEO_GRPC are both empty")
	}

	for _, pair := range []struct{ name, path string }{
		{"WAF_GEO_COUNTRY", c.Country},
		{"WAF_GEO_ASN", c.ASN},
	} {
		if pair.path == "" {
			return fmt.Errorf("%s is empty", pair.name)
		}

		abs, err := filepath.Abs(pair.path)
		if err != nil {
			return fmt.Errorf("%s: %w", pair.name, err)
		}

		if _, err := os.Stat(abs); err != nil {
			return fmt.Errorf("%s: %w", pair.name, err)
		}

		switch pair.name {
		case "WAF_GEO_COUNTRY":
			c.Country = abs
		case "WAF_GEO_ASN":
			c.ASN = abs
		}
	}

	if c.ControllerURL != "" {
		u, err := url.Parse(c.ControllerURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("WAF_GEO_CONTROLLER_URL: want http(s)://host[:port], got %q", c.ControllerURL)
		}

		abs, err := filepath.Abs(c.FetchDir)
		if err != nil {
			return fmt.Errorf("WAF_GEO_FETCH_DIR: %w", err)
		}

		c.FetchDir = abs
	}

	return nil
}

func env(name, def string) string {
	if v, ok := os.LookupEnv(name); ok && v != "" {
		return v
	}

	return def
}

func envKeep(name, def string) string {
	if v, ok := os.LookupEnv(name); ok {
		return v
	}

	return def
}

func envDuration(name string, def time.Duration) (time.Duration, error) {
	raw, ok := os.LookupEnv(name)
	if !ok || raw == "" {
		return def, nil
	}

	d, err := time.ParseDuration(strings.TrimSpace(raw))
	if err != nil {
		return 0, fmt.Errorf("%s: %w", name, err)
	}

	if d <= 0 {
		return 0, fmt.Errorf("%s must be positive", name)
	}

	return d, nil
}

func parseLevel(s string) (slog.Level, error) {
	level, err := loglevel.Parse(s)
	if err != nil {
		return 0, fmt.Errorf("WAF_GEO_LOG: %w", err)
	}

	return level, nil
}
