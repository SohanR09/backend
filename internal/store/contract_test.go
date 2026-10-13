package store

import (
	"errors"
	"testing"
	"time"

	"geofence/internal/model"
)

// The contract suite pins down the behaviour every Store implementation must
// share. It runs against Memory here and against Postgres in
// postgres_integration_test.go (build tag "integration").

func TestMemoryContract(t *testing.T) {
	runContract(t, func(t *testing.T) Store { return NewMemory() })
}

var (
	t0 = time.Date(2025, 1, 15, 10, 0, 0, 0, time.UTC)

	squareRing = [][2]float64{
		{37.7749, -122.4194}, {37.7849, -122.4194}, {37.7849, -122.4094}, {37.7749, -122.4094}, {37.7749, -122.4194},
	}
	// Concave "L": the notch around (8, 8) is outside.
	lRing = [][2]float64{{0, 0}, {0, 10}, {5, 10}, {5, 5}, {10, 5}, {10, 0}, {0, 0}}
)

func mustNoErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func geofence(id, category string, ring [][2]float64, created time.Time) model.Geofence {
	return model.Geofence{ID: id, Name: "name-" + id, Description: "desc-" + id, Coordinates: ring, Category: category, CreatedAt: created}
}

func vehicle(id, number string) model.Vehicle {
	return model.Vehicle{ID: id, VehicleNumber: number, DriverName: "John", VehicleType: "truck", Phone: "+1", Status: "active", CreatedAt: t0}
}

func ids(gs []model.Geofence) []string {
	out := make([]string, len(gs))
	for i, g := range gs {
		out[i] = g.ID
	}
	return out
}

func sameIDs(t *testing.T, got []model.Geofence, want ...string) {
	t.Helper()
	g := ids(got)
	if len(g) != len(want) {
		t.Fatalf("got ids %v, want %v", g, want)
	}
	for i := range g {
		if g[i] != want[i] {
			t.Fatalf("got ids %v, want %v", g, want)
		}
	}
}

