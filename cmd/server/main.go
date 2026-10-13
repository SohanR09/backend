// Command server runs the geofencing and vehicle tracking API.
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" database/sql driver

	"geofence/internal/api"
	"geofence/internal/store"
	"geofence/internal/ws"
)

func main() {
	cfg := api.Config{
		APIKey:          os.Getenv("API_KEY"),
		RateLimitPerSec: envFloat("RATE_LIMIT_PER_SEC", 50),
		RateLimitBurst:  envInt("RATE_LIMIT_BURST", 100),
		TrustProxy:      envBool("TRUST_PROXY", false),
		AllowedOrigin:   os.Getenv("ALLOWED_ORIGIN"),
	}

	dsn := store.DSNFromEnv(os.Getenv)
	pg, err := connectPostgres(dsn, envInt("DB_CONNECT_RETRIES", 30), envBool("DB_AUTO_CREATE", true))
	if err != nil {
		log.Fatalf("database: %v", err)
	}
	defer pg.Close()

	hub := ws.NewHub()
	srv, err := api.New(pg, hub, cfg)
	if err != nil {
		log.Fatalf("init: %v", err)
	}

	addr := ":" + envString("PORT", "8080")
	httpServer := &http.Server{
		Addr:              addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	go func() {
		log.Printf("geofence API listening on %s (auth=%t, rate limit=%v/s burst %d)",
			addr, cfg.APIKey != "", cfg.RateLimitPerSec, cfg.RateLimitBurst)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	log.Println("shutting down...")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(ctx); err != nil {
		log.Printf("shutdown: %v", err)
	}
	srv.Close()
}

// connectPostgres opens the connection pool, waits for the database server to
// accept connections (it may still be starting) and applies the schema. When
// autoCreate is set and the database itself does not exist yet, it is created
// first (this needs a role with CREATEDB, such as the default "postgres").
func connectPostgres(dsn string, attempts int, autoCreate bool) (*store.Postgres, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("open: %w", err)
	}
	db.SetMaxOpenConns(20)
	db.SetMaxIdleConns(10)
	db.SetConnMaxLifetime(30 * time.Minute)

	target := redactDSN(dsn)
	var lastErr error
	for i := 1; i <= max(attempts, 1); i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		pg, err := store.NewPostgres(ctx, db) // pings implicitly and migrates
		cancel()
		if err == nil {
			log.Printf("connected to PostgreSQL at %s", target)
			return pg, nil
		}
		lastErr = err
		if autoCreate && isMissingDatabase(err) {
			log.Printf("database does not exist yet, creating it ...")
			if cerr := createDatabase(dsn); cerr != nil {
				_ = db.Close()
				return nil, fmt.Errorf("database does not exist and could not be created (create it manually, or use a role with CREATEDB): %w", cerr)
			}
			continue // retry immediately now that it exists
		}
		log.Printf("waiting for PostgreSQL at %s (attempt %d/%d): %v", target, i, attempts, err)
		time.Sleep(2 * time.Second)
	}
	_ = db.Close()
	return nil, fmt.Errorf("could not connect to %s: %w", target, lastErr)
}

// redactDSN hides the password so it never reaches the logs.
func redactDSN(dsn string) string {
	u, err := url.Parse(dsn)
	if err != nil {
		return "<unparseable DSN>"
	}
	return u.Redacted()
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
