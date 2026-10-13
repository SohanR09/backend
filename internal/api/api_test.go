package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"geofence/internal/store"
)

// fakeHub records broadcasts instead of serving WebSockets.
type fakeHub struct {
	mu   sync.Mutex
	msgs [][]byte
}

func (f *fakeHub) ServeHTTP(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }
func (f *fakeHub) Broadcast(m []byte) {
	f.mu.Lock()
	f.msgs = append(f.msgs, m)
	f.mu.Unlock()
}
func (f *fakeHub) messages() [][]byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([][]byte(nil), f.msgs...)
}

type env struct {
	t   *testing.T
	srv *Server
	h   http.Handler
	hub *fakeHub
}

func newEnv(t *testing.T, cfg Config) *env {
	t.Helper()
	hub := &fakeHub{}
	srv, err := New(store.NewMemory(), hub, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	return &env{t: t, srv: srv, h: srv.Handler(), hub: hub}
}

func (e *env) do(method, path string, body any, headers ...string) (int, map[string]any) {
	e.t.Helper()
	var rd *bytes.Reader
	if body == nil {
		rd = bytes.NewReader(nil)
	} else if s, ok := body.(string); ok {
		rd = bytes.NewReader([]byte(s))
	} else {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, rd)
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	var out map[string]any
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			e.t.Fatalf("%s %s: response is not JSON: %q", method, path, rec.Body.String())
		}
	}
	return rec.Code, out
}

func (e *env) mustTimeNS(resp map[string]any) {
	e.t.Helper()
	v, ok := resp["time_ns"].(string)
	if !ok || v == "" {
		e.t.Fatalf("response missing string time_ns: %v", resp)
	}
}

var squareCoords = [][]float64{
	{37.7749, -122.4194}, {37.7849, -122.4194}, {37.7849, -122.4094}, {37.7749, -122.4094}, {37.7749, -122.4194},
}

func (e *env) newGeofence(category string) string {
	e.t.Helper()
	code, resp := e.do("POST", "/geofences", map[string]any{
		"name": "Zone " + category, "description": "d", "coordinates": squareCoords, "category": category,
	})
	if code != http.StatusCreated {
		e.t.Fatalf("create geofence: %d %v", code, resp)
	}
	e.mustTimeNS(resp)
	return resp["id"].(string)
}

func (e *env) newVehicle(number string) string {
	e.t.Helper()
	code, resp := e.do("POST", "/vehicles", map[string]any{
		"vehicle_number": number, "driver_name": "John Doe", "vehicle_type": "truck", "phone": "+1234567890",
	})
	if code != http.StatusCreated {
		e.t.Fatalf("create vehicle: %d %v", code, resp)
	}
	return resp["id"].(string)
}

func (e *env) locate(vehicleID string, lat, lon float64, ts string) map[string]any {
	e.t.Helper()
	code, resp := e.do("POST", "/vehicles/location", map[string]any{
		"vehicle_id": vehicleID, "latitude": lat, "longitude": lon, "timestamp": ts,
	})
	if code != http.StatusOK {
		e.t.Fatalf("locate: %d %v", code, resp)
	}
	e.mustTimeNS(resp)
	return resp
}

