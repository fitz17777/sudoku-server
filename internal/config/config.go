package config

import (
	"fmt"
	"os"
	"strconv"
)

type Config struct {
	Port            string
	BaseURL         string
	RedisAddr       string
	RedisPassword   string
	OIDCIssuer      string
	OIDCClientID    string
	OIDCClientSecret string
	OIDCRedirectURL string
	SessionSecret   string
	PuzzleRegenerate bool
}

func Load() (*Config, error) {
	c := &Config{
		Port:             getEnv("PORT", "8080"),
		BaseURL:          getEnv("BASE_URL", "http://localhost:8080"),
		RedisAddr:        getEnv("REDIS_ADDR", "localhost:6379"),
		RedisPassword:    getEnv("REDIS_PASSWORD", ""),
		OIDCIssuer:       getEnv("OIDC_ISSUER", ""),
		OIDCClientID:     getEnv("OIDC_CLIENT_ID", ""),
		OIDCClientSecret: getEnv("OIDC_CLIENT_SECRET", ""),
		OIDCRedirectURL:  getEnv("OIDC_REDIRECT_URL", ""),
		SessionSecret:    getEnv("SESSION_SECRET", ""),
		PuzzleRegenerate: getBoolEnv("PUZZLE_REGENERATE", false),
	}

	var missing []string
	if c.OIDCIssuer == "" {
		missing = append(missing, "OIDC_ISSUER")
	}
	if c.OIDCClientID == "" {
		missing = append(missing, "OIDC_CLIENT_ID")
	}
	if c.OIDCClientSecret == "" {
		missing = append(missing, "OIDC_CLIENT_SECRET")
	}
	if c.OIDCRedirectURL == "" {
		c.OIDCRedirectURL = c.BaseURL + "/callback"
	}
	if c.SessionSecret == "" {
		missing = append(missing, "SESSION_SECRET")
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("missing required environment variables: %v", missing)
	}
	return c, nil
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getBoolEnv(key string, fallback bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return fallback
	}
	return b
}
