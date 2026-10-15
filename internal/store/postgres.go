package store

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"geofence/internal/geo"
	"geofence/internal/model"
)

//go:embed migrations/schema.sql
var schemaSQL string

// queryTimeout bounds every database call made on behalf of a request.
const queryTimeout = 10 * time.Second

// Postgres is the PostgreSQL-backed Store. It works with any database/sql
// driver for PostgreSQL (the server uses pgx's stdlib driver).
type Postgres struct {
	db *sql.DB
}

var _ Store = (*Postgres)(nil)

// NewPostgres wraps an open *sql.DB and creates the schema if needed.
func NewPostgres(ctx context.Context, db *sql.DB) (*Postgres, error) {
	p := &Postgres{db: db}
	if err := p.migrate(ctx); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return p, nil
}

// Close releases the connection pool.
func (p *Postgres) Close() error { return p.db.Close() }

// Ping checks that the database is reachable.
func (p *Postgres) Ping(ctx context.Context) error { return p.db.PingContext(ctx) }

// migrate applies schema.sql inside one transaction guarded by an advisory
// lock, so several instances starting together do not race.
func (p *Postgres) migrate(ctx context.Context) error {
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(727274)`); err != nil {
		return err
	}
	for _, stmt := range splitStatements(schemaSQL) {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("%w (statement: %.60q)", err, stmt)
		}
	}
	return tx.Commit()
}

// splitStatements splits a SQL script into statements. Full-line comments are
// removed first (so semicolons inside comments are harmless), then the script
// is split on semicolons. The schema contains no semicolons inside literals.
func splitStatements(script string) []string {
	var kept []string
	for _, line := range strings.Split(script, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "--") {
			continue
		}
		kept = append(kept, line)
	}
	var out []string
	for _, part := range strings.Split(strings.Join(kept, "\n"), ";") {
		if stmt := strings.TrimSpace(part); stmt != "" {
			out = append(out, stmt)
		}
	}
	return out
}

func newCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), queryTimeout)
}

// scanner is satisfied by *sql.Row and *sql.Rows.
type scanner interface{ Scan(dest ...any) error }

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// ---- geofences ----

const geofenceColumns = `id, name, description, category, coordinates, created_at`

func scanGeofence(s scanner) (model.Geofence, error) {
	var (
		g      model.Geofence
		coords []byte
	)
	if err := s.Scan(&g.ID, &g.Name, &g.Description, &g.Category, &coords, &g.CreatedAt); err != nil {
		return model.Geofence{}, err
	}
	if err := json.Unmarshal(coords, &g.Coordinates); err != nil {
		return model.Geofence{}, fmt.Errorf("decode coordinates of %s: %w", g.ID, err)
	}
	g.CreatedAt = g.CreatedAt.UTC()
	return g, nil
}

func scanGeofences(rows *sql.Rows) ([]model.Geofence, error) {
	defer rows.Close()
	out := make([]model.Geofence, 0)
	for rows.Next() {
		g, err := scanGeofence(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func (p *Postgres) CreateGeofence(g model.Geofence) error {
	ctx, cancel := newCtx()
	defer cancel()

	coords, err := json.Marshal(g.Coordinates)
	if err != nil {
		return err
	}
	b := geo.BoundingBox(g.Coordinates)
	_, err = p.db.ExecContext(ctx, `
		INSERT INTO geofences (id, name, description, category, coordinates, bbox, created_at)
		VALUES ($1, $2, $3, $4, $5::jsonb,
		        box(point($6::float8, $7::float8), point($8::float8, $9::float8)), $10)`,
		g.ID, g.Name, g.Description, g.Category, string(coords),
		b.MinLon, b.MinLat, b.MaxLon, b.MaxLat, g.CreatedAt)
	return err
}

func (p *Postgres) ListGeofences(category string) ([]model.Geofence, error) {
	ctx, cancel := newCtx()
	defer cancel()

	rows, err := p.db.QueryContext(ctx,
		`SELECT `+geofenceColumns+` FROM geofences WHERE ($1 = '' OR category = $1) ORDER BY seq`, category)
	if err != nil {
		return nil, err
	}
	return scanGeofences(rows)
}

func (p *Postgres) GetGeofence(id string) (model.Geofence, error) {
	ctx, cancel := newCtx()
	defer cancel()

	g, err := scanGeofence(p.db.QueryRowContext(ctx,
		`SELECT `+geofenceColumns+` FROM geofences WHERE id = $1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return model.Geofence{}, ErrNotFound
	}
	return g, err
}

