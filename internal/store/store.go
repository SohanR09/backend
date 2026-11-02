// Package store defines the persistence boundary of the backend. The API
// layer depends only on the Store interface; Postgres is the production
// implementation and Memory is a lightweight implementation used by tests.
package store

import (
	"errors"

	"geofence/internal/model"
)

// Sentinel errors returned by Store implementations.
var (
	ErrNotFound  = errors.New("not found")
	ErrDuplicate = errors.New("already exists")
)

// AlertRuleFilter narrows an alert rule listing.
type AlertRuleFilter struct {
	GeofenceID string
	VehicleID  string
}

// Store is the persistence interface used by the API layer.
type Store interface {
	CreateGeofence(g model.Geofence) error
	// ListGeofences returns geofences in creation order; an empty category
	// returns all of them.
	ListGeofences(category string) ([]model.Geofence, error)
	GetGeofence(id string) (model.Geofence, error)
	// GeofencesContaining returns every geofence whose polygon contains the
	// point (boundary included), in creation order.
	GeofencesContaining(lat, lon float64) ([]model.Geofence, error)

	// CreateVehicle returns ErrDuplicate if the vehicle number is taken.
	CreateVehicle(v model.Vehicle) error
	ListVehicles() ([]model.Vehicle, error)
	GetVehicle(id string) (model.Vehicle, error)

	// ApplyLocation records the location and, if it is the newest known
	// position of the vehicle, atomically replaces the set of geofences the
	// vehicle is inside with insideIDs. It returns whether the location was
	// the newest and the geofences the vehicle was inside before the call
	// (for an older, out-of-order location that is simply the current state,
	// which is left unchanged). Returns ErrNotFound for an unknown vehicle.
	ApplyLocation(loc model.Location, insideIDs []string) (isLatest bool, previous []model.Geofence, err error)
	LatestLocation(vehicleID string) (model.Location, error)
	// GeofenceState returns the geofences the vehicle is currently inside.
	GeofenceState(vehicleID string) ([]model.Geofence, error)

	CreateAlertRule(r model.AlertRule) error
	// ListAlertRules returns rules in creation order with GeofenceName and
	// VehicleNumber filled in.
	ListAlertRules(f AlertRuleFilter) ([]model.AlertRule, error)
	DeleteAlertRule(id string) error
	// HasActiveAlertRule reports whether an active rule asks for eventType
	// ("entry" or "exit") of this vehicle in this geofence.
	HasActiveAlertRule(vehicleID, geofenceID, eventType string) (bool, error)

	AddViolation(v model.Violation) error
	// ListViolations returns matching violations newest first plus the total
	// number of matches ignoring Limit/Offset.
	ListViolations(f model.ViolationFilter) ([]model.Violation, int, error)

	// AddAlertEvent persists a delivered alert; ListAlertEvents returns the
	// most recent ones, newest first.
	AddAlertEvent(e model.AlertEvent) error
	ListAlertEvents(limit int) ([]model.AlertEvent, error)

	LatestLocations() (map[string]model.Location, error)
	GeofenceStates() (map[string][]model.Geofence, error)
}
