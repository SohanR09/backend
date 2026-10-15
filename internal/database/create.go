package database

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// isMissingDatabase reports whether err is PostgreSQL's "database does not
// exist" error (SQLSTATE 3D000).
func isMissingDatabase(err error) bool {
	return err != nil && strings.Contains(err.Error(), "3D000")
}

// quoteIdent quotes a PostgreSQL identifier.
func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// maintenanceDSN returns the DSN with the database switched to "postgres",
// plus the name of the database the original DSN pointed at.
func maintenanceDSN(dsn string) (maintenance, dbName string, err error) {
	u, err := url.Parse(dsn)
	if err != nil {
		return "", "", err
	}
	if u.Scheme != "postgres" && u.Scheme != "postgresql" {
		return "", "", fmt.Errorf("unsupported DSN scheme %q", u.Scheme)
	}
	dbName = strings.TrimPrefix(u.Path, "/")
	if dbName == "" {
		return "", "", fmt.Errorf("DSN has no database name")
	}
	u.Path = "/postgres"
	return u.String(), dbName, nil
}

// createDatabase creates the database named in dsn by connecting to the
// "postgres" maintenance database. It needs a role with CREATEDB.
func createDatabase(dsn string) error {
	maintenance, dbName, err := maintenanceDSN(dsn)
	if err != nil {
		return err
	}
	db, err := sql.Open("pgx", maintenance)
	if err != nil {
		return err
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_, err = db.ExecContext(ctx, "CREATE DATABASE "+quoteIdent(dbName))
	return err
}
