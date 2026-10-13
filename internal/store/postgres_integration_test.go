//go:build integration

package store

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"geofence/internal/model"
)

// Run with a reachable database, for example:
//
//	TEST_DATABASE_URL="postgres://geofence:YOUR_PASSWORD@localhost:5432/geofence_test?sslmode=disable" \
//	  go test -tags integration ./internal/store/
//
// WARNING: every table of the target database is truncated before each test.
func TestPostgresContract(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	runContract(t, func(t *testing.T) Store {
		t.Helper()
		db, err := sql.Open("pgx", dsn)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Close() })

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		pg, err := NewPostgres(ctx, db)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, `TRUNCATE alert_events, violations, alert_rules,
			vehicle_geofence_state, vehicle_locations, vehicles, geofences RESTART IDENTITY CASCADE`); err != nil {
			t.Fatal(err)
		}
		return pg
	})
}

// TestPostgresMigrateIsIdempotent applies the schema twice, as happens on
// every restart of the server.
func TestPostgresMigrateIsIdempotent(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for i := 0; i < 2; i++ {
		if _, err := NewPostgres(context.Background(), db); err != nil {
			t.Fatalf("migrate run %d: %v", i+1, err)
		}
	}
}

// TestPostgresConcurrentLocationUpdates fires many simultaneous updates for
// one vehicle with different timestamps. The row lock must serialise them so
// the final state always matches the update with the newest timestamp, no
// matter in which order the transactions ran.
func TestPostgresConcurrentLocationUpdates(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(10)

	ctx := context.Background()
	pg, err := NewPostgres(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `TRUNCATE alert_events, violations, alert_rules,
		vehicle_geofence_state, vehicle_locations, vehicles, geofences RESTART IDENTITY CASCADE`); err != nil {
		t.Fatal(err)
	}
	mustNoErr(t, pg.CreateGeofence(geofence("geo_a", "restricted_zone", squareRing, t0)))
	mustNoErr(t, pg.CreateVehicle(vehicle("veh_1", "KA-01")))

	const n = 40
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		go func(i int) {
			inside := []string(nil)
			if i%2 == 0 {
				inside = []string{"geo_a"}
			}
			_, _, err := pg.ApplyLocation(model.Location{
				VehicleID: "veh_1", Latitude: float64(i), Longitude: 0, Timestamp: t0.Add(time.Duration(i) * time.Minute),
			}, inside)
			errs <- err
		}(i)
	}
	for i := 0; i < n; i++ {
		if err := <-errs; err != nil {
			t.Fatalf("concurrent ApplyLocation: %v", err)
		}
	}

	loc, err := pg.LatestLocation("veh_1")
	mustNoErr(t, err)
	if loc.Latitude != n-1 {
		t.Fatalf("latest latitude = %v, want %d", loc.Latitude, n-1)
	}
	state, err := pg.GeofenceState("veh_1")
	mustNoErr(t, err)
	if len(state) != 0 { // newest update (i = 39, odd) is outside every geofence
		t.Fatalf("final state = %v, want empty", ids(state))
	}
}