func runContract(t *testing.T, newStore func(t *testing.T) Store) {
	t.Run("geofences", func(t *testing.T) {
		s := newStore(t)
		a := geofence("geo_a", model.CategoryRestricted, squareRing, t0)
		b := geofence("geo_b", model.CategoryDelivery, lRing, t0.Add(time.Second))
		mustNoErr(t, s.CreateGeofence(a))
		mustNoErr(t, s.CreateGeofence(b))

		all, err := s.ListGeofences("")
		mustNoErr(t, err)
		sameIDs(t, all, "geo_a", "geo_b")

		only, err := s.ListGeofences(model.CategoryDelivery)
		mustNoErr(t, err)
		sameIDs(t, only, "geo_b")

		none, err := s.ListGeofences(model.CategoryToll)
		mustNoErr(t, err)
		if none == nil || len(none) != 0 {
			t.Fatalf("expected empty non-nil slice, got %#v", none)
		}

		got, err := s.GetGeofence("geo_a")
		mustNoErr(t, err)
		if got.Name != a.Name || got.Description != a.Description || got.Category != a.Category ||
			!got.CreatedAt.Equal(a.CreatedAt) || len(got.Coordinates) != len(squareRing) {
			t.Fatalf("round trip mismatch: %+v", got)
		}
		for i, p := range squareRing {
			if got.Coordinates[i] != p {
				t.Fatalf("coordinate %d = %v, want %v (full precision must survive)", i, got.Coordinates[i], p)
			}
		}
		if _, err := s.GetGeofence("geo_missing"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("missing geofence err = %v, want ErrNotFound", err)
		}
	})

	t.Run("geofences containing", func(t *testing.T) {
		s := newStore(t)
		mustNoErr(t, s.CreateGeofence(geofence("geo_sq", model.CategoryRestricted, squareRing, t0)))
		mustNoErr(t, s.CreateGeofence(geofence("geo_l", model.CategoryDelivery, lRing, t0.Add(time.Second))))

		check := func(lat, lon float64, want ...string) {
			t.Helper()
			got, err := s.GeofencesContaining(lat, lon)
			mustNoErr(t, err)
			sameIDs(t, got, want...)
		}
		check(37.7799, -122.4144, "geo_sq") // inside the square
		check(37.7849, -122.4144, "geo_sq") // exactly on an edge
		check(37.7749, -122.4194, "geo_sq") // exactly on a vertex
		check(37.8, -122.4144)              // outside both
		check(2, 2, "geo_l")                // inside the L
		check(8, 8)                         // in the L's bounding box but inside the notch
		check(2, -5)                        // west of everything
		if got, err := s.GeofencesContaining(1, 1); err != nil || got == nil {
			t.Fatalf("expected non-nil result, got %v err %v", got, err)
		}
	})

	t.Run("vehicles", func(t *testing.T) {
		s := newStore(t)
		mustNoErr(t, s.CreateVehicle(vehicle("veh_1", "KA-01")))
		mustNoErr(t, s.CreateVehicle(vehicle("veh_2", "KA-02")))
		if err := s.CreateVehicle(vehicle("veh_3", "KA-01")); !errors.Is(err, ErrDuplicate) {
			t.Fatalf("duplicate number err = %v, want ErrDuplicate", err)
		}
		list, err := s.ListVehicles()
		mustNoErr(t, err)
		if len(list) != 2 || list[0].ID != "veh_1" || list[1].ID != "veh_2" {
			t.Fatalf("list = %+v", list)
		}
		got, err := s.GetVehicle("veh_1")
		mustNoErr(t, err)
		if got.VehicleNumber != "KA-01" || got.DriverName != "John" || got.Phone != "+1" || got.Status != "active" || !got.CreatedAt.Equal(t0) {
			t.Fatalf("round trip mismatch: %+v", got)
		}
		if _, err := s.GetVehicle("veh_nope"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("apply location and state", func(t *testing.T) {
		s := newStore(t)
		mustNoErr(t, s.CreateGeofence(geofence("geo_a", model.CategoryRestricted, squareRing, t0)))
		mustNoErr(t, s.CreateGeofence(geofence("geo_b", model.CategoryDelivery, lRing, t0.Add(time.Second))))
		mustNoErr(t, s.CreateVehicle(vehicle("veh_1", "KA-01")))

		if _, err := s.LatestLocation("veh_1"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("no location yet: err = %v, want ErrNotFound", err)
		}
		if _, _, err := s.ApplyLocation(model.Location{VehicleID: "veh_nope", Timestamp: t0}, nil); !errors.Is(err, ErrNotFound) {
			t.Fatalf("unknown vehicle: err = %v, want ErrNotFound", err)
		}

		// First ever location: latest, nothing before it.
		latest, prev, err := s.ApplyLocation(model.Location{VehicleID: "veh_1", Latitude: 37.7799, Longitude: -122.4144, Timestamp: t0.Add(10 * time.Minute)}, []string{"geo_a"})
		mustNoErr(t, err)
		if !latest || len(prev) != 0 {
			t.Fatalf("first: latest=%v prev=%v", latest, ids(prev))
		}
		state, err := s.GeofenceState("veh_1")
		mustNoErr(t, err)
		sameIDs(t, state, "geo_a")

		// Newer location moves the vehicle into both: previous is the old state.
		latest, prev, err = s.ApplyLocation(model.Location{VehicleID: "veh_1", Latitude: 2, Longitude: 2, Timestamp: t0.Add(20 * time.Minute)}, []string{"geo_a", "geo_b"})
		mustNoErr(t, err)
		if !latest {
			t.Fatal("newer location must be latest")
		}
		sameIDs(t, prev, "geo_a")
		state, _ = s.GeofenceState("veh_1")
		sameIDs(t, state, "geo_a", "geo_b")

		// Older, out-of-order location: stored but state and latest are untouched.
		latest, prev, err = s.ApplyLocation(model.Location{VehicleID: "veh_1", Latitude: 50, Longitude: 50, Timestamp: t0.Add(5 * time.Minute)}, nil)
		mustNoErr(t, err)
		if latest {
			t.Fatal("older location must not be latest")
		}
		sameIDs(t, prev, "geo_a", "geo_b")
		state, _ = s.GeofenceState("veh_1")
		sameIDs(t, state, "geo_a", "geo_b")
		loc, err := s.LatestLocation("veh_1")
		mustNoErr(t, err)
		if loc.Latitude != 2 || loc.Longitude != 2 || !loc.Timestamp.Equal(t0.Add(20*time.Minute)) {
			t.Fatalf("latest = %+v", loc)
		}

		// Same timestamp as the latest counts as latest (last write wins).
		latest, _, err = s.ApplyLocation(model.Location{VehicleID: "veh_1", Latitude: 50, Longitude: 50, Timestamp: t0.Add(20 * time.Minute)}, nil)
		mustNoErr(t, err)
		if !latest {
			t.Fatal("equal timestamp must count as latest")
		}
		state, _ = s.GeofenceState("veh_1")
		if len(state) != 0 {
			t.Fatalf("state should be empty, got %v", ids(state))
		}
		loc, _ = s.LatestLocation("veh_1")
		if loc.Latitude != 50 {
			t.Fatalf("latest = %+v, want the last write at the same timestamp", loc)
		}
	})

	t.Run("alert rules", func(t *testing.T) {
		s := newStore(t)
		mustNoErr(t, s.CreateGeofence(geofence("geo_a", model.CategoryRestricted, squareRing, t0)))
		mustNoErr(t, s.CreateGeofence(geofence("geo_b", model.CategoryDelivery, lRing, t0.Add(time.Second))))
		mustNoErr(t, s.CreateVehicle(vehicle("veh_1", "KA-01")))
		mustNoErr(t, s.CreateVehicle(vehicle("veh_2", "KA-02")))

		rule := func(id, geofenceID, vehicleID, event string) model.AlertRule {
			return model.AlertRule{ID: id, GeofenceID: geofenceID, VehicleID: vehicleID, EventType: event, Status: "active", CreatedAt: t0}
		}
		mustNoErr(t, s.CreateAlertRule(rule("alert_1", "geo_a", "", model.EventEntry)))     // all vehicles, entry
		mustNoErr(t, s.CreateAlertRule(rule("alert_2", "geo_a", "veh_1", model.EventExit))) // veh_1 only, exit
		mustNoErr(t, s.CreateAlertRule(rule("alert_3", "geo_b", "veh_2", model.EventBoth))) // veh_2, both

		all, err := s.ListAlertRules(AlertRuleFilter{})
		mustNoErr(t, err)
		if len(all) != 3 || all[0].ID != "alert_1" || all[2].ID != "alert_3" {
			t.Fatalf("all = %+v", all)
		}
		if all[0].GeofenceName != "name-geo_a" || all[0].VehicleID != "" || all[0].VehicleNumber != "" {
			t.Fatalf("all-vehicles rule view = %+v", all[0])
		}
		if all[1].VehicleID != "veh_1" || all[1].VehicleNumber != "KA-01" {
			t.Fatalf("vehicle rule view = %+v", all[1])
		}

		byGeo, _ := s.ListAlertRules(AlertRuleFilter{GeofenceID: "geo_a"})
		if len(byGeo) != 2 {
			t.Fatalf("by geofence = %d, want 2", len(byGeo))
		}
		byVeh, _ := s.ListAlertRules(AlertRuleFilter{VehicleID: "veh_1"})
		if len(byVeh) != 1 || byVeh[0].ID != "alert_2" {
			t.Fatalf("by vehicle = %+v", byVeh)
		}

		has := func(vehicleID, geofenceID, event string, want bool) {
			t.Helper()
			got, err := s.HasActiveAlertRule(vehicleID, geofenceID, event)
			mustNoErr(t, err)
			if got != want {
				t.Fatalf("HasActiveAlertRule(%s,%s,%s) = %v, want %v", vehicleID, geofenceID, event, got, want)
			}
		}
		has("veh_1", "geo_a", model.EventEntry, true)  // all-vehicles entry rule
		has("veh_2", "geo_a", model.EventEntry, true)  // all-vehicles entry rule
		has("veh_1", "geo_a", model.EventExit, true)   // veh_1's exit rule
		has("veh_2", "geo_a", model.EventExit, false)  // exit rule is veh_1 only
		has("veh_2", "geo_b", model.EventEntry, true)  // 'both' covers entry
		has("veh_2", "geo_b", model.EventExit, true)   // ... and exit
		has("veh_1", "geo_b", model.EventEntry, false) // rule is veh_2 only

		mustNoErr(t, s.DeleteAlertRule("alert_1"))
		has("veh_1", "geo_a", model.EventEntry, false)
		if err := s.DeleteAlertRule("alert_1"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("second delete err = %v, want ErrNotFound", err)
		}
	})

	t.Run("violations", func(t *testing.T) {
		s := newStore(t)
		mustNoErr(t, s.CreateGeofence(geofence("geo_a", model.CategoryRestricted, squareRing, t0)))
		mustNoErr(t, s.CreateGeofence(geofence("geo_b", model.CategoryDelivery, lRing, t0.Add(time.Second))))
		mustNoErr(t, s.CreateVehicle(vehicle("veh_1", "KA-01")))
		mustNoErr(t, s.CreateVehicle(vehicle("veh_2", "KA-02")))

		add := func(id, vehicleID, geofenceID, event string, at time.Time) {
			t.Helper()
			mustNoErr(t, s.AddViolation(model.Violation{
				ID: id, VehicleID: vehicleID, VehicleNumber: "num-" + vehicleID, GeofenceID: geofenceID,
				GeofenceName: "name-" + geofenceID, EventType: event, Latitude: 37.7799, Longitude: -122.4144, Timestamp: at,
			}))
		}
		add("viol_1", "veh_1", "geo_a", "entry", t0.Add(1*time.Hour))
		add("viol_2", "veh_1", "geo_a", "exit", t0.Add(2*time.Hour))
		add("viol_3", "veh_2", "geo_b", "entry", t0.Add(3*time.Hour))
		add("viol_4", "veh_1", "geo_b", "entry", t0.Add(4*time.Hour))

		vids := func(vs []model.Violation) []string {
			out := make([]string, len(vs))
			for i, v := range vs {
				out[i] = v.ID
			}
			return out
		}
		eq := func(got []string, want ...string) {
			t.Helper()
			if len(got) != len(want) {
				t.Fatalf("got %v, want %v", got, want)
			}
			for i := range got {
				if got[i] != want[i] {
					t.Fatalf("got %v, want %v", got, want)
				}
			}
		}

		got, total, err := s.ListViolations(model.ViolationFilter{})
		mustNoErr(t, err)
		if total != 4 {
			t.Fatalf("total = %d, want 4", total)
		}
		eq(vids(got), "viol_4", "viol_3", "viol_2", "viol_1") // newest first
		if got[3].VehicleNumber != "num-veh_1" || got[3].GeofenceName != "name-geo_a" || got[3].Latitude != 37.7799 || !got[3].Timestamp.Equal(t0.Add(time.Hour)) {
			t.Fatalf("round trip mismatch: %+v", got[3])
		}

		got, total, _ = s.ListViolations(model.ViolationFilter{VehicleID: "veh_1"})
		eq(vids(got), "viol_4", "viol_2", "viol_1")
		if total != 3 {
			t.Fatalf("vehicle total = %d", total)
		}
		got, _, _ = s.ListViolations(model.ViolationFilter{GeofenceID: "geo_b"})
		eq(vids(got), "viol_4", "viol_3")

		start, end := t0.Add(90*time.Minute), t0.Add(3*time.Hour)
		got, total, _ = s.ListViolations(model.ViolationFilter{Start: &start, End: &end})
		eq(vids(got), "viol_3", "viol_2") // both bounds inclusive
		if total != 2 {
			t.Fatalf("range total = %d", total)
		}

		got, total, _ = s.ListViolations(model.ViolationFilter{Limit: 2, Offset: 1})
		eq(vids(got), "viol_3", "viol_2")
		if total != 4 {
			t.Fatalf("paged total = %d, want 4 (ignores limit/offset)", total)
		}
		got, total, _ = s.ListViolations(model.ViolationFilter{Limit: 2, Offset: 10})
		if len(got) != 0 || total != 4 || got == nil {
			t.Fatalf("offset past end: got %#v total %d", got, total)
		}
		got, total, _ = s.ListViolations(model.ViolationFilter{VehicleID: "veh_none"})
		if len(got) != 0 || total != 0 || got == nil {
			t.Fatalf("no matches: got %#v total %d", got, total)
		}
	})

	t.Run("alert events", func(t *testing.T) {
		s := newStore(t)
		ev := func(id string, at time.Time) model.AlertEvent {
			return model.AlertEvent{
				EventID: id, EventType: "entry", Timestamp: at,
				Vehicle:  model.AlertVehicle{VehicleID: "veh_1", VehicleNumber: "KA-01", DriverName: "John"},
				Geofence: model.AlertGeofence{GeofenceID: "geo_a", GeofenceName: "A", Category: "restricted_zone"},
				Location: model.AlertLocation{Latitude: 37.7799, Longitude: -122.4144},
			}
		}
		mustNoErr(t, s.AddAlertEvent(ev("evt_1", t0)))
		mustNoErr(t, s.AddAlertEvent(ev("evt_2", t0.Add(time.Minute))))
		mustNoErr(t, s.AddAlertEvent(ev("evt_3", t0.Add(2*time.Minute))))

		got, err := s.ListAlertEvents(2)
		mustNoErr(t, err)
		if len(got) != 2 || got[0].EventID != "evt_3" || got[1].EventID != "evt_2" {
			t.Fatalf("got %+v", got)
		}
		if got[0].Vehicle.DriverName != "John" || got[0].Geofence.Category != "restricted_zone" ||
			got[0].Location.Latitude != 37.7799 || !got[0].Timestamp.Equal(t0.Add(2*time.Minute)) {
			t.Fatalf("round trip mismatch: %+v", got[0])
		}
		all, _ := s.ListAlertEvents(0)
		if len(all) != 3 {
			t.Fatalf("limit 0 should return all, got %d", len(all))
		}
	})
}
