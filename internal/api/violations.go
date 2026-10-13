package api

import (
	"net/http"
	"strconv"
	"time"

	"geofence/internal/model"
)

const (
	defaultHistoryLimit = 50
	maxHistoryLimit     = 500
)

func (s *Server) violationHistory(r *http.Request) (int, map[string]any) {
	q := r.URL.Query()
	filter := model.ViolationFilter{
		VehicleID:  q.Get("vehicle_id"),
		GeofenceID: q.Get("geofence_id"),
		Limit:      defaultHistoryLimit,
	}

	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return fail(http.StatusBadRequest, "limit must be a positive integer")
		}
		filter.Limit = min(n, maxHistoryLimit)
	}
	if v := q.Get("offset"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return fail(http.StatusBadRequest, "offset must be a non-negative integer")
		}
		filter.Offset = n
	}
	for _, p := range []struct {
		name string
		dst  **time.Time
	}{{"start_date", &filter.Start}, {"end_date", &filter.End}} {
		v := q.Get(p.name)
		if v == "" {
			continue
		}
		t, err := time.Parse(time.RFC3339Nano, v)
		if err != nil {
			return fail(http.StatusBadRequest, "%s must be ISO 8601, e.g. 2025-01-01T00:00:00Z", p.name)
		}
		t = t.UTC()
		*p.dst = &t
	}
	if filter.Start != nil && filter.End != nil && filter.Start.After(*filter.End) {
		return fail(http.StatusBadRequest, "start_date must not be after end_date")
	}

	violations, total, err := s.store.ListViolations(filter)
	if err != nil {
		return fail(http.StatusInternalServerError, "could not load violation history")
	}
	return http.StatusOK, map[string]any{"violations": violations, "total_count": total}
}