// GeofencesContaining uses the GiST index on the bounding box to find
// candidates, then applies the exact point-in-polygon test to each. The point
// is expressed as a degenerate box because the box GiST operator class only
// indexes box-vs-box containment (a plain "box @> point" cannot use the index).
func (p *Postgres) GeofencesContaining(lat, lon float64) ([]model.Geofence, error) {
	ctx, cancel := newCtx()
	defer cancel()

	rows, err := p.db.QueryContext(ctx,
		`SELECT `+geofenceColumns+` FROM geofences
		 WHERE bbox @> box(point($1::float8, $2::float8), point($1::float8, $2::float8)) ORDER BY seq`,
		lon, lat)
	if err != nil {
		return nil, err
	}
	candidates, err := scanGeofences(rows)
	if err != nil {
		return nil, err
	}
	out := candidates[:0]
	for _, g := range candidates {
		if geo.PointInPolygon(lat, lon, g.Coordinates) {
			out = append(out, g)
		}
	}
	return out, nil
}

// ---- vehicles ----

const vehicleColumns = `id, vehicle_number, driver_name, vehicle_type, phone, status, created_at`

func scanVehicle(s scanner) (model.Vehicle, error) {
	var v model.Vehicle
	if err := s.Scan(&v.ID, &v.VehicleNumber, &v.DriverName, &v.VehicleType, &v.Phone, &v.Status, &v.CreatedAt); err != nil {
		return model.Vehicle{}, err
	}
	v.CreatedAt = v.CreatedAt.UTC()
	return v, nil
}

func (p *Postgres) CreateVehicle(v model.Vehicle) error {
	ctx, cancel := newCtx()
	defer cancel()

	res, err := p.db.ExecContext(ctx, `
		INSERT INTO vehicles (id, vehicle_number, driver_name, vehicle_type, phone, status, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (vehicle_number) DO NOTHING`,
		v.ID, v.VehicleNumber, v.DriverName, v.VehicleType, v.Phone, v.Status, v.CreatedAt)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return ErrDuplicate
	}
	return nil
}

