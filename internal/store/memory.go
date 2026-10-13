package store

import (
	"sort"
	"sync"

	"geofence/internal/geo"
	"geofence/internal/model"
)

// Memory is a concurrency-safe in-memory Store. It exists for unit tests and
// as the reference behaviour the Postgres store must match; the server itself
// always runs on Postgres.
type Memory struct {
	mu sync.RWMutex

	geofences    []model.Geofence
	vehicles     []model.Vehicle
	latest       map[string]model.Location
	state        map[string][]string
	rules        []model.AlertRule
	violations   []model.Violation
	alertEvents  []model.AlertEvent
	vehicleByNum map[string]struct{}
}

// NewMemory returns an empty in-memory store.
func NewMemory() *Memory {
	return &Memory{
		latest:       make(map[string]model.Location),
		state:        make(map[string][]string),
		vehicleByNum: make(map[string]struct{}),
	}
}

var _ Store = (*Memory)(nil)

func (m *Memory) CreateGeofence(g model.Geofence) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.geofences = append(m.geofences, g)
	return nil
}

func (m *Memory) ListGeofences(category string) ([]model.Geofence, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]model.Geofence, 0, len(m.geofences))
	for _, g := range m.geofences {
		if category == "" || g.Category == category {
			out = append(out, g)
		}
	}
	return out, nil
}

func (m *Memory) GetGeofence(id string) (model.Geofence, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, g := range m.geofences {
		if g.ID == id {
			return g, nil
		}
	}
	return model.Geofence{}, ErrNotFound
}

func (m *Memory) GeofencesContaining(lat, lon float64) ([]model.Geofence, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]model.Geofence, 0, 4)
	for _, g := range m.geofences {
		if geo.PointInPolygon(lat, lon, g.Coordinates) {
			out = append(out, g)
		}
	}
	return out, nil
}

func (m *Memory) CreateVehicle(v model.Vehicle) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, taken := m.vehicleByNum[v.VehicleNumber]; taken {
		return ErrDuplicate
	}
	m.vehicleByNum[v.VehicleNumber] = struct{}{}
	m.vehicles = append(m.vehicles, v)
	return nil
}

func (m *Memory) ListVehicles() ([]model.Vehicle, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]model.Vehicle, len(m.vehicles))
	copy(out, m.vehicles)
	return out, nil
}

func (m *Memory) GetVehicle(id string) (model.Vehicle, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, v := range m.vehicles {
		if v.ID == id {
			return v, nil
		}
	}
	return model.Vehicle{}, ErrNotFound
}

// geofencesByIDLocked resolves ids to geofences, preserving order. Callers
// must hold at least a read lock.
func (m *Memory) geofencesByIDLocked(ids []string) []model.Geofence {
	out := make([]model.Geofence, 0, len(ids))
	for _, id := range ids {
		for _, g := range m.geofences {
			if g.ID == id {
				out = append(out, g)
				break
			}
		}
	}
	return out
}

func (m *Memory) ApplyLocation(loc model.Location, insideIDs []string) (bool, []model.Geofence, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	known := false
	for _, v := range m.vehicles {
		if v.ID == loc.VehicleID {
			known = true
			break
		}
	}
	if !known {
		return false, nil, ErrNotFound
	}

	previous := m.geofencesByIDLocked(m.state[loc.VehicleID])
	cur, ok := m.latest[loc.VehicleID]
	if ok && loc.Timestamp.Before(cur.Timestamp) {
		return false, previous, nil
	}
	m.latest[loc.VehicleID] = loc
	m.state[loc.VehicleID] = append([]string(nil), insideIDs...)
	return true, previous, nil
}

func (m *Memory) LatestLocation(vehicleID string) (model.Location, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	loc, ok := m.latest[vehicleID]
	if !ok {
		return model.Location{}, ErrNotFound
	}
	return loc, nil
}

func (m *Memory) GeofenceState(vehicleID string) ([]model.Geofence, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.geofencesByIDLocked(m.state[vehicleID]), nil
}

func (m *Memory) CreateAlertRule(r model.AlertRule) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rules = append(m.rules, r)
	return nil
}

func (m *Memory) ListAlertRules(f AlertRuleFilter) ([]model.AlertRule, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]model.AlertRule, 0, len(m.rules))
	for _, r := range m.rules {
		if f.GeofenceID != "" && r.GeofenceID != f.GeofenceID {
			continue
		}
		if f.VehicleID != "" && r.VehicleID != f.VehicleID {
			continue
		}
		for _, g := range m.geofences {
			if g.ID == r.GeofenceID {
				r.GeofenceName = g.Name
				break
			}
		}
		for _, v := range m.vehicles {
			if v.ID == r.VehicleID {
				r.VehicleNumber = v.VehicleNumber
				break
			}
		}
		out = append(out, r)
	}
	return out, nil
}

func (m *Memory) DeleteAlertRule(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, r := range m.rules {
		if r.ID == id {
			m.rules = append(m.rules[:i], m.rules[i+1:]...)
			return nil
		}
	}
	return ErrNotFound
}

func (m *Memory) HasActiveAlertRule(vehicleID, geofenceID, eventType string) (bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, r := range m.rules {
		if r.GeofenceID != geofenceID || r.Status != "active" {
			continue
		}
		if r.VehicleID != "" && r.VehicleID != vehicleID {
			continue
		}
		if r.EventType == model.EventBoth || r.EventType == eventType {
			return true, nil
		}
	}
	return false, nil
}

func (m *Memory) AddViolation(v model.Violation) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.violations = append(m.violations, v)
	return nil
}

func (m *Memory) ListViolations(f model.ViolationFilter) ([]model.Violation, int, error) {
	m.mu.RLock()
	matched := make([]model.Violation, 0)
	for i := len(m.violations) - 1; i >= 0; i-- { // newest insert first
		v := m.violations[i]
		if f.VehicleID != "" && v.VehicleID != f.VehicleID {
			continue
		}
		if f.GeofenceID != "" && v.GeofenceID != f.GeofenceID {
			continue
		}
		if f.Start != nil && v.Timestamp.Before(*f.Start) {
			continue
		}
		if f.End != nil && v.Timestamp.After(*f.End) {
			continue
		}
		matched = append(matched, v)
	}
	m.mu.RUnlock()

	// Newest first; for equal timestamps the later insert comes first, which
	// matches the Postgres ordering (recorded_at DESC, seq DESC).
	sort.SliceStable(matched, func(i, j int) bool {
		return matched[i].Timestamp.After(matched[j].Timestamp)
	})
	total := len(matched)

	if f.Offset >= total {
		return []model.Violation{}, total, nil
	}
	matched = matched[f.Offset:]
	if f.Limit > 0 && f.Limit < len(matched) {
		matched = matched[:f.Limit]
	}
	return matched, total, nil
}

func (m *Memory) AddAlertEvent(e model.AlertEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.alertEvents = append(m.alertEvents, e)
	return nil
}

func (m *Memory) ListAlertEvents(limit int) ([]model.AlertEvent, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	n := len(m.alertEvents)
	if limit <= 0 || limit > n {
		limit = n
	}
	out := make([]model.AlertEvent, 0, limit)
	for i := n - 1; i >= n-limit; i-- {
		out = append(out, m.alertEvents[i])
	}
	return out, nil
}
