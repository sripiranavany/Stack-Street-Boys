package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata" // built-in time zone database, so Europe/Berlin works in a tiny container

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const tzName = "Europe/Berlin"

var berlin *time.Location

func init() {
	loc, err := time.LoadLocation(tzName)
	if err != nil {
		loc = time.UTC
	}
	berlin = loc
}

// ---------- errors and JSON ----------

type apiError struct {
	Status int
	Msg    string
}

func (e *apiError) Error() string { return e.Msg }

func badRequest(format string, a ...any) *apiError {
	return &apiError{Status: http.StatusBadRequest, Msg: fmt.Sprintf(format, a...)}
}

func notFound(format string, a ...any) *apiError {
	return &apiError{Status: http.StatusNotFound, Msg: fmt.Sprintf(format, a...)}
}

type handlerFunc func(w http.ResponseWriter, r *http.Request) error

// wrap turns a handler that returns an error into a normal http handler,
// adds a 30 second time limit and converts errors into JSON answers.
func wrap(h handlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		err := h(w, r.WithContext(ctx))
		if err == nil {
			return
		}
		var ae *apiError
		if errors.As(err, &ae) {
			writeJSON(w, ae.Status, map[string]string{"error": ae.Msg})
			return
		}
		log.Printf("ERROR %s %s: %v", r.Method, r.URL.RequestURI(), err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("write json: %v", err)
	}
}

// isUndefinedTable is true when a query failed because a table does not exist yet.
func isUndefinedTable(err error) bool {
	var pe *pgconn.PgError
	return errors.As(err, &pe) && pe.Code == "42P01"
}

// ---------- query parameters ----------

func parseTime(s string) (time.Time, error) {
	layouts := []string{time.RFC3339Nano, "2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02"}
	// In a URL a "+" turns into a space, so also try with the space changed back.
	candidates := []string{s, strings.ReplaceAll(s, " ", "+")}
	for _, c := range candidates {
		for _, l := range layouts {
			if t, err := time.ParseInLocation(l, c, time.UTC); err == nil {
				return t.UTC(), nil
			}
		}
	}
	return time.Time{}, fmt.Errorf("invalid time %q (use 2026-11-01 or 2026-11-01T12:00:00Z)", s)
}

func parseBool(s string, def bool) bool {
	switch strings.ToLower(s) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	}
	return def
}

func parseIntParam(s string, def, min, max int) (int, error) {
	if s == "" {
		return def, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < min || n > max {
		return 0, fmt.Errorf("must be a whole number from %d to %d", min, max)
	}
	return n, nil
}

// parseHorizon understands Go durations ("72h") and days ("3d").
func parseHorizon(s string) (time.Duration, error) {
	if strings.HasSuffix(s, "d") {
		n, err := strconv.ParseFloat(strings.TrimSuffix(s, "d"), 64)
		if err != nil || n <= 0 {
			return 0, fmt.Errorf("invalid horizon %q", s)
		}
		return time.Duration(n * float64(24*time.Hour)), nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("invalid horizon %q (examples: 24h, 72h, 3d)", s)
	}
	return d, nil
}

func parseMetric(s string) (string, error) {
	switch s {
	case "", "density", "densityData":
		return "densityData", nil
	case "battery", "batteryData":
		return "batteryData", nil
	}
	return "", badRequest("type must be densityData or batteryData")
}

type aggDef struct {
	sql  string        // text for the SQL interval
	step time.Duration // same length in Go
}

var aggs = map[string]aggDef{
	"15m": {"15 minutes", 15 * time.Minute},
	"1h":  {"1 hour", time.Hour},
	"1d":  {"1 day", 24 * time.Hour},
}

// latestTime is the newest reading in the database. Because the data is historical,
// "the last 7 days" means the 7 days before this moment, not before today.
func (a *app) latestTime(ctx context.Context) (time.Time, error) {
	var t *time.Time
	if err := a.db.QueryRow(ctx, `SELECT max(time) FROM readings`).Scan(&t); err != nil {
		return time.Time{}, err
	}
	if t == nil {
		return time.Now().UTC(), nil
	}
	return *t, nil
}

// timeRange reads ?from= and ?to=. Missing values default to the 7 days up to the newest reading.
func (a *app) timeRange(r *http.Request, defaultSpan time.Duration) (time.Time, time.Time, error) {
	q := r.URL.Query()
	var from, to time.Time
	var err error

	if v := q.Get("to"); v != "" {
		if to, err = parseTime(v); err != nil {
			return from, to, badRequest("to: %v", err)
		}
	} else {
		latest, err := a.latestTime(r.Context())
		if err != nil {
			return from, to, err
		}
		to = latest.Add(time.Second)
	}
	if v := q.Get("from"); v != "" {
		if from, err = parseTime(v); err != nil {
			return from, to, badRequest("from: %v", err)
		}
	} else {
		from = to.Add(-defaultSpan)
	}
	if !from.Before(to) {
		return from, to, badRequest("from must be earlier than to")
	}
	return from, to, nil
}

func (a *app) sensorName(ctx context.Context, id string) (string, error) {
	var name string
	err := a.db.QueryRow(ctx, `SELECT name FROM sensors WHERE sensor_id = $1`, id).Scan(&name)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", notFound("sensor %q not found", id)
	}
	return name, err
}

// ---------- small calculation helpers ----------

// percentile returns the p-th percentile (0..1) with linear interpolation.
func percentile(vals []float64, p float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	s := append([]float64(nil), vals...)
	sort.Float64s(s)
	idx := p * float64(len(s)-1)
	lo, hi := int(math.Floor(idx)), int(math.Ceil(idx))
	if lo == hi {
		return s[lo]
	}
	return s[lo] + (s[hi]-s[lo])*(idx-float64(lo))
}

type point struct {
	Time       time.Time `json:"time"`
	Value      *float64  `json:"value"`
	Normalized *float64  `json:"normalized,omitempty"`
	N          int       `json:"n"`
}

// fillBuckets returns one point for every step between from and to.
// Missing buckets get value null, which means "sensor offline / unknown" (never 0).
func fillBuckets(from, to time.Time, step time.Duration, got map[int64]point) []point {
	var out []point
	for t := from.Truncate(step); t.Before(to); t = t.Add(step) {
		if p, ok := got[t.Unix()]; ok {
			out = append(out, p)
		} else {
			out = append(out, point{Time: t})
		}
	}
	return out
}

// ---------- tiny SQL builder ----------

type qb struct {
	sb   strings.Builder
	args []any
}

func (q *qb) add(sql string) *qb {
	q.sb.WriteString(sql)
	q.sb.WriteByte(' ')
	return q
}

// p stores a value and returns its placeholder ($1, $2, ...).
func (q *qb) p(v any) string {
	q.args = append(q.args, v)
	return "$" + strconv.Itoa(len(q.args))
}

func (q *qb) String() string { return q.sb.String() }