func (p *Postgres) ListVehicles() ([]model.Vehicle, error) {
	ctx, cancel := newCtx()
	defer cancel()

	rows, err := p.db.QueryContext(ctx, `SELECT `+vehicleColumns+` FROM vehicles ORDER BY seq`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]model.Vehicle, 0)
	for rows.Next() {
		v, err := scanVehicle(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (p *Postgres) GetVehicle(id string) (model.Vehicle, error) {
	ctx, cancel := newCtx()
	defer cancel()

	v, err := scanVehicle(p.db.QueryRowContext(ctx,
		`SELECT `+vehicleColumns+` FROM vehicles WHERE id = $1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return model.Vehicle{}, ErrNotFound
	}
	return v, err
}

// ---- locations and geofence state ----

const stateQuery = `
	SELECT g.id, g.name, g.description, g.category, g.coordinates, g.created_at
	FROM vehicle_geofence_state s
	JOIN geofences g ON g.id = s.geofence_id
	WHERE s.vehicle_id = $1
	ORDER BY g.seq`

// ApplyLocation does everything that must be atomic per vehicle in a single
// transaction: it locks the vehicle row (serialising concurrent updates for
// the same vehicle), stores the location, and swaps the geofence state only
// when the location is the newest one.
func (p *Postgres) ApplyLocation(loc model.Location, insideIDs []string) (bool, []model.Geofence, error) {
	ctx, cancel := newCtx()
	defer cancel()

	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return false, nil, err
	}
	defer func() { _ = tx.Rollback() }()

	var lockedID string
	err = tx.QueryRowContext(ctx, `SELECT id FROM vehicles WHERE id = $1 FOR UPDATE`, loc.VehicleID).Scan(&lockedID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil, ErrNotFound
	}
	if err != nil {
		return false, nil, err
	}

	var newest sql.NullTime
	if err := tx.QueryRowContext(ctx,
		`SELECT max(recorded_at) FROM vehicle_locations WHERE vehicle_id = $1`, loc.VehicleID).Scan(&newest); err != nil {
		return false, nil, err
	}
	isLatest := !newest.Valid || !loc.Timestamp.Before(newest.Time)

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO vehicle_locations (vehicle_id, latitude, longitude, recorded_at) VALUES ($1, $2, $3, $4)`,
		loc.VehicleID, loc.Latitude, loc.Longitude, loc.Timestamp); err != nil {
		return false, nil, err
	}

	rows, err := tx.QueryContext(ctx, stateQuery, loc.VehicleID)
	if err != nil {
		return false, nil, err
	}
	previous, err := scanGeofences(rows)
	if err != nil {
		return false, nil, err
	}

	if isLatest {
		if _, err := tx.ExecContext(ctx, `DELETE FROM vehicle_geofence_state WHERE vehicle_id = $1`, loc.VehicleID); err != nil {
			return false, nil, err
		}
		for _, id := range insideIDs {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO vehicle_geofence_state (vehicle_id, geofence_id) VALUES ($1, $2)`,
				loc.VehicleID, id); err != nil {
				return false, nil, err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return false, nil, err
	}
	return isLatest, previous, nil
}

func (p *Postgres) LatestLocation(vehicleID string) (model.Location, error) {
	ctx, cancel := newCtx()
	defer cancel()

	loc := model.Location{VehicleID: vehicleID}
	err := p.db.QueryRowContext(ctx, `
		SELECT latitude, longitude, recorded_at
		FROM vehicle_locations WHERE vehicle_id = $1
		ORDER BY recorded_at DESC, id DESC LIMIT 1`, vehicleID).
		Scan(&loc.Latitude, &loc.Longitude, &loc.Timestamp)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Location{}, ErrNotFound
	}
	loc.Timestamp = loc.Timestamp.UTC()
	return loc, err
}

func (p *Postgres) GeofenceState(vehicleID string) ([]model.Geofence, error) {
	ctx, cancel := newCtx()
	defer cancel()

	rows, err := p.db.QueryContext(ctx, stateQuery, vehicleID)
	if err != nil {
		return nil, err
	}
	return scanGeofences(rows)
}

// ---- alert rules ----

func (p *Postgres) CreateAlertRule(r model.AlertRule) error {
	ctx, cancel := newCtx()
	defer cancel()

	_, err := p.db.ExecContext(ctx, `
		INSERT INTO alert_rules (id, geofence_id, vehicle_id, event_type, status, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		r.ID, r.GeofenceID, nullIfEmpty(r.VehicleID), r.EventType, r.Status, r.CreatedAt)
	return err
}

func (p *Postgres) ListAlertRules(f AlertRuleFilter) ([]model.AlertRule, error) {
	ctx, cancel := newCtx()
	defer cancel()

	rows, err := p.db.QueryContext(ctx, `
		SELECT r.id, r.geofence_id, g.name, r.vehicle_id, COALESCE(v.vehicle_number, ''),
		       r.event_type, r.status, r.created_at
		FROM alert_rules r
		JOIN geofences g ON g.id = r.geofence_id
		LEFT JOIN vehicles v ON v.id = r.vehicle_id
		WHERE ($1 = '' OR r.geofence_id = $1) AND ($2 = '' OR r.vehicle_id = $2)
		ORDER BY r.seq`, f.GeofenceID, f.VehicleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]model.AlertRule, 0)
	for rows.Next() {
		var (
			r         model.AlertRule
			vehicleID sql.NullString
		)
		if err := rows.Scan(&r.ID, &r.GeofenceID, &r.GeofenceName, &vehicleID, &r.VehicleNumber,
			&r.EventType, &r.Status, &r.CreatedAt); err != nil {
			return nil, err
		}
		r.VehicleID = vehicleID.String
		r.CreatedAt = r.CreatedAt.UTC()
		out = append(out, r)
	}
	return out, rows.Err()
}

func (p *Postgres) DeleteAlertRule(id string) error {
	ctx, cancel := newCtx()
	defer cancel()

	res, err := p.db.ExecContext(ctx, `DELETE FROM alert_rules WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return ErrNotFound
	}
	return nil
}

func (p *Postgres) HasActiveAlertRule(vehicleID, geofenceID, eventType string) (bool, error) {
	ctx, cancel := newCtx()
	defer cancel()

	var found bool
	err := p.db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM alert_rules
			WHERE geofence_id = $1 AND status = 'active'
			  AND (vehicle_id IS NULL OR vehicle_id = $2)
			  AND event_type IN ('both', $3)
		)`, geofenceID, vehicleID, eventType).Scan(&found)
	return found, err
}

// ---- violations ----

func (p *Postgres) AddViolation(v model.Violation) error {
	ctx, cancel := newCtx()
	defer cancel()

	_, err := p.db.ExecContext(ctx, `
		INSERT INTO violations (id, vehicle_id, vehicle_number, geofence_id, geofence_name,
		                        event_type, latitude, longitude, recorded_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		v.ID, v.VehicleID, v.VehicleNumber, v.GeofenceID, v.GeofenceName,
		v.EventType, v.Latitude, v.Longitude, v.Timestamp)
	return err
}

func (p *Postgres) ListViolations(f model.ViolationFilter) ([]model.Violation, int, error) {
	ctx, cancel := newCtx()
	defer cancel()

	var (
		conds []string
		args  []any
	)
	add := func(cond string, v any) {
		args = append(args, v)
		conds = append(conds, fmt.Sprintf(cond, len(args)))
	}
	if f.VehicleID != "" {
		add("vehicle_id = $%d", f.VehicleID)
	}
	if f.GeofenceID != "" {
		add("geofence_id = $%d", f.GeofenceID)
	}
	if f.Start != nil {
		add("recorded_at >= $%d", *f.Start)
	}
	if f.End != nil {
		add("recorded_at <= $%d", *f.End)
	}
	where := ""
	if len(conds) > 0 {
		where = " WHERE " + strings.Join(conds, " AND ")
	}

	var total int
	if err := p.db.QueryRowContext(ctx, `SELECT count(*) FROM violations`+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	query := `SELECT id, vehicle_id, vehicle_number, geofence_id, geofence_name,
	                 event_type, latitude, longitude, recorded_at
	          FROM violations` + where + ` ORDER BY recorded_at DESC, seq DESC`
	if f.Limit > 0 {
		args = append(args, f.Limit)
		query += fmt.Sprintf(" LIMIT $%d", len(args))
	}
	if f.Offset > 0 {
		args = append(args, f.Offset)
		query += fmt.Sprintf(" OFFSET $%d", len(args))
	}

	rows, err := p.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	out := make([]model.Violation, 0)
	for rows.Next() {
		var v model.Violation
		if err := rows.Scan(&v.ID, &v.VehicleID, &v.VehicleNumber, &v.GeofenceID, &v.GeofenceName,
			&v.EventType, &v.Latitude, &v.Longitude, &v.Timestamp); err != nil {
			return nil, 0, err
		}
		v.Timestamp = v.Timestamp.UTC()
		out = append(out, v)
	}
	return out, total, rows.Err()
}

// ---- delivered alerts ----

func (p *Postgres) AddAlertEvent(e model.AlertEvent) error {
	ctx, cancel := newCtx()
	defer cancel()

	payload, err := json.Marshal(e)
	if err != nil {
		return err
	}
	_, err = p.db.ExecContext(ctx, `
		INSERT INTO alert_events (event_id, event_type, recorded_at, payload)
		VALUES ($1, $2, $3, $4::jsonb)`,
		e.EventID, e.EventType, e.Timestamp, string(payload))
	return err
}

func (p *Postgres) ListAlertEvents(limit int) ([]model.AlertEvent, error) {
	ctx, cancel := newCtx()
	defer cancel()

	if limit <= 0 {
		limit = 500
	}
	rows, err := p.db.QueryContext(ctx, `SELECT payload FROM alert_events ORDER BY seq DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]model.AlertEvent, 0)
	for rows.Next() {
		var payload []byte
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var e model.AlertEvent
		if err := json.Unmarshal(payload, &e); err != nil {
			return nil, fmt.Errorf("decode alert event: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
