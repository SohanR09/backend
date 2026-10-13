-- Schema for the geofencing service. Every statement is idempotent so it is
-- safe to run on each start-up. Statements are separated by semicolons.

CREATE TABLE IF NOT EXISTS geofences (
    seq         BIGSERIAL UNIQUE,
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    category    TEXT NOT NULL
                CHECK (category IN ('delivery_zone', 'restricted_zone', 'toll_zone', 'customer_area')),
    -- Polygon ring as [[lat, lon], ...]; the exact point-in-polygon test runs
    -- on the few candidates returned by the bbox index.
    coordinates JSONB NOT NULL,
    -- Bounding box (x = longitude, y = latitude) used as a GiST-indexed pre-filter.
    bbox        BOX NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_geofences_category ON geofences (category);

CREATE INDEX IF NOT EXISTS idx_geofences_bbox ON geofences USING GIST (bbox);

CREATE TABLE IF NOT EXISTS vehicles (
    seq            BIGSERIAL UNIQUE,
    id             TEXT PRIMARY KEY,
    vehicle_number TEXT NOT NULL UNIQUE,
    driver_name    TEXT NOT NULL,
    vehicle_type   TEXT NOT NULL,
    phone          TEXT NOT NULL,
    status         TEXT NOT NULL DEFAULT 'active',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS vehicle_locations (
    id          BIGSERIAL PRIMARY KEY,
    vehicle_id  TEXT NOT NULL REFERENCES vehicles (id),
    latitude    DOUBLE PRECISION NOT NULL,
    longitude   DOUBLE PRECISION NOT NULL,
    recorded_at TIMESTAMPTZ NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_vehicle_locations_latest
    ON vehicle_locations (vehicle_id, recorded_at DESC);

-- Which geofences each vehicle is currently inside (used to detect entry/exit).
CREATE TABLE IF NOT EXISTS vehicle_geofence_state (
    vehicle_id  TEXT NOT NULL REFERENCES vehicles (id),
    geofence_id TEXT NOT NULL REFERENCES geofences (id),
    PRIMARY KEY (vehicle_id, geofence_id)
);

CREATE TABLE IF NOT EXISTS alert_rules (
    seq         BIGSERIAL UNIQUE,
    id          TEXT PRIMARY KEY,
    geofence_id TEXT NOT NULL REFERENCES geofences (id),
    -- NULL means the rule applies to every vehicle.
    vehicle_id  TEXT REFERENCES vehicles (id),
    event_type  TEXT NOT NULL CHECK (event_type IN ('entry', 'exit', 'both')),
    status      TEXT NOT NULL DEFAULT 'active',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_alert_rules_geofence ON alert_rules (geofence_id);

CREATE INDEX IF NOT EXISTS idx_alert_rules_vehicle ON alert_rules (vehicle_id);

-- Every geofence entry/exit event (the violation history).
CREATE TABLE IF NOT EXISTS violations (
    seq            BIGSERIAL UNIQUE,
    id             TEXT PRIMARY KEY,
    vehicle_id     TEXT NOT NULL REFERENCES vehicles (id),
    vehicle_number TEXT NOT NULL,
    geofence_id    TEXT NOT NULL REFERENCES geofences (id),
    geofence_name  TEXT NOT NULL,
    event_type     TEXT NOT NULL CHECK (event_type IN ('entry', 'exit')),
    latitude       DOUBLE PRECISION NOT NULL,
    longitude      DOUBLE PRECISION NOT NULL,
    recorded_at    TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_violations_time ON violations (recorded_at DESC, seq DESC);

CREATE INDEX IF NOT EXISTS idx_violations_vehicle ON violations (vehicle_id, recorded_at DESC);

CREATE INDEX IF NOT EXISTS idx_violations_geofence ON violations (geofence_id, recorded_at DESC);

-- Alerts that were generated and pushed over WebSocket.
CREATE TABLE IF NOT EXISTS alert_events (
    seq         BIGSERIAL PRIMARY KEY,
    event_id    TEXT NOT NULL UNIQUE,
    event_type  TEXT NOT NULL,
    recorded_at TIMESTAMPTZ NOT NULL,
    payload     JSONB NOT NULL
);
