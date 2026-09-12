/*
 * Конфигурация процесса из переменных окружения.
 *
 * Пути к country и asn проверяются здесь: сервис, который тихо отдаёт
 * пустые списки из-за опечатки в каталоге, хуже не запустившегося.
 * Содержимое этих путей -- уже store, и оно перечитывается на ходу.
 */

package config

import (
	"fmt"
	"log/slog"
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
	ServiceName    string
	HeartbeatEvery time.Duration
	ReloadEvery    time.Duration
	LogLevel       slog.Level
}

func Load() (*Config, error) {
	c := &Config{
		Country:     env("WAF_GEO_COUNTRY", "./data/country"),
		ASN:         env("WAF_GEO_ASN", "./data/asn"),
		HTTP:        envKeep("WAF_GEO_HTTP", ":8092"),
		GRPC:        envKeep("WAF_GEO_GRPC", ":50051"),
		NatsURL:     envKeep("WAF_NATS_URL", "nats://127.0.0.1:4222"),
		ServiceName: env("WAF_SERVICE_NAME", "geo"),
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

/*
 * Стартовый порог журнала: словарь error_log nginx без emerg -- тот же, что у
 * инспекторов и у документа policy/log-levels (shared/logkit). Документ
 * переставляет порог живьём, переменная действует до него.
 */
func parseLevel(s string) (slog.Level, error) {
	level, err := loglevel.Parse(s)
	if err != nil {
		return 0, fmt.Errorf("WAF_GEO_LOG: %w", err)
	}

	return level, nil
}