func waitMessages(t *testing.T, h *fakeHub, n int) [][]byte {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if m := h.messages(); len(m) >= n {
			return m
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("expected %d broadcast(s), got %d", n, len(h.messages()))
	return nil
}

func TestGeofenceValidation(t *testing.T) {
	e := newEnv(t, Config{})
	good := map[string]any{"name": "n", "coordinates": squareCoords, "category": "restricted_zone"}

	tests := []struct {
		name   string
		mutate func(m map[string]any)
	}{
		{"missing name", func(m map[string]any) { m["name"] = "" }},
		{"bad category", func(m map[string]any) { m["category"] = "nope" }},
		{"unclosed", func(m map[string]any) { m["coordinates"] = squareCoords[:4] }},
		{"too few points", func(m map[string]any) { m["coordinates"] = squareCoords[:3] }},
		{"latitude out of range", func(m map[string]any) {
			m["coordinates"] = [][]float64{{91, 0}, {0, 0}, {0, 1}, {91, 0}}
		}},
		{"longitude out of range", func(m map[string]any) {
			m["coordinates"] = [][]float64{{0, 181}, {0, 0}, {1, 0}, {0, 181}}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := map[string]any{}
			for k, v := range good {
				body[k] = v
			}
			tt.mutate(body)
			code, resp := e.do("POST", "/geofences", body)
			if code != http.StatusBadRequest {
				t.Fatalf("code = %d, want 400 (%v)", code, resp)
			}
			e.mustTimeNS(resp)
			if resp["error"] == "" {
				t.Fatal("expected error message")
			}
		})
	}

	if code, _ := e.do("POST", "/geofences", "{not json"); code != http.StatusBadRequest {
		t.Fatalf("invalid JSON: code = %d, want 400", code)
	}
}

func TestListGeofencesFilterAndShape(t *testing.T) {
	e := newEnv(t, Config{})
	e.newGeofence("restricted_zone")
	e.newGeofence("delivery_zone")

	code, resp := e.do("GET", "/geofences?category=restricted_zone", nil)
	if code != 200 {
		t.Fatalf("code = %d", code)
	}
	e.mustTimeNS(resp)
	list := resp["geofences"].([]any)
	if len(list) != 1 {
		t.Fatalf("got %d geofences, want 1", len(list))
	}
	g := list[0].(map[string]any)
	for _, k := range []string{"id", "name", "description", "coordinates", "category", "created_at"} {
		if _, ok := g[k]; !ok {
			t.Fatalf("geofence missing %q", k)
		}
	}
	if code, _ := e.do("GET", "/geofences?category=bogus", nil); code != http.StatusBadRequest {
		t.Fatalf("bad category filter: code = %d, want 400", code)
	}
}

func TestVehicleRegistrationAndDuplicates(t *testing.T) {
	e := newEnv(t, Config{})
	e.newVehicle("KA-01-AB-1234")

	code, resp := e.do("POST", "/vehicles", map[string]any{
		"vehicle_number": "KA-01-AB-1234", "driver_name": "x", "vehicle_type": "car", "phone": "1",
	})
	if code != http.StatusConflict {
		t.Fatalf("duplicate: code = %d, want 409 (%v)", code, resp)
	}
	if code, _ := e.do("POST", "/vehicles", map[string]any{"vehicle_number": "A"}); code != http.StatusBadRequest {
		t.Fatalf("missing fields: code = %d, want 400", code)
	}
	code, resp = e.do("GET", "/vehicles", nil)
	if code != 200 || len(resp["vehicles"].([]any)) != 1 {
		t.Fatalf("list vehicles: %d %v", code, resp)
	}
}

func TestLocationValidation(t *testing.T) {
	e := newEnv(t, Config{})
	vid := e.newVehicle("V1")
	ts := "2025-01-15T10:35:00Z"

	tests := []struct {
		name string
		body map[string]any
		want int
	}{
		{"unknown vehicle", map[string]any{"vehicle_id": "veh_nope", "latitude": 1, "longitude": 1, "timestamp": ts}, 404},
		{"missing latitude", map[string]any{"vehicle_id": vid, "longitude": 1, "timestamp": ts}, 400},
		{"latitude range", map[string]any{"vehicle_id": vid, "latitude": 100, "longitude": 1, "timestamp": ts}, 400},
		{"longitude range", map[string]any{"vehicle_id": vid, "latitude": 1, "longitude": -200, "timestamp": ts}, 400},
		{"missing timestamp", map[string]any{"vehicle_id": vid, "latitude": 1, "longitude": 1}, 400},
		{"bad timestamp", map[string]any{"vehicle_id": vid, "latitude": 1, "longitude": 1, "timestamp": "yesterday"}, 400},
		{"missing vehicle id", map[string]any{"latitude": 1, "longitude": 1, "timestamp": ts}, 400},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, resp := e.do("POST", "/vehicles/location", tt.body)
			if code != tt.want {
				t.Fatalf("code = %d, want %d (%v)", code, tt.want, resp)
			}
		})
	}
}

