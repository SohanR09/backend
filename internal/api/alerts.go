package api

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"geofence/internal/model"
	"geofence/internal/store"
)

type configureAlertRequest struct {
	GeofenceID string `json:"geofence_id"`
	VehicleID  string `json:"vehicle_id"`
	EventType  string `json:"event_type"`
}

func (s *Server) configureAlert(r *http.Request) (int, map[string]any) {
	var req configureAlertRequest
	if status, err := decodeJSON(r, &req); err != nil {
		return fail(status, "%v", err)
	}
	req.GeofenceID = strings.TrimSpace(req.GeofenceID)
	req.VehicleID = strings.TrimSpace(req.VehicleID)

	if req.GeofenceID == "" {
		return fail(http.StatusBadRequest, "geofence_id is required")
	}
	if !model.ValidEventType(req.EventType) {
		return fail(http.StatusBadRequest, "event_type must be one of: entry, exit, both")
	}
	if _, err := s.store.GetGeofence(req.GeofenceID); err == store.ErrNotFound {
		return fail(http.StatusNotFound, "geofence %q not found", req.GeofenceID)
	} else if err != nil {
		return fail(http.StatusInternalServerError, "could not load geofence")
	}
	if req.VehicleID != "" {
		if _, err := s.store.GetVehicle(req.VehicleID); err == store.ErrNotFound {
			return fail(http.StatusNotFound, "vehicle %q not found", req.VehicleID)
		} else if err != nil {
			return fail(http.StatusInternalServerError, "could not load vehicle")
		}
	}

	rule := model.AlertRule{
		ID:         newID("alert"),
		GeofenceID: req.GeofenceID,
		VehicleID:  req.VehicleID,
		EventType:  req.EventType,
		Status:     "active",
		CreatedAt:  time.Now().UTC(),
	}
	if err := s.store.CreateAlertRule(rule); err != nil {
		return fail(http.StatusInternalServerError, "could not save alert rule")
	}
	return http.StatusCreated, map[string]any{
		"alert_id":    rule.ID,
		"geofence_id": rule.GeofenceID,
		"vehicle_id":  nullable(rule.VehicleID),
		"event_type":  rule.EventType,
		"status":      rule.Status,
	}
}

// nullable renders an empty string as JSON null ("applies to all vehicles").
func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

type alertRuleView struct {
	AlertID       string    `json:"alert_id"`
	GeofenceID    string    `json:"geofence_id"`
	GeofenceName  string    `json:"geofence_name"`
	VehicleID     any       `json:"vehicle_id"`
	VehicleNumber any       `json:"vehicle_number"`
	EventType     string    `json:"event_type"`
	Status        string    `json:"status"`
	CreatedAt     time.Time `json:"created_at"`
}

func (s *Server) listAlerts(r *http.Request) (int, map[string]any) {
	q := r.URL.Query()
	page, err := parsePagination(q, 500)
	if err != nil {
		return fail(http.StatusBadRequest, "%v", err)
	}
	rules, err := s.store.ListAlertRules(store.AlertRuleFilter{
		GeofenceID: q.Get("geofence_id"),
		VehicleID:  q.Get("vehicle_id"),
	})
	if err != nil {
		return fail(http.StatusInternalServerError, "could not load alert rules")
	}

	views := make([]alertRuleView, 0, len(rules))
	for _, rule := range rules {
		v := alertRuleView{
			AlertID:      rule.ID,
			GeofenceID:   rule.GeofenceID,
			GeofenceName: rule.GeofenceName,
			VehicleID:    nullable(rule.VehicleID),
			EventType:    rule.EventType,
			Status:       rule.Status,
			CreatedAt:    rule.CreatedAt,
		}
		if rule.VehicleID != "" {
			v.VehicleNumber = rule.VehicleNumber
		}
		views = append(views, v)
	}
	body := map[string]any{"alerts": paginate(views, page)}
	if page.paged {
		body["total_count"] = len(views)
	}
	return http.StatusOK, body
}

func (s *Server) deleteAlert(r *http.Request) (int, map[string]any) {
	id := r.PathValue("alert_id")
	switch err := s.store.DeleteAlertRule(id); err {
	case nil:
		return http.StatusOK, map[string]any{"alert_id": id, "deleted": true}
	case store.ErrNotFound:
		return fail(http.StatusNotFound, "alert %q not found", id)
	default:
		return fail(http.StatusInternalServerError, "could not delete alert rule")
	}
}

// listAlertEvents returns recently delivered alerts (newest first) so a
// freshly loaded client can show a feed without waiting for new events.
func (s *Server) listAlertEvents(r *http.Request) (int, map[string]any) {
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return fail(http.StatusBadRequest, "limit must be a positive integer")
		}
		limit = min(n, 500)
	}
	events, err := s.store.ListAlertEvents(limit)
	if err != nil {
		return fail(http.StatusInternalServerError, "could not load alert events")
	}
	return http.StatusOK, map[string]any{"events": events}
}

// processTransitions compares the geofences the vehicle is now inside with
// the ones it was inside before, records every entry/exit as a violation
// (history), and queues a real-time alert for each transition that matches an
// active alert rule.
func (s *Server) processTransitions(v model.Vehicle, loc model.Location, inside, previous []model.Geofence) error {
	was := make(map[string]struct{}, len(previous))
	for _, g := range previous {
		was[g.ID] = struct{}{}
	}
	is := make(map[string]struct{}, len(inside))
	for _, g := range inside {
		is[g.ID] = struct{}{}
	}

	for _, g := range inside {
		if _, existed := was[g.ID]; !existed {
			if err := s.recordEvent(v, loc, g, model.EventEntry); err != nil {
				return err
			}
		}
	}
	for _, g := range previous {
		if _, still := is[g.ID]; !still {
			if err := s.recordEvent(v, loc, g, model.EventExit); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Server) recordEvent(v model.Vehicle, loc model.Location, g model.Geofence, eventType string) error {
	violation := model.Violation{
		ID:            newID("viol"),
		VehicleID:     v.ID,
		VehicleNumber: v.VehicleNumber,
		GeofenceID:    g.ID,
		GeofenceName:  g.Name,
		EventType:     eventType,
		Latitude:      loc.Latitude,
		Longitude:     loc.Longitude,
		Timestamp:     loc.Timestamp,
	}
	if err := s.store.AddViolation(violation); err != nil {
		return err
	}

	// Several matching rules still produce a single alert.
	matched, err := s.store.HasActiveAlertRule(v.ID, g.ID, eventType)
	if err != nil || !matched {
		return err
	}
	s.enqueue(model.AlertEvent{
		EventID:   newID("evt"),
		EventType: eventType,
		Timestamp: loc.Timestamp,
		Vehicle:   model.AlertVehicle{VehicleID: v.ID, VehicleNumber: v.VehicleNumber, DriverName: v.DriverName},
		Geofence:  model.AlertGeofence{GeofenceID: g.ID, GeofenceName: g.Name, Category: g.Category},
		Location:  model.AlertLocation{Latitude: loc.Latitude, Longitude: loc.Longitude},
	})
	return nil
}
