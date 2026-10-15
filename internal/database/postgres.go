// Package database opens the PostgreSQL connection pool for the server,
// waits for the database to become reachable and creates it on first start.
package database

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"net/url"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" database/sql driver

	"geofence/internal/store"
)

// ConnectPostgres opens the connection pool, waits for the database server to
// accept connections (it may still be starting) and applies the schema. When
// autoCreate is set and the database itself does not exist yet, it is created
// first (this needs a role with CREATEDB, such as the default "postgres").
func ConnectPostgres(dsn string, attempts int, autoCreate bool) (*store.Postgres, error) {
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