func TestEntryExitFlowWithAlerts(t *testing.T) {
	e := newEnv(t, Config{})
	gid := e.newGeofence("restricted_zone")
	vid := e.newVehicle("KA-01-AB-1234")

	code, resp := e.do("POST", "/alerts/configure", map[string]any{"geofence_id": gid, "event_type": "both"})
	if code != http.StatusCreated {
		t.Fatalf("configure alert: %d %v", code, resp)
	}
	e.mustTimeNS(resp)
	if resp["vehicle_id"] != nil {
		t.Fatalf("vehicle_id should be null for an all-vehicles rule, got %v", resp["vehicle_id"])
	}

	// Outside: no events.
	resp = e.locate(vid, 37.70, -122.50, "2025-01-15T10:00:00Z")
	if n := len(resp["current_geofences"].([]any)); n != 0 {
		t.Fatalf("expected outside, got %d geofences", n)
	}

	// Inside: entry event.
	resp = e.locate(vid, 37.7799, -122.4144, "2025-01-15T10:05:00Z")
	cur := resp["current_geofences"].([]any)
	if len(cur) != 1 || cur[0].(map[string]any)["status"] != "inside" {
		t.Fatalf("expected one inside geofence, got %v", cur)
	}
	// Still inside: no new event.
	e.locate(vid, 37.7800, -122.4140, "2025-01-15T10:06:00Z")
	// Leave: exit event.
	e.locate(vid, 37.70, -122.50, "2025-01-15T10:10:00Z")

	msgs := waitMessages(t, e.hub, 2)
	if len(msgs) != 2 {
		t.Fatalf("got %d alerts, want exactly 2 (entry, exit)", len(msgs))
	}
	var entry, exit map[string]any
	_ = json.Unmarshal(msgs[0], &entry)
	_ = json.Unmarshal(msgs[1], &exit)
	if entry["event_type"] != "entry" || exit["event_type"] != "exit" {
		t.Fatalf("event order wrong: %v then %v", entry["event_type"], exit["event_type"])
	}
	for _, k := range []string{"event_id", "timestamp", "vehicle", "geofence", "location"} {
		if _, ok := entry[k]; !ok {
			t.Fatalf("alert missing %q", k)
		}
	}
	if entry["geofence"].(map[string]any)["category"] != "restricted_zone" {
		t.Fatalf("bad geofence payload: %v", entry["geofence"])
	}

	// Alerts are persisted for history.
	code, resp = e.do("GET", "/alerts/events", nil)
	if code != 200 || len(resp["events"].([]any)) != 2 {
		t.Fatalf("alert events: %d %v", code, resp)
	}

	// Violation history: both transitions, newest first.
	code, resp = e.do("GET", "/violations/history?vehicle_id="+vid, nil)
	if code != 200 {
		t.Fatalf("history: %d", code)
	}
	e.mustTimeNS(resp)
	if resp["total_count"].(float64) != 2 {
		t.Fatalf("total_count = %v, want 2", resp["total_count"])
	}
	v := resp["violations"].([]any)
	if v[0].(map[string]any)["event_type"] != "exit" || v[1].(map[string]any)["event_type"] != "entry" {
		t.Fatalf("history order wrong: %v", v)
	}
	for _, k := range []string{"id", "vehicle_id", "vehicle_number", "geofence_id", "geofence_name", "event_type", "latitude", "longitude", "timestamp"} {
		if _, ok := v[0].(map[string]any)[k]; !ok {
			t.Fatalf("violation missing %q", k)
		}
	}
}

