// Package config loads the server's runtime settings from environment
// variables.
package config

import (
	"log"
	"os"
	"strconv"
)

// Config holds every setting read from the environment.
type Config struct {
	// Port is the TCP port the HTTP server listens on (PORT, default 8080).
	Port string

	// DSN is the PostgreSQL connection URL (see DSNFromEnv).
	DSN string
	// DBConnectRetries is how many times to try reaching the database at
	// startup (DB_CONNECT_RETRIES, default 30).
	DBConnectRetries int
	// DBAutoCreate creates the database on first start when it does not
	// exist (DB_AUTO_CREATE, default true).
	DBAutoCreate bool

	// APIKey, when non-empty, is required on every request (API_KEY).
	APIKey string
	// RateLimitPerSec / RateLimitBurst throttle location updates
	// (RATE_LIMIT_PER_SEC default 50, RATE_LIMIT_BURST default 100).
	RateLimitPerSec float64
	RateLimitBurst  int
	// TrustProxy makes the rate limiter key on X-Forwarded-For (TRUST_PROXY).
	TrustProxy bool
	// AllowedOrigin is the CORS origin (ALLOWED_ORIGIN, "*" when empty).
	AllowedOrigin string
}

// Load reads the configuration from the process environment.
func Load() Config {
	return Config{
		Port:             envString("PORT", "8080"),
		DSN:              DSNFromEnv(os.Getenv),
		DBConnectRetries: envInt("DB_CONNECT_RETRIES", 30),
		DBAutoCreate:     envBool("DB_AUTO_CREATE", true),
		APIKey:           os.Getenv("API_KEY"),
		RateLimitPerSec:  envFloat("RATE_LIMIT_PER_SEC", 50),
		RateLimitBurst:   envInt("RATE_LIMIT_BURST", 100),
		TrustProxy:       envBool("TRUST_PROXY", false),
		AllowedOrigin:    os.Getenv("ALLOWED_ORIGIN"),
	}
}

func envString(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
		log.Printf("ignoring invalid %s=%q", key, v)
	}
	return fallback
}

func envFloat(key string, fallback float64) float64 {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.ParseFloat(v, 64); err == nil {
			return n
		}
		log.Printf("ignoring invalid %s=%q", key, v)
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	if v := os.Getenv(key); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
		log.Printf("ignoring invalid %s=%q", key, v)
	}
	return fallback
}
