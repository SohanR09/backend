package store

import (
	"net"
	"net/url"
	"strings"
)

// DSNFromEnv builds a PostgreSQL connection URL from environment variables.
//
// DATABASE_URL, when set, is used as is. Otherwise the URL is assembled from
// DB_HOST (default "localhost"), DB_PORT ("5432"), DB_USER ("postgres"),
// DB_PASSWORD, DB_NAME ("geofence") and DB_SSLMODE ("disable"). Building the
// URL from parts means passwords with special characters need no manual escaping.
func DSNFromEnv(getenv func(string) string) string {
	if v := strings.TrimSpace(getenv("DATABASE_URL")); v != "" {
		return v
	}
	get := func(key, fallback string) string {
		if v := strings.TrimSpace(getenv(key)); v != "" {
			return v
		}
		return fallback
	}
	u := url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(get("DB_USER", "postgres"), getenv("DB_PASSWORD")),
		Host:     net.JoinHostPort(get("DB_HOST", "localhost"), get("DB_PORT", "5432")),
		Path:     "/" + get("DB_NAME", "geofence"),
		RawQuery: url.Values{"sslmode": {get("DB_SSLMODE", "disable")}}.Encode(),
	}
	return u.String()
}
