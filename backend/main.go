package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"ssb-backend/internal/api"
	ebbdb "ssb-backend/internal/db"
	"ssb-backend/internal/worker"
)

// app holds everything the handlers share.
type app struct {
	db *pgxpool.Pool
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// dsn builds the database address from environment variables.
// In Kubernetes they come from the ConfigMap "app-config" and the Secret "db-credentials".
func dsn() string {
	u := url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(env("POSTGRES_USER", "app"), os.Getenv("POSTGRES_PASSWORD")),
		Host:     net.JoinHostPort(env("DB_HOST", "localhost"), env("DB_PORT", "5432")),
		Path:     env("POSTGRES_DB", "energy"),
		RawQuery: "sslmode=disable",
	}
	return u.String()
}

// connect opens a pool; tune can adjust the config (e.g. point it at the ebb schema).
func connect(ctx context.Context, tune ...func(*pgxpool.Config)) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn())
	if err != nil {
		return nil, err
	}
	cfg.MaxConns = 10
	for _, t := range tune {
		t(cfg)
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	// The database may still be starting: retry for about a minute.
	for i := 1; i <= 30; i++ {
		if err = pool.Ping(ctx); err == nil {
			return pool, nil
		}
		log.Printf("waiting for database (%d/30): %v", i, err)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
	return nil, fmt.Errorf("database not reachable: %w", err)
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s (%s)", r.Method, r.URL.RequestURI(), time.Since(start).Round(time.Millisecond))
	})
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := connect(ctx)
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()
	a := &app{db: pool}

	mux := http.NewServeMux()

	// Health checks. /healthz is used by Kubernetes, /readyz also checks the database.
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		if err := pool.Ping(r.Context()); err != nil {
			http.Error(w, "database not ready", http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte("ok"))
	})
	mux.HandleFunc("GET /api/hello", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"message": "Hello from Go on STACKIT"})
	})

	// The API.
	mux.HandleFunc("GET /api/sensors", wrap(a.listSensors))
	mux.HandleFunc("GET /api/sensors/{id}/timeseries", wrap(a.timeseries))
	mux.HandleFunc("GET /api/heatmap", wrap(a.heatmap))
	mux.HandleFunc("GET /api/patterns/{id}", wrap(a.patterns))
	mux.HandleFunc("GET /api/events/spikes", wrap(a.spikes))
	mux.HandleFunc("GET /api/forecast/{id}", wrap(a.forecast))
	mux.HandleFunc("GET /api/context", wrap(a.context))
	mux.HandleFunc("GET /api/export", wrap(a.export))

	// Ebb decision layer (/api/v1): forecasts, store plans, Calm Hours, events, alerts, replay clock.
	// Its tables live in the "ebb" schema of the same database.
	ebbPool, err := connect(ctx, ebbdb.ConfigureEbb)
	if err != nil {
		log.Fatal(err)
	}
	defer ebbPool.Close()
	if err := ebbdb.Migrate(ctx, ebbPool); err != nil {
		log.Fatal(err)
	}
	forecaster := env("FORECASTER_URL", "http://forecaster:8000")
	(&api.Server{P: ebbPool, ForecasterURL: forecaster, GeminiKey: os.Getenv("GEMINI_API_KEY")}).Register(mux)
	// The worker (replay clock, forecast runs, gap-fill, sensor health, alerts) runs in exactly one place:
	// enable it with EBB_WORKER=true on a single replica.
	if env("EBB_WORKER", "false") == "true" {
		go (&worker.Worker{P: ebbPool, ForecasterURL: forecaster, LiveURL: os.Getenv("LIVE_URL")}).Run(ctx)
		log.Printf("ebb worker started")
	}

	srv := &http.Server{
		Addr:              ":" + env("PORT", "8080"),
		Handler:           logRequests(mux),
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      2 * time.Minute,
		IdleTimeout:       2 * time.Minute,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		srv.Shutdown(shutdownCtx)
	}()

	log.Printf("listening on %s", srv.Addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
