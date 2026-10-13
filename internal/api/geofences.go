package api

import (
	"net/http"
	"strings"
	"time"

	"geofence/internal/geo"
	"geofence/internal/model"
)

const maxPolygonPoints = 10000

type createGeofenceRequest struct {
	Name        string       `json:"name"`
	Description string       `json:"description"`
	Coordinates [][2]float64 `json:"coordinates"`
	Category    string       `json:"category"`
}

func (s *Server) createGeofence(r *http.Request) (int, map[string]any) {
	var req createGeofenceRequest
	if status, err := decodeJSON(r, &req); err != nil {
		return fail(status, "%v", err)
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		return fail(http.StatusBadRequest, "name is required")
	}
	if !model.ValidCategory(req.Category) {
		return fail(http.StatusBadRequest,
			"category must be one of: delivery_zone, restricted_zone, toll_zone, customer_area")
	}
	if len(req.Coordinates) > maxPolygonPoints {
		return fail(http.StatusBadRequest, "coordinates may contain at most %d points", maxPolygonPoints)
	}
	if err := geo.ValidatePolygon(req.Coordinates); err != nil {
		return fail(http.StatusBadRequest, "%v", err)
	}

	g := model.Geofence{
		ID:          newID("geo"),
		Name:        req.Name,
		Description: strings.TrimSpace(req.Description),
		Coordinates: req.Coordinates,
		Category:    req.Category,
		CreatedAt:   time.Now().UTC(),
	}
	if err := s.store.CreateGeofence(g); err != nil {
		return fail(http.StatusInternalServerError, "could not save geofence")
	}

	return http.StatusCreated, map[string]any{"id": g.ID, "name": g.Name, "status": "active"}
}

func (s *Server) listGeofences(r *http.Request) (int, map[string]any) {
	q := r.URL.Query()
	category := q.Get("category")
	if category != "" && !model.ValidCategory(category) {
		return fail(http.StatusBadRequest,
			"category must be one of: delivery_zone, restricted_zone, toll_zone, customer_area")
	}
	page, err := parsePagination(q, 500)
	if err != nil {
		return fail(http.StatusBadRequest, "%v", err)
	}
	all, err := s.store.ListGeofences(category)
	if err != nil {
		return fail(http.StatusInternalServerError, "could not load geofences")
	}
	body := map[string]any{"geofences": paginate(all, page)}
	if page.paged {
		body["total_count"] = len(all)
	}
	return http.StatusOK, body
}
