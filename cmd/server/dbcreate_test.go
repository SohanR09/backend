package main

import (
	"errors"
	"testing"
)

func TestMaintenanceDSN(t *testing.T) {
	m, name, err := maintenanceDSN("postgres://postgres:pw@localhost:5433/geofence?sslmode=disable")
	if err != nil || name != "geofence" || m != "postgres://postgres:pw@localhost:5433/postgres?sslmode=disable" {
		t.Fatalf("got %q %q %v", m, name, err)
	}
	if _, _, err := maintenanceDSN("postgres://u:p@h:1"); err == nil {
		t.Fatal("expected error for missing database name")
	}
	if _, _, err := maintenanceDSN("mysql://u:p@h/db"); err == nil {
		t.Fatal("expected error for non-postgres scheme")
	}
}

func TestQuoteIdent(t *testing.T) {
	if got := quoteIdent(`my"db`); got != `"my""db"` {
		t.Fatalf("got %s", got)
	}
}

func TestIsMissingDatabase(t *testing.T) {
	if !isMissingDatabase(errors.New(`FATAL: database "x" does not exist (SQLSTATE 3D000)`)) {
		t.Fatal("should match 3D000")
	}
	if isMissingDatabase(errors.New("connection refused")) || isMissingDatabase(nil) {
		t.Fatal("should not match other errors")
	}
}
