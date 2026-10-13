// Package model holds the domain types shared across the backend.
package model

import "time"

// Geofence categories accepted by the API.
const (
	CategoryDelivery   = "delivery_zone"
	CategoryRestricted = "restricted_zone"
	CategoryToll       = "toll_zone"
	CategoryCustomer   = "customer_area"
)

// Alert rule event types.
const (
	EventEntry = "entry"
	EventExit  = "exit"
	EventBoth  = "both"
)

// ValidCategory reports whether c is a supported geofence category.
func ValidCategory(c string) bool {
	switch c {
	case CategoryDelivery, CategoryRestricted, CategoryToll, CategoryCustomer:
		return true
	}
	return false
}

// ValidEventType reports whether e is a supported alert rule event type.
func ValidEventType(e string) bool {
	return e == EventEntry || e == EventExit || e == EventBoth
}

// Geofence is a polygonal region. Coordinates are [latitude, longitude] pairs
// and the ring is closed (first point equals last point).
type Geofence struct {
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	Description string       `json:"description"`
	Coordinates [][2]float64 `json:"coordinates"`
	Category    string       `json:"category"`
	CreatedAt   time.Time    `json:"created_at"`
}

// Vehicle is a tracked vehicle.
type Vehicle struct {
	ID            string    `json:"id"`
	VehicleNumber string    `json:"vehicle_number"`
	DriverName    string    `json:"driver_name"`
	VehicleType   string    `json:"vehicle_type"`
	Phone         string    `json:"phone"`
	Status        string    `json:"status"`
	CreatedAt     time.Time `json:"created_at"`
}

// Location is a recorded vehicle position.
type Location struct {
	VehicleID string    `json:"vehicle_id"`
	Latitude  float64   `json:"latitude"`
	Longitude float64   `json:"longitude"`
	Timestamp time.Time `json:"timestamp"`
}

// AlertRule configures which geofence events raise an alert.
// An empty VehicleID means the rule applies to every vehicle.
type AlertRule struct {
	ID         string    `json:"alert_id"`
	GeofenceID string    `json:"geofence_id"`
	VehicleID  string    `json:"vehicle_id,omitempty"`
	EventType  string    `json:"event_type"`
	Status     string    `json:"status"`
	CreatedAt  time.Time `json:"created_at"`

	// Filled in by Store.ListAlertRules from the referenced records so list
	// responses need no extra lookups.
	GeofenceName  string `json:"-"`
	VehicleNumber string `json:"-"`
}

// Violation is a recorded geofence entry/exit event.
type Violation struct {
	ID            string    `json:"id"`
	VehicleID     string    `json:"vehicle_id"`
	VehicleNumber string    `json:"vehicle_number"`
	GeofenceID    string    `json:"geofence_id"`
	GeofenceName  string    `json:"geofence_name"`
	EventType     string    `json:"event_type"`
	Latitude      float64   `json:"latitude"`
	Longitude     float64   `json:"longitude"`
	Timestamp     time.Time `json:"timestamp"`
}

// ViolationFilter narrows a history query.
type ViolationFilter struct {
	VehicleID  string
	GeofenceID string
	Start      *time.Time
	End        *time.Time
	Limit      int
	Offset     int
}

// AlertEvent is the payload broadcast to WebSocket clients.
type AlertEvent struct {
	EventID   string        `json:"event_id"`
	EventType string        `json:"event_type"`
	Timestamp time.Time     `json:"timestamp"`
	Vehicle   AlertVehicle  `json:"vehicle"`
	Geofence  AlertGeofence `json:"geofence"`
	Location  AlertLocation `json:"location"`
}

// AlertVehicle is the vehicle section of an AlertEvent.
type AlertVehicle struct {
	VehicleID     string `json:"vehicle_id"`
	VehicleNumber string `json:"vehicle_number"`
	DriverName    string `json:"driver_name"`
}

// AlertGeofence is the geofence section of an AlertEvent.
type AlertGeofence struct {
	GeofenceID   string `json:"geofence_id"`
	GeofenceName string `json:"geofence_name"`
	Category     string `json:"category"`
}

// AlertLocation is the location section of an AlertEvent.
type AlertLocation struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}