func TestAlertRuleScopingAndEventType(t *testing.T) {
	e := newEnv(t, Config{})
	gid := e.newGeofence("restricted_zone")
	target := e.newVehicle("TARGET")
	other := e.newVehicle("OTHER")

	// Only entry events for TARGET.
	if code, resp := e.do("POST", "/alerts/configure", map[string]any{
		"geofence_id": gid, "vehicle_id": target, "event_type": "entry",
	}); code != http.StatusCreated {
		t.Fatalf("configure: %d %v", code, resp)
	}

	e.locate(other, 37.7799, -122.4144, "2025-01-15T10:00:00Z") // other enters: no alert
	e.locate(target, 37.7799, -122.4144, "2025-01-15T10:00:00Z")
	e.locate(target, 37.70, -122.50, "2025-01-15T10:05:00Z") // exit: not configured
	waitMessages(t, e.hub, 1)
	time.Sleep(100 * time.Millisecond)
	if n := len(e.hub.messages()); n != 1 {
		t.Fatalf("got %d alerts, want 1", n)
	}

	// History still records every transition regardless of rules.
	_, resp := e.do("GET", "/violations/history", nil)
	if resp["total_count"].(float64) != 3 {
		t.Fatalf("history total = %v, want 3", resp["total_count"])
	}
}

func TestOutOfOrderLocationDoesNotFlipState(t *testing.T) {
	e := newEnv(t, Config{})
	e.newGeofence("restricted_zone")
	vid := e.newVehicle("V")

	e.locate(vid, 37.7799, -122.4144, "2025-01-15T10:10:00Z") // inside (newest)
	resp := e.locate(vid, 37.70, -122.50, "2025-01-15T10:00:00Z")
	if n := len(resp["current_geofences"].([]any)); n != 1 {
		t.Fatalf("stale outside report must not clear state; current_geofences=%d", n)
	}
	_, hist := e.do("GET", "/violations/history", nil)
	if hist["total_count"].(float64) != 1 {
		t.Fatalf("stale report must not create exit event, total=%v", hist["total_count"])
	}
}

func TestGetVehicleLocation(t *testing.T) {
	e := newEnv(t, Config{})
	gid := e.newGeofence("delivery_zone")
	vid := e.newVehicle("V")

	if code, _ := e.do("GET", "/vehicles/location/"+vid, nil); code != http.StatusNotFound {
		t.Fatalf("before any location: code = %d, want 404", code)
	}
	if code, _ := e.do("GET", "/vehicles/location/veh_nope", nil); code != http.StatusNotFound {
		t.Fatalf("unknown vehicle: code = %d, want 404", code)
	}

	e.locate(vid, 37.7799, -122.4144, "2025-01-15T10:35:00Z")
	code, resp := e.do("GET", "/vehicles/location/"+vid, nil)
	if code != 200 {
		t.Fatalf("code = %d", code)
	}
	e.mustTimeNS(resp)
	if resp["vehicle_number"] != "V" {
		t.Fatalf("vehicle_number = %v", resp["vehicle_number"])
	}
	loc := resp["current_location"].(map[string]any)
	if loc["latitude"].(float64) != 37.7799 || loc["timestamp"] != "2025-01-15T10:35:00Z" {
		t.Fatalf("bad location: %v", loc)
	}
	gs := resp["current_geofences"].([]any)
	if len(gs) != 1 || gs[0].(map[string]any)["geofence_id"] != gid || gs[0].(map[string]any)["category"] != "delivery_zone" {
		t.Fatalf("bad geofences: %v", gs)
	}

	code, resp = e.do("GET", "/vehicles/locations", nil)
	if code != 200 || len(resp["locations"].([]any)) != 1 {
		t.Fatalf("locations: %d %v", code, resp)
	}
}

