// Package config reads the service's settings from the environment.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	// Env is "development", "staging" or "production". It gates exactly one behaviour:
	// in development the login code is written to the log instead of being mailed.
	Env string

	// Version identifies the build. Reported by /v1/health.
	Version string

	Addr string

	DatabaseURL string
	RedisURL    string

	// AuthSecret peppers login-code hashes and signs access tokens. A leaked database
	// without this secret yields no usable credential (ADR-002).
	AuthSecret string

	AccessTokenTTL  time.Duration
	RefreshTokenTTL time.Duration
	LoginCodeTTL    time.Duration

	// MaxCodeAttempts is how many wrong codes a challenge tolerates before it is spent.
	MaxCodeAttempts int
	// MaxCodesPerHour caps how many codes one address can be sent in an hour.
	MaxCodesPerHour int
	// ResendAfter is how long the client should wait before offering "send it again".
	ResendAfter time.Duration
}

func (c Config) IsDevelopment() bool { return c.Env == "development" }

// Load reads configuration, applying defaults that are safe for local development and
// refusing to start without the things that have no safe default.
func Load() (Config, error) {
	c := Config{
		Env:             env("POD_ENV", "development"),
		Version:         env("POD_VERSION", "dev"),
		Addr:            env("POD_ADDR", ":8080"),
		DatabaseURL:     os.Getenv("DATABASE_URL"),
		RedisURL:        os.Getenv("REDIS_URL"),
		AuthSecret:      os.Getenv("POD_AUTH_SECRET"),
		MaxCodeAttempts: 5,
		MaxCodesPerHour: 5,
		ResendAfter:     30 * time.Second,
	}

	var err error
	if c.AccessTokenTTL, err = duration("POD_ACCESS_TOKEN_TTL", 15*time.Minute); err != nil {
		return c, err
	}
	if c.RefreshTokenTTL, err = duration("POD_REFRESH_TOKEN_TTL", 30*24*time.Hour); err != nil {
		return c, err
	}
	if c.LoginCodeTTL, err = duration("POD_LOGIN_CODE_TTL", 10*time.Minute); err != nil {
		return c, err
	}
	if n, err := integer("POD_MAX_CODE_ATTEMPTS", c.MaxCodeAttempts); err == nil {
		c.MaxCodeAttempts = n
	} else {
		return c, err
	}

	var problems []string
	if c.DatabaseURL == "" {
		problems = append(problems, "DATABASE_URL is required")
	}
	switch {
	case c.AuthSecret == "" && c.IsDevelopment():
		// Deterministic so that restarting the dev server doesn't sign everyone out.
		c.AuthSecret = "development-only-not-a-secret"
	case c.AuthSecret == "":
		problems = append(problems, "POD_AUTH_SECRET is required outside development")
	case len(c.AuthSecret) < 32:
		problems = append(problems, "POD_AUTH_SECRET must be at least 32 bytes")
	}
	if len(problems) > 0 {
		return c, fmt.Errorf("config: %s", strings.Join(problems, "; "))
	}
	return c, nil
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func duration(key string, fallback time.Duration) (time.Duration, error) {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("config: %s: %w", key, err)
	}
	if d <= 0 {
		return 0, errors.New("config: " + key + " must be positive")
	}
	return d, nil
}

func integer(key string, fallback int) (int, error) {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("config: %s: %w", key, err)
	}
	return n, nil
}
