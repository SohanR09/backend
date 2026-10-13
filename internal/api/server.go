// Package api wires the HTTP handlers, middleware and asynchronous alert
// delivery together.
package api

import (
	"crypto/rand"
	"encoding/hex"
	"log"
	"net/http"
	"sync"
	"time"

	"geofence/internal/model"
	"geofence/internal/store"
)

// Hub is the real-time delivery channel: it accepts WebSocket upgrades on
// /ws/alerts and fans alert payloads out to connected clients.
type Hub interface {
	http.Handler
	Broadcast(msg []byte)
}

// Config holds optional server behaviour.
type Config struct {
	// APIKey, when non-empty, is required on every request except /health,
	// CORS preflights and static hub upgrades that pass ?api_key=.
	APIKey string
	// RateLimitPerSec and RateLimitBurst throttle POST /vehicles/location per
	// client. Zero values disable rate limiting.
	RateLimitPerSec float64
	RateLimitBurst  int
	// TrustProxy makes the rate limiter key on X-Forwarded-For.
	TrustProxy bool
	// AllowedOrigin is returned in Access-Control-Allow-Origin ("*" if empty).
	AllowedOrigin string
}

// Server implements the geofencing API.
type Server struct {
	store   store.Store
	hub     Hub
	cfg     Config
	limiter *rateLimiter

	events    chan model.AlertEvent
	done      chan struct{}
	workerEnd chan struct{}
	closeOnce sync.Once
}

// New builds a Server and starts the alert delivery worker. It returns an
// error so future initialisation can fail cleanly.
func New(st store.Store, hub Hub, cfg Config) (*Server, error) {
	s := &Server{
		store:     st,
		hub:       hub,
		cfg:       cfg,
		events:    make(chan model.AlertEvent, 1024),
		done:      make(chan struct{}),
		workerEnd: make(chan struct{}),
	}
	if cfg.RateLimitPerSec > 0 && cfg.RateLimitBurst > 0 {
		s.limiter = newRateLimiter(cfg.RateLimitPerSec, cfg.RateLimitBurst)
	}
	go s.deliveryWorker()
	return s, nil
}

// Close stops the delivery worker after flushing queued alerts.
func (s *Server) Close() {
	s.closeOnce.Do(func() {
		close(s.done)
		<-s.workerEnd
	})
}

// Handler returns the fully wrapped HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /health", s.handle(func(*http.Request) (int, map[string]any) {
		return http.StatusOK, map[string]any{"status": "ok"}
	}))

	mux.HandleFunc("POST /geofences", s.handle(s.createGeofence))
	mux.HandleFunc("GET /geofences", s.handle(s.listGeofences))

	mux.HandleFunc("POST /vehicles", s.handle(s.createVehicle))
	mux.HandleFunc("GET /vehicles", s.handle(s.listVehicles))
	mux.HandleFunc("GET /vehicles/locations", s.handle(s.listVehicleLocations))
	mux.Handle("POST /vehicles/location", s.rateLimit(s.handle(s.updateLocation)))
	mux.HandleFunc("GET /vehicles/location/{vehicle_id}", s.handle(s.getVehicleLocation))

	mux.HandleFunc("POST /alerts/configure", s.handle(s.configureAlert))
	mux.HandleFunc("GET /alerts", s.handle(s.listAlerts))
	mux.HandleFunc("GET /alerts/events", s.handle(s.listAlertEvents))
	mux.HandleFunc("DELETE /alerts/{alert_id}", s.handle(s.deleteAlert))

	mux.HandleFunc("GET /violations/history", s.handle(s.violationHistory))

	mux.Handle("/ws/alerts", s.hub)

	mux.HandleFunc("/", s.handle(func(r *http.Request) (int, map[string]any) {
		return fail(http.StatusNotFound, "no route for %s %s", r.Method, r.URL.Path)
	}))

	return s.recoverer(s.cors(s.logger(s.auth(s.limitBody(mux)))))
}

// newID returns a prefixed random identifier such as "veh_9f2c41aa7b10".
func newID(prefix string) string {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand failing is not recoverable for ID generation.
		panic("api: cannot read random bytes: " + err.Error())
	}
	return prefix + "_" + hex.EncodeToString(b[:])
}

// deliveryWorker persists alert events and pushes them to WebSocket clients,
// keeping both off the request path.
func (s *Server) deliveryWorker() {
	defer close(s.workerEnd)
	for {
		select {
		case ev := <-s.events:
			s.deliver(ev)
		case <-s.done:
			for {
				select {
				case ev := <-s.events:
					s.deliver(ev)
				default:
					return
				}
			}
		}
	}
}

func (s *Server) deliver(ev model.AlertEvent) {
	if err := s.store.AddAlertEvent(ev); err != nil {
		log.Printf("alerts: store event %s: %v", ev.EventID, err)
	}
	payload, err := marshalJSON(ev)
	if err != nil {
		log.Printf("alerts: encode event %s: %v", ev.EventID, err)
		return
	}
	s.hub.Broadcast(payload)
}

// enqueue hands an event to the worker without ever blocking the request.
func (s *Server) enqueue(ev model.AlertEvent) {
	select {
	case s.events <- ev:
	default:
		// Queue full: fall back to a goroutine so the alert is not lost.
		go func() {
			select {
			case s.events <- ev:
			case <-s.done:
				s.deliver(ev)
			case <-time.After(5 * time.Second):
				log.Printf("alerts: dropped event %s (queue saturated)", ev.EventID)
			}
		}()
	}
}
