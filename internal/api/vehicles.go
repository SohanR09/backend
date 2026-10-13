package api

import (
	"net/http"
	"strings"
	"time"

	"geofence/internal/geo"
	"geofence/internal/model"
	"geofence/internal/store"
)

type createVehicleRequest struct {
	VehicleNumber string `json:"vehicle_number"`
	DriverName    string `json:"driver_name"`
	VehicleType   string `json:"vehicle_type"`
	Phone         string `json:"phone"`
}

func (s *Server) createVehicle(r *http.Request) (int, map[string]any) {
	var req createVehicleRequest
	if status, err := decodeJSON(r, &req); err != nil {
		return fail(status, "%v", err)
	}
	req.VehicleNumber = strings.TrimSpace(req.VehicleNumber)
	req.DriverName = strings.TrimSpace(req.DriverName)
	req.VehicleType = strings.TrimSpace(req.VehicleType)
	req.Phone = strings.TrimSpace(req.Phone)

	switch {
	case req.VehicleNumber == "":
		return fail(http.StatusBadRequest, "vehicle_number is required")
	case req.DriverName == "":
		return fail(http.StatusBadRequest, "driver_name is required")
	case req.VehicleType == "":
		return fail(http.StatusBadRequest, "vehicle_type is required")
	case req.Phone == "":
		return fail(http.StatusBadRequest, "phone is required")
	}

	v := model.Vehicle{
		ID:            newID("veh"),
		VehicleNumber: req.VehicleNumber,
		DriverName:    req.DriverName,
		VehicleType:   req.VehicleType,
		Phone:         req.Phone,
		Status:        "active",
		CreatedAt:     time.Now().UTC(),
	}
	switch err := s.store.CreateVehicle(v); err {
	case nil:
	case store.ErrDuplicate:
		return fail(http.StatusConflict, "vehicle_number %q is already registered", v.VehicleNumber)
	default:
		return fail(http.StatusInternalServerError, "could not save vehicle")
	}
	return http.StatusCreated, map[string]any{"id": v.ID, "vehicle_number": v.VehicleNumber, "status": v.Status}
}

func (s *Server) listVehicles(r *http.Request) (int, map[string]any) {
	page, err := parsePagination(r.URL.Query(), 500)
	if err != nil {
		return fail(http.StatusBadRequest, "%v", err)
	}
	all, err := s.store.ListVehicles()
	if err != nil {
		return fail(http.StatusInternalServerError, "could not load vehicles")
	}
	body := map[string]any{"vehicles": paginate(all, page)}
	if page.paged {
		body["total_count"] = len(all)
	}
	return http.StatusOK, body
}

type updateLocationRequest struct {
	VehicleID string   `json:"vehicle_id"`
	Latitude  *float64 `json:"latitude"`
	Longitude *float64 `json:"longitude"`
	Timestamp string   `json:"timestamp"`
}

type geofenceStatus struct {
	GeofenceID   string `json:"geofence_id"`
	GeofenceName string `json:"geofence_name"`
	Status       string `json:"status"`
}

func (s *Server) updateLocation(r *http.Request) (int, map[string]any) {
	var req updateLocationRequest
	if status, err := decodeJSON(r, &req); err != nil {
		return fail(status, "%v", err)
	}
	if strings.TrimSpace(req.VehicleID) == "" {
		return fail(http.StatusBadRequest, "vehicle_id is required")
	}
	if req.Latitude == nil || req.Longitude == nil {
		return fail(http.StatusBadRequest, "latitude and longitude are required")
	}
	if err := geo.ValidateCoordinate(*req.Latitude, *req.Longitude); err != nil {
		return fail(http.StatusBadRequest, "%v", err)
	}
	if req.Timestamp == "" {
		return fail(http.StatusBadRequest, "timestamp is required (ISO 8601, e.g. 2025-01-15T10:35:00Z)")
	}
	ts, err := time.Parse(time.RFC3339Nano, req.Timestamp)
	if err != nil {
		return fail(http.StatusBadRequest, "timestamp must be ISO 8601, e.g. 2025-01-15T10:35:00Z")
	}
	ts = ts.UTC()

	vehicle, err := s.store.GetVehicle(req.VehicleID)
	if err == store.ErrNotFound {
		return fail(http.StatusNotFound, "vehicle %q not found", req.VehicleID)
	} else if err != nil {
		return fail(http.StatusInternalServerError, "could not load vehicle")
	}

	loc := model.Location{VehicleID: vehicle.ID, Latitude: *req.Latitude, Longitude: *req.Longitude, Timestamp: ts}

	inside, err := s.store.GeofencesContaining(loc.Latitude, loc.Longitude)
	if err != nil {
		return fail(http.StatusInternalServerError, "could not evaluate geofences")
	}
	insideIDs := make([]string, len(inside))
	for i, g := range inside {
		insideIDs[i] = g.ID
	}

	isLatest, previous, err := s.store.ApplyLocation(loc, insideIDs)
	if err == store.ErrNotFound {
		return fail(http.StatusNotFound, "vehicle %q not found", req.VehicleID)
	} else if err != nil {
		return fail(http.StatusInternalServerError, "could not save location")
	}

	if isLatest {
		if err := s.processTransitions(vehicle, loc, inside, previous); err != nil {
			return fail(http.StatusInternalServerError, "could not process geofence events")
		}
	} else {
		// An older, out-of-order report: it is kept for history but must not
		// change the vehicle's state, so report the real current geofences.
		inside = previous
	}

	current := make([]geofenceStatus, 0, len(inside))
	for _, g := range inside {
		current = append(current, geofenceStatus{GeofenceID: g.ID, GeofenceName: g.Name, Status: "inside"})
	}
	return http.StatusOK, map[string]any{
		"vehicle_id":        vehicle.ID,
		"location_updated":  true,
		"current_geofences": current,
	}
}

