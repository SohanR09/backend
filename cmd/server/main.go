// Command server runs the geofencing and vehicle tracking API.
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"geofence/internal/api"
	"geofence/internal/config"
	"geofence/internal/database"
	"geofence/internal/ws"
)

func main() {
	env := config.Load()
	cfg := api.Config{
		APIKey:          env.APIKey,
		RateLimitPerSec: env.RateLimitPerSec,
		RateLimitBurst:  env.RateLimitBurst,
		TrustProxy:      env.TrustProxy,
		AllowedOrigin:   env.AllowedOrigin,
	}

	pg, err := database.ConnectPostgres(env.DSN, env.DBConnectRetries, env.DBAutoCreate)
	if err != nil {
		log.Fatalf("database: %v", err)
	}
	defer pg.Close()

	hub := ws.NewHub()
	srv, err := api.New(pg, hub, cfg)
	if err != nil {
		log.Fatalf("init: %v", err)
	}

	addr := ":" + env.Port
	httpServer := &http.Server{
		Addr:              addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	go func() {
		log.Printf("geofence API listening on %s (auth=%t, rate limit=%v/s burst %d)",
			addr, cfg.APIKey != "", cfg.RateLimitPerSec, cfg.RateLimitBurst)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	log.Println("shutting down...")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(ctx); err != nil {
		log.Printf("shutdown: %v", err)
	}
	srv.Close()
}