func TestAlertConfigurationAndManagement(t *testing.T) {
	e := newEnv(t, Config{})
	gid := e.newGeofence("toll_zone")
	vid := e.newVehicle("V")

	if code, _ := e.do("POST", "/alerts/configure", map[string]any{"geofence_id": gid, "event_type": "sometimes"}); code != 400 {
		t.Fatalf("bad event_type: code = %d, want 400", code)
	}
	if code, _ := e.do("POST", "/alerts/configure", map[string]any{"geofence_id": "geo_nope", "event_type": "entry"}); code != 404 {
		t.Fatalf("unknown geofence: code = %d, want 404", code)
	}
	if code, _ := e.do("POST", "/alerts/configure", map[string]any{"geofence_id": gid, "vehicle_id": "veh_nope", "event_type": "entry"}); code != 404 {
		t.Fatalf("unknown vehicle: code = %d, want 404", code)
	}

	_, created := e.do("POST", "/alerts/configure", map[string]any{"geofence_id": gid, "vehicle_id": vid, "event_type": "exit"})
	alertID := created["alert_id"].(string)

	code, resp := e.do("GET", "/alerts?geofence_id="+gid+"&vehicle_id="+vid, nil)
	if code != 200 {
		t.Fatalf("list alerts: %d", code)
	}
	e.mustTimeNS(resp)
	a := resp["alerts"].([]any)[0].(map[string]any)
	if a["geofence_name"] != "Zone toll_zone" || a["vehicle_number"] != "V" || a["status"] != "active" {
		t.Fatalf("bad alert view: %v", a)
	}

	if code, _ := e.do("DELETE", "/alerts/"+alertID, nil); code != 200 {
		t.Fatalf("delete: code = %d", code)
	}
	if code, _ := e.do("DELETE", "/alerts/"+alertID, nil); code != 404 {
		t.Fatalf("delete twice: code = %d, want 404", code)
	}
	_, resp = e.do("GET", "/alerts", nil)
	if len(resp["alerts"].([]any)) != 0 {
		t.Fatal("alert should be gone")
	}
}

func TestViolationHistoryFiltersAndPagination(t *testing.T) {
	e := newEnv(t, Config{})
	gid := e.newGeofence("restricted_zone")
	vid := e.newVehicle("V")

	// 3 round trips = 6 events, one hour apart.
	base := time.Date(2025, 1, 15, 8, 0, 0, 0, time.UTC)
	for i := 0; i < 3; i++ {
		e.locate(vid, 37.7799, -122.4144, base.Add(time.Duration(2*i)*time.Hour).Format(time.RFC3339))
		e.locate(vid, 37.70, -122.50, base.Add(time.Duration(2*i+1)*time.Hour).Format(time.RFC3339))
	}

	_, resp := e.do("GET", "/violations/history?limit=4", nil)
	if resp["total_count"].(float64) != 6 || len(resp["violations"].([]any)) != 4 {
		t.Fatalf("limit: total=%v len=%d", resp["total_count"], len(resp["violations"].([]any)))
	}
	_, resp = e.do("GET", "/violations/history?limit=4&offset=4", nil)
	if len(resp["violations"].([]any)) != 2 {
		t.Fatalf("offset page should have 2 rows, got %d", len(resp["violations"].([]any)))
	}
	_, resp = e.do("GET", "/violations/history?geofence_id="+gid+"&start_date=2025-01-15T10:00:00Z&end_date=2025-01-15T11:00:00Z", nil)
	if resp["total_count"].(float64) != 2 {
		t.Fatalf("date range total = %v, want 2", resp["total_count"])
	}
	_, resp = e.do("GET", "/violations/history?vehicle_id=veh_other", nil)
	if resp["total_count"].(float64) != 0 || len(resp["violations"].([]any)) != 0 {
		t.Fatalf("unknown vehicle filter should be empty: %v", resp)
	}

	for _, bad := range []string{"limit=0", "limit=abc", "offset=-1", "start_date=nope",
		"start_date=2025-02-01T00:00:00Z&end_date=2025-01-01T00:00:00Z"} {
		if code, _ := e.do("GET", "/violations/history?"+bad, nil); code != 400 {
			t.Fatalf("%s: code = %d, want 400", bad, code)
		}
	}
	// A limit above the maximum is clamped rather than rejected.
	if code, _ := e.do("GET", "/violations/history?limit=100000", nil); code != 200 {
		t.Fatalf("huge limit: code = %d, want 200", code)
	}
}