type vehicleLocationView struct {
	VehicleID        string                `json:"vehicle_id"`
	VehicleNumber    string                `json:"vehicle_number"`
	DriverName       string                `json:"driver_name,omitempty"`
	CurrentLocation  locationView          `json:"current_location"`
	CurrentGeofences []currentGeofenceView `json:"current_geofences"`
}

type locationView struct {
	Latitude  float64   `json:"latitude"`
	Longitude float64   `json:"longitude"`
	Timestamp time.Time `json:"timestamp"`
}

type currentGeofenceView struct {
	GeofenceID   string `json:"geofence_id"`
	GeofenceName string `json:"geofence_name"`
	Category     string `json:"category"`
}

func (s *Server) viewFor(v model.Vehicle) (vehicleLocationView, error) {
	loc, err := s.store.LatestLocation(v.ID)
	if err != nil {
		return vehicleLocationView{}, err
	}
	geofences, err := s.store.GeofenceState(v.ID)
	if err != nil {
		return vehicleLocationView{}, err
	}
	views := make([]currentGeofenceView, 0, len(geofences))
	for _, g := range geofences {
		views = append(views, currentGeofenceView{GeofenceID: g.ID, GeofenceName: g.Name, Category: g.Category})
	}
	return vehicleLocationView{
		VehicleID:        v.ID,
		VehicleNumber:    v.VehicleNumber,
		DriverName:       v.DriverName,
		CurrentLocation:  locationView{Latitude: loc.Latitude, Longitude: loc.Longitude, Timestamp: loc.Timestamp},
		CurrentGeofences: views,
	}, nil
}

func (s *Server) getVehicleLocation(r *http.Request) (int, map[string]any) {
	id := r.PathValue("vehicle_id")
	v, err := s.store.GetVehicle(id)
	if err == store.ErrNotFound {
		return fail(http.StatusNotFound, "vehicle %q not found", id)
	} else if err != nil {
		return fail(http.StatusInternalServerError, "could not load vehicle")
	}
	view, err := s.viewFor(v)
	if err == store.ErrNotFound {
		return fail(http.StatusNotFound, "no location recorded yet for vehicle %q", id)
	} else if err != nil {
		return fail(http.StatusInternalServerError, "could not load location")
	}
	return http.StatusOK, map[string]any{
		"vehicle_id":        view.VehicleID,
		"vehicle_number":    view.VehicleNumber,
		"current_location":  view.CurrentLocation,
		"current_geofences": view.CurrentGeofences,
	}
}

// listVehicleLocations is an extra endpoint (not part of the required set)
// that returns the latest position of every vehicle that has reported one, so
// the dashboard can draw all markers with a single request.
func (s *Server) listVehicleLocations(*http.Request) (int, map[string]any) {
	vehicles, err := s.store.ListVehicles()
	if err != nil {
		return fail(http.StatusInternalServerError, "could not load vehicles")
	}
	out := make([]vehicleLocationView, 0, len(vehicles))
	for _, v := range vehicles {
		view, err := s.viewFor(v)
		if err == store.ErrNotFound {
			continue
		} else if err != nil {
			return fail(http.StatusInternalServerError, "could not load locations")
		}
		out = append(out, view)
	}
	return http.StatusOK, map[string]any{"locations": out}
}