func TestAPIKeyAuth(t *testing.T) {
	e := newEnv(t, Config{APIKey: "secret"})

	code, resp := e.do("GET", "/geofences", nil)
	if code != http.StatusUnauthorized {
		t.Fatalf("no key: code = %d, want 401", code)
	}
	e.mustTimeNS(resp)
	if code, _ := e.do("GET", "/geofences", nil, "X-API-Key", "wrong"); code != 401 {
		t.Fatalf("wrong key: code = %d, want 401", code)
	}
	if code, _ := e.do("GET", "/geofences", nil, "X-API-Key", "secret"); code != 200 {
		t.Fatalf("header key: code = %d, want 200", code)
	}
	if code, _ := e.do("GET", "/geofences", nil, "Authorization", "Bearer secret"); code != 200 {
		t.Fatalf("bearer key: code = %d, want 200", code)
	}
	if code, _ := e.do("GET", "/health", nil); code != 200 {
		t.Fatalf("health must stay open: code = %d", code)
	}
	if code, _ := e.do("GET", "/ws/alerts", nil); code != 401 {
		t.Fatalf("ws without key: code = %d, want 401", code)
	}
	if code, _ := e.do("GET", "/ws/alerts?api_key=secret", nil); code != 200 {
		t.Fatalf("ws with query key: code = %d, want 200", code)
	}
}

func TestRateLimitOnLocationUpdates(t *testing.T) {
	e := newEnv(t, Config{RateLimitPerSec: 0.001, RateLimitBurst: 2})
	vid := e.newVehicle("V")
	post := func() int {
		code, _ := e.do("POST", "/vehicles/location", map[string]any{
			"vehicle_id": vid, "latitude": 1, "longitude": 1, "timestamp": "2025-01-15T10:00:00Z",
		})
		return code
	}
	if post() != 200 || post() != 200 {
		t.Fatal("first two requests within burst must pass")
	}
	if code := post(); code != http.StatusTooManyRequests {
		t.Fatalf("third request: code = %d, want 429", code)
	}
	// Other routes are not rate limited.
	for i := 0; i < 5; i++ {
		if code, _ := e.do("GET", "/vehicles", nil); code != 200 {
			t.Fatalf("GET /vehicles throttled: %d", code)
		}
	}
}

func TestPaginationOnListEndpoints(t *testing.T) {
	e := newEnv(t, Config{})
	for i := 0; i < 5; i++ {
		e.newVehicle("V" + string(rune('A'+i)))
	}
	_, resp := e.do("GET", "/vehicles?limit=2&offset=1", nil)
	if len(resp["vehicles"].([]any)) != 2 || resp["total_count"].(float64) != 5 {
		t.Fatalf("paged vehicles: %v", resp)
	}
	if code, _ := e.do("GET", "/vehicles?limit=x", nil); code != 400 {
		t.Fatalf("bad limit: code = %d, want 400", code)
	}
}

func TestUnknownRouteReturnsJSON404(t *testing.T) {
	e := newEnv(t, Config{})
	code, resp := e.do("GET", "/nope", nil)
	if code != 404 || !strings.Contains(resp["error"].(string), "/nope") {
		t.Fatalf("got %d %v", code, resp)
	}
	e.mustTimeNS(resp)
}

func TestCORSPreflight(t *testing.T) {
	e := newEnv(t, Config{APIKey: "secret"})
	req := httptest.NewRequest(http.MethodOptions, "/geofences", nil)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent || rec.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("preflight: %d %v", rec.Code, rec.Header())
	}
}
