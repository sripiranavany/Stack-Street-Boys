// Package api exposes Ebb's REST API (/api/v1).
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"ssb-backend/internal/db"
)

type Server struct {
	P             *pgxpool.Pool
	ForecasterURL string
	GeminiKey     string

	mu      sync.Mutex
	typical db.Profile
	typAt   time.Time
}

// Register mounts Ebb's decision API under /api/v1 on the service's shared mux
// (next to the City Pulse data API under /api).
func (s *Server) Register(mux *http.ServeMux) {
	routes := []struct {
		pattern string
		h       http.HandlerFunc
	}{
		{"GET /api/v1/state", s.getState},
		{"POST /api/v1/admin/clock", s.postClock},
		{"GET /api/v1/sensors", s.getSensors},
		{"GET /api/v1/sensors/{id}/history", s.getHistory},
		{"GET /api/v1/sensors/{id}/forecast", s.getForecast},
		{"GET /api/v1/sensors/{id}/forecast-vs-actual", s.getForecastVsActual},
		{"POST /api/v1/whatif", s.postWhatIf},
		{"GET /api/v1/locations/compare", s.getCompare},
		{"GET /api/v1/health", s.getHealth},
		{"GET /api/v1/alerts", s.getAlerts},
		{"PATCH /api/v1/alerts/{id}", s.patchAlert},
		{"GET /api/v1/events", s.getEvents},
		{"GET /api/v1/events/learned", s.getLearnedEvents},
		{"POST /api/v1/events", s.postEvent},
		{"DELETE /api/v1/events/{id}", s.deleteEvent},
		{"GET /api/v1/model", s.getModel},
		{"GET /api/v1/stores", s.getStores},
		{"GET /api/v1/stores/overview", s.getStoresOverview},
		{"GET /api/v1/stores/{id}/dashboard", s.getStoreDashboard},
		{"GET /api/v1/stores/{id}/plan", s.getPlan},
		{"GET /api/v1/stores/{id}/forecast", s.getStoreForecast},
		{"PUT /api/v1/stores/{id}/config", s.putStoreConfig},
		{"GET /api/v1/campaigns/{id}", s.getCampaign},
		{"PUT /api/v1/campaigns/{id}", s.putCampaign},
		{"POST /api/v1/calm-hours/offer", s.postOffer},
		{"POST /api/v1/calm-hours/redeem", s.postRedeem},
		{"GET /api/v1/calm-hours/offers", s.getOffers},
		{"POST /api/v1/calm-hours/simulate", s.postSimulate},
		{"GET /api/v1/ad-slots", s.getAdSlots},
		{"POST /api/v1/event-planner", s.postEventPlanner},
		{"POST /api/v1/chat", s.postChat},
	}
	for _, rt := range routes {
		mux.HandleFunc(rt.pattern, recoverer(rt.h))
	}
}

// recoverer turns a panic in one handler into a 500 instead of killing the service.
func recoverer(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				fail(w, 500, fmt.Errorf("internal error: %v", v))
			}
		}()
		h(w, r)
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, code int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

func readJSON(r *http.Request, v any) error {
	return json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(v)
}

func qInt(r *http.Request, k string, def int) int {
	if v, err := strconv.Atoi(r.URL.Query().Get(k)); err == nil {
		return v
	}
	return def
}

func round(v float64, d int) float64 {
	p := math.Pow(10, float64(d))
	return math.Round(v*p) / p
}

func (s *Server) now(ctx context.Context) (db.State, error) { return db.GetState(ctx, s.P) }

func (s *Server) typicalProfile(ctx context.Context) db.Profile {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.typical == nil || time.Since(s.typAt) > 10*time.Minute {
		st, err := s.now(ctx)
		if err == nil {
			if p, err := db.LoadTypical(ctx, s.P, st.ReplayStart); err == nil {
				s.typical, s.typAt = p, time.Now()
			}
		}
	}
	return s.typical
}

// latestRun is the newest forecast run visible at sim time now.
func (s *Server) latestRun(ctx context.Context, now time.Time) (int, time.Time, string, error) {
	var id int
	var asOf time.Time
	var ver string
	err := s.P.QueryRow(ctx, `SELECT id, as_of, model_version FROM forecast_runs WHERE as_of <= $1 ORDER BY as_of DESC, id DESC LIMIT 1`, now).
		Scan(&id, &asOf, &ver)
	return id, asOf.In(db.Berlin), ver, err
}

func (s *Server) proxy(w http.ResponseWriter, path string, body any) {
	b, _ := json.Marshal(body)
	resp, err := http.Post(s.ForecasterURL+path, "application/json", bytes.NewReader(b))
	if err != nil {
		fail(w, 502, err)
		return
	}
	defer resp.Body.Close()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

// ---------------------------------------------------------------- state & clock
func (s *Server) getState(w http.ResponseWriter, r *http.Request) {
	st, err := s.now(r.Context())
	if err != nil {
		fail(w, 500, err)
		return
	}
	out := map[string]any{"mode": st.Mode, "running": st.Running, "speed": st.Speed, "sim_now": st.SimNow,
		"replay_start": st.ReplayStart, "replay_end": st.ReplayEnd}
	if id, asOf, ver, err := s.latestRun(r.Context(), st.SimNow); err == nil {
		out["forecast_run"] = map[string]any{"id": id, "as_of": asOf, "model_version": ver}
	}
	writeJSON(w, out)
}

func (s *Server) postClock(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Action string  `json:"action"` // play | pause | speed | jump | reset | live | replay
		Speed  float64 `json:"speed"`
		To     string  `json:"to"`
	}
	if err := readJSON(r, &req); err != nil {
		fail(w, 400, err)
		return
	}
	ctx := r.Context()
	st, err := s.now(ctx)
	if err != nil {
		fail(w, 500, err)
		return
	}
	upd := map[string]string{}
	switch req.Action {
	case "play":
		upd["running"] = "true"
		if !st.SimNow.Before(st.ReplayEnd) {
			upd["sim_now"] = db.FormatT(st.ReplayStart)
		}
	case "pause":
		upd["running"] = "false"
	case "live": // "now" is the wall clock; data arrives from the sensors (Kafka / LIVE_URL)
		upd["mode"] = "live"
		upd["force_forecast"] = "true"
	case "replay": // back to the recorded week, at the current replay time
		upd["mode"] = "replay"
	case "speed":
		if req.Speed <= 0 || req.Speed > 100000 {
			fail(w, 400, fmt.Errorf("speed out of range"))
			return
		}
		upd["speed"] = strconv.FormatFloat(req.Speed, 'f', -1, 64)
	case "jump", "reset":
		to := st.ReplayStart
		if req.Action == "jump" {
			if to, err = time.Parse(time.RFC3339, req.To); err != nil {
				fail(w, 400, err)
				return
			}
			if to.Before(st.ReplayStart) {
				to = st.ReplayStart
			}
			if to.After(st.ReplayEnd) {
				to = st.ReplayEnd
			}
		}
		// forget everything that "has not happened yet" at the new time
		for _, q := range []string{
			`DELETE FROM forecast_runs WHERE as_of > $1`,
			`DELETE FROM readings WHERE is_estimated AND ts > $1`,
			`DELETE FROM alerts WHERE ts > $1`,
		} {
			if _, err := s.P.Exec(ctx, q, to); err != nil {
				fail(w, 500, err)
				return
			}
		}
		if req.Action == "reset" {
			_, _ = s.P.Exec(ctx, `DELETE FROM offers`)
			_, _ = s.P.Exec(ctx, `DELETE FROM alerts`)
		}
		upd["sim_now"] = db.FormatT(to)
	default:
		fail(w, 400, fmt.Errorf("unknown action %q", req.Action))
		return
	}
	if err := db.SetState(ctx, s.P, upd); err != nil {
		fail(w, 500, err)
		return
	}
	s.getState(w, r)
}

// ---------------------------------------------------------------- sensors
type sensorOut struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Label     string     `json:"label"`
	Lat       float64    `json:"lat"`
	Lon       float64    `json:"lon"`
	Density   *float64   `json:"density"`   // mean of the last hour
	Typical   float64    `json:"typical"`   // normal for this weekday & hour
	VsNormal  *float64   `json:"vs_normal"` // density/typical - 1
	Estimated bool       `json:"estimated"`
	Next3h    *float64   `json:"forecast_next_3h"`
	Health    *db.Health `json:"health"`
}

func (s *Server) getSensors(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	st, err := s.now(ctx)
	if err != nil {
		fail(w, 500, err)
		return
	}
	now := st.SimNow
	rows, err := s.P.Query(ctx, `
		SELECT s.id, s.name, s.label, s.lat, s.lon,
		       (SELECT avg(density) FROM readings WHERE sensor_id = s.id AND ts <= $1 AND ts > $1 - interval '1 hour'),
		       coalesce((SELECT bool_and(is_estimated) FROM readings WHERE sensor_id = s.id AND ts <= $1 AND ts > $1 - interval '1 hour'), false)
		FROM sensors s WHERE s.active ORDER BY s.label`, now)
	if err != nil {
		fail(w, 500, err)
		return
	}
	var out []sensorOut
	for rows.Next() {
		var o sensorOut
		if err := rows.Scan(&o.ID, &o.Name, &o.Label, &o.Lat, &o.Lon, &o.Density, &o.Estimated); err != nil {
			fail(w, 500, err)
			return
		}
		out = append(out, o)
	}
	rows.Close()
	typ := s.typicalProfile(ctx)
	hs, _ := db.SensorHealth(ctx, s.P, now)
	hmap := map[string]*db.Health{}
	for i := range hs {
		hmap[hs[i].SensorID] = &hs[i]
	}
	runID, _, _, rerr := s.latestRun(ctx, now)
	for i := range out {
		o := &out[i]
		if p := typ[o.ID]; p != nil {
			o.Typical = round(p[db.Weekday(now)][now.Hour()], 1)
		}
		if o.Density != nil {
			d := round(*o.Density, 1)
			o.Density = &d
			if o.Typical > 0 {
				v := round(d/o.Typical-1, 3)
				o.VsNormal = &v
			}
		}
		o.Health = hmap[o.ID]
		if rerr == nil {
			var f *float64
			_ = s.P.QueryRow(ctx, `SELECT avg(p50) FROM forecasts WHERE run_id=$1 AND sensor_id=$2 AND ts > $3 AND ts <= $3 + interval '3 hours'`,
				runID, o.ID, now).Scan(&f)
			if f != nil {
				v := round(*f, 1)
				o.Next3h = &v
			}
		}
	}
	writeJSON(w, map[string]any{"sim_now": now, "sensors": out})
}

func (s *Server) getHistory(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	st, err := s.now(ctx)
	if err != nil {
		fail(w, 500, err)
		return
	}
	hours := qInt(r, "hours", 168)
	rows, err := s.P.Query(ctx, `
		SELECT date_trunc('hour', ts) hr, avg(density), bool_and(is_estimated)
		FROM readings WHERE sensor_id = $1 AND ts <= $2 AND ts > $2 - make_interval(hours => $3)
		GROUP BY 1 ORDER BY 1`, r.PathValue("id"), st.SimNow, hours)
	if err != nil {
		fail(w, 500, err)
		return
	}
	defer rows.Close()
	type pt struct {
		TS        time.Time `json:"ts"`
		Density   float64   `json:"density"`
		Estimated bool      `json:"estimated"`
	}
	out := []pt{}
	for rows.Next() {
		var p pt
		if rows.Scan(&p.TS, &p.Density, &p.Estimated) == nil {
			p.TS = p.TS.In(db.Berlin)
			p.Density = round(p.Density, 2)
			out = append(out, p)
		}
	}
	writeJSON(w, out)
}

type fcPoint struct {
	TS      time.Time       `json:"ts"`
	P10     float64         `json:"p10"`
	P50     float64         `json:"p50"`
	P90     float64         `json:"p90"`
	Reasons json.RawMessage `json:"reasons,omitempty"`
}

func (s *Server) loadForecast(ctx context.Context, runID int, sensor string, from time.Time) ([]fcPoint, error) {
	rows, err := s.P.Query(ctx, `SELECT ts, p10, p50, p90, reasons FROM forecasts WHERE run_id=$1 AND sensor_id=$2 AND ts > $3 ORDER BY ts`,
		runID, sensor, from)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []fcPoint{}
	for rows.Next() {
		var p fcPoint
		var reasons []byte
		if err := rows.Scan(&p.TS, &p.P10, &p.P50, &p.P90, &reasons); err != nil {
			return nil, err
		}
		p.TS = p.TS.In(db.Berlin)
		p.P10, p.P50, p.P90 = round(p.P10, 2), round(p.P50, 2), round(p.P90, 2)
		p.Reasons = reasons
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Server) getForecast(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	st, err := s.now(ctx)
	if err != nil {
		fail(w, 500, err)
		return
	}
	runID, asOf, ver, err := s.latestRun(ctx, st.SimNow)
	if err != nil {
		writeJSON(w, map[string]any{"points": []fcPoint{}, "pending": true})
		return
	}
	pts, err := s.loadForecast(ctx, runID, r.PathValue("id"), st.SimNow.Add(-time.Hour))
	if err != nil {
		fail(w, 500, err)
		return
	}
	writeJSON(w, map[string]any{"run_id": runID, "as_of": asOf, "model_version": ver, "points": pts})
}

// getForecastVsActual pairs each past hour with the forecast made at least 6 hours before it.
func (s *Server) getForecastVsActual(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	st, err := s.now(ctx)
	if err != nil {
		fail(w, 500, err)
		return
	}
	hours := qInt(r, "hours", 168)
	rows, err := s.P.Query(ctx, `
		WITH act AS (SELECT date_trunc('hour', ts) hr, avg(density) v FROM readings
		             WHERE sensor_id=$1 AND NOT is_estimated AND ts <= $2 AND ts > $2 - make_interval(hours => $3) GROUP BY 1),
		     fc AS (SELECT DISTINCT ON (f.ts) f.ts, f.p10, f.p50, f.p90 FROM forecasts f JOIN forecast_runs r ON r.id=f.run_id
		            WHERE f.sensor_id=$1 AND f.ts <= $2 AND f.ts > $2 - make_interval(hours => $3) AND r.as_of <= f.ts - interval '6 hours'
		            ORDER BY f.ts, r.as_of DESC)
		SELECT act.hr, act.v, fc.p10, fc.p50, fc.p90 FROM act JOIN fc ON fc.ts = act.hr ORDER BY 1`,
		r.PathValue("id"), st.SimNow, hours)
	if err != nil {
		fail(w, 500, err)
		return
	}
	defer rows.Close()
	type pt struct {
		TS     time.Time `json:"ts"`
		Actual float64   `json:"actual"`
		P10    float64   `json:"p10"`
		P50    float64   `json:"p50"`
		P90    float64   `json:"p90"`
	}
	out := []pt{}
	var sa, se float64
	for rows.Next() {
		var p pt
		if rows.Scan(&p.TS, &p.Actual, &p.P10, &p.P50, &p.P90) == nil {
			p.TS = p.TS.In(db.Berlin)
			sa += p.Actual
			se += math.Abs(p.Actual - p.P50)
			p.Actual, p.P10, p.P50, p.P90 = round(p.Actual, 2), round(p.P10, 2), round(p.P50, 2), round(p.P90, 2)
			out = append(out, p)
		}
	}
	res := map[string]any{"points": out}
	if sa > 0 {
		res["live_wape_pct"] = round(se/sa*100, 1)
	}
	writeJSON(w, res)
}

func (s *Server) postWhatIf(w http.ResponseWriter, r *http.Request) {
	var req map[string]any
	if err := readJSON(r, &req); err != nil {
		fail(w, 400, err)
		return
	}
	st, err := s.now(r.Context())
	if err != nil {
		fail(w, 500, err)
		return
	}
	req["as_of"] = db.FormatT(st.SimNow.Truncate(time.Hour))
	s.proxy(w, "/whatif", req)
}

// ---------------------------------------------------------------- location profiles (store-location use case)
func (s *Server) getCompare(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	st, err := s.now(ctx)
	if err != nil {
		fail(w, 500, err)
		return
	}
	rows, err := s.P.Query(ctx, `
		WITH h AS (SELECT sensor_id, date_trunc('hour', ts) hr, avg(density) v FROM readings
		           WHERE NOT is_estimated AND ts >= $1 AND ts <= $2 GROUP BY 1, 2),
		     x AS (SELECT h.*, extract(hour FROM h.hr AT TIME ZONE 'Europe/Berlin') hod,
		                  extract(isodow FROM h.hr AT TIME ZONE 'Europe/Berlin') dow, w.precip
		           FROM h LEFT JOIN weather w ON w.ts = h.hr AND w.kind = 'actual')
		SELECT s.id, s.label, avg(v),
		       avg(v) FILTER (WHERE dow < 6), avg(v) FILTER (WHERE dow >= 6),
		       avg(v) FILTER (WHERE hod BETWEEN 10 AND 17), avg(v) FILTER (WHERE hod BETWEEN 18 AND 23),
		       avg(v) FILTER (WHERE hod BETWEEN 10 AND 21 AND precip > 0.2), avg(v) FILTER (WHERE hod BETWEEN 10 AND 21 AND precip = 0),
		       max(v)
		FROM x JOIN sensors s ON s.id = x.sensor_id WHERE s.active GROUP BY 1, 2 ORDER BY 3 DESC`, db.TrainStart, st.SimNow)
	if err != nil {
		fail(w, 500, err)
		return
	}
	defer rows.Close()
	type prof struct {
		ID         string  `json:"id"`
		Label      string  `json:"label"`
		Avg        float64 `json:"avg_density"`
		Weekday    float64 `json:"weekday_avg"`
		Weekend    float64 `json:"weekend_avg"`
		Daytime    float64 `json:"daytime_avg"`
		Evening    float64 `json:"evening_avg"`
		RainEffect float64 `json:"rain_effect"`
		Max        float64 `json:"max_density"`
		PeakHour   int     `json:"peak_hour"`
		BestFor    string  `json:"best_for"`
	}
	var out []prof
	for rows.Next() {
		var p prof
		var wet, dry *float64
		if err := rows.Scan(&p.ID, &p.Label, &p.Avg, &p.Weekday, &p.Weekend, &p.Daytime, &p.Evening, &wet, &dry, &p.Max); err != nil {
			fail(w, 500, err)
			return
		}
		if wet != nil && dry != nil && *dry > 0 {
			p.RainEffect = round(*wet / *dry - 1, 3)
		}
		switch {
		case p.Evening > 1.3*p.Daytime:
			p.BestFor = "Evening leisure: bars, food, events"
		case p.Daytime > 1.3*p.Evening:
			p.BestFor = "Daytime retail and services"
		default:
			p.BestFor = "All-day mixed use"
		}
		if p.Weekend > 1.25*p.Weekday {
			p.BestFor += " · weekend destination"
		}
		p.Avg, p.Weekday, p.Weekend, p.Daytime, p.Evening, p.Max =
			round(p.Avg, 1), round(p.Weekday, 1), round(p.Weekend, 1), round(p.Daytime, 1), round(p.Evening, 1), round(p.Max, 1)
		out = append(out, p)
	}
	rows.Close()
	typ := s.typicalProfile(ctx)
	for i := range out {
		if p := typ[out[i].ID]; p != nil {
			best, bv := 0, -1.0
			for h := 0; h < 24; h++ {
				v := 0.0
				for d := 0; d < 7; d++ {
					v += p[d][h]
				}
				if v > bv {
					best, bv = h, v
				}
			}
			out[i].PeakHour = best
		}
	}
	writeJSON(w, out)
}

// ---------------------------------------------------------------- health, alerts, events, model
func (s *Server) getHealth(w http.ResponseWriter, r *http.Request) {
	st, err := s.now(r.Context())
	if err != nil {
		fail(w, 500, err)
		return
	}
	hs, err := db.SensorHealth(r.Context(), s.P, st.SimNow)
	if err != nil {
		fail(w, 500, err)
		return
	}
	writeJSON(w, hs)
}

func (s *Server) getAlerts(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	st, err := s.now(ctx)
	if err != nil {
		fail(w, 500, err)
		return
	}
	rows, err := s.P.Query(ctx, `SELECT a.id, a.kind, a.sensor_id, s.label, a.ts, a.title, a.detail, a.payload, a.status
		FROM alerts a LEFT JOIN sensors s ON s.id = a.sensor_id
		WHERE a.ts <= $1 AND a.status <> 'resolved' ORDER BY (a.status = 'open') DESC, a.ts DESC LIMIT 100`, st.SimNow)
	if err != nil {
		fail(w, 500, err)
		return
	}
	defer rows.Close()
	type al struct {
		ID      int             `json:"id"`
		Kind    string          `json:"kind"`
		Sensor  *string         `json:"sensor_id"`
		Label   *string         `json:"label"`
		TS      time.Time       `json:"ts"`
		Title   string          `json:"title"`
		Detail  *string         `json:"detail"`
		Payload json.RawMessage `json:"payload"`
		Status  string          `json:"status"`
	}
	out := []al{}
	for rows.Next() {
		var a al
		var pl []byte
		if rows.Scan(&a.ID, &a.Kind, &a.Sensor, &a.Label, &a.TS, &a.Title, &a.Detail, &pl, &a.Status) == nil {
			a.TS = a.TS.In(db.Berlin)
			a.Payload = pl
			out = append(out, a)
		}
	}
	writeJSON(w, out)
}

func (s *Server) patchAlert(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Status string `json:"status"` // ack | labelled
		Label  string `json:"label"`
	}
	if err := readJSON(r, &req); err != nil {
		fail(w, 400, err)
		return
	}
	ctx := r.Context()
	id := r.PathValue("id")
	if req.Label != "" {
		var payload []byte
		if err := s.P.QueryRow(ctx, `SELECT payload FROM alerts WHERE id=$1 AND kind='anomaly'`, id).Scan(&payload); err != nil {
			fail(w, 404, fmt.Errorf("anomaly alert not found"))
			return
		}
		var p struct {
			SensorIDs []string `json:"sensor_ids"`
			Start     string   `json:"start"`
			Ratio     float64  `json:"ratio"`
		}
		_ = json.Unmarshal(payload, &p)
		start, _ := time.Parse(time.RFC3339, p.Start)
		size := "S"
		if p.Ratio >= 3 {
			size = "L"
		} else if p.Ratio >= 1.8 {
			size = "M"
		}
		if _, err := s.P.Exec(ctx, `INSERT INTO events (name, category, start_ts, end_ts, sensor_ids, size, source)
			VALUES ($1,'event',$2,$3,$4,$5,'labelled')`, req.Label, start, start.Add(6*time.Hour), p.SensorIDs, size); err != nil {
			fail(w, 500, err)
			return
		}
		req.Status = "labelled"
	}
	if req.Status == "" {
		req.Status = "ack"
	}
	if _, err := s.P.Exec(ctx, `UPDATE alerts SET status=$2 WHERE id=$1`, id, req.Status); err != nil {
		fail(w, 500, err)
		return
	}
	writeJSON(w, map[string]string{"status": req.Status})
}

type event struct {
	ID        int       `json:"id"`
	Name      string    `json:"name"`
	Category  string    `json:"category"`
	Start     time.Time `json:"start"`
	End       time.Time `json:"end"`
	SensorIDs []string  `json:"sensor_ids"`
	Size      *string   `json:"size"`
	Profile   *string   `json:"profile"`
	Recurring bool      `json:"recurring"`
	Source    string    `json:"source"`
}

func (s *Server) getEvents(w http.ResponseWriter, r *http.Request) {
	rows, err := s.P.Query(r.Context(), `SELECT id, name, category, start_ts, end_ts, sensor_ids, size, profile, recurring, source
		FROM events ORDER BY start_ts DESC`)
	if err != nil {
		fail(w, 500, err)
		return
	}
	defer rows.Close()
	out := []event{}
	for rows.Next() {
		var e event
		if rows.Scan(&e.ID, &e.Name, &e.Category, &e.Start, &e.End, &e.SensorIDs, &e.Size, &e.Profile, &e.Recurring, &e.Source) == nil {
			e.Start, e.End = e.Start.In(db.Berlin), e.End.In(db.Berlin)
			out = append(out, e)
		}
	}
	writeJSON(w, out)
}

func (s *Server) postEvent(w http.ResponseWriter, r *http.Request) {
	var e event
	if err := readJSON(r, &e); err != nil {
		fail(w, 400, err)
		return
	}
	if e.Name == "" || len(e.SensorIDs) == 0 || !e.End.After(e.Start) {
		fail(w, 400, fmt.Errorf("name, sensor_ids and start < end required"))
		return
	}
	if e.Category == "" {
		e.Category = "event"
	}
	err := s.P.QueryRow(r.Context(), `INSERT INTO events (name, category, start_ts, end_ts, sensor_ids, size, profile, recurring, source)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,'manual') RETURNING id`, e.Name, e.Category, e.Start, e.End, e.SensorIDs, e.Size, e.Profile, e.Recurring).Scan(&e.ID)
	if err != nil {
		fail(w, 500, err)
		return
	}
	_ = db.SetState(r.Context(), s.P, map[string]string{"force_forecast": "true"}) // worker re-forecasts with the event layer
	writeJSON(w, e)
}

func (s *Server) deleteEvent(w http.ResponseWriter, r *http.Request) {
	if _, err := s.P.Exec(r.Context(), `DELETE FROM events WHERE id=$1`, r.PathValue("id")); err != nil {
		fail(w, 500, err)
		return
	}
	_ = db.SetState(r.Context(), s.P, map[string]string{"force_forecast": "true"})
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) getModel(w http.ResponseWriter, r *http.Request) {
	rows, err := s.P.Query(r.Context(), `SELECT model_version, metric, scope, value FROM model_metrics ORDER BY 1, 2, 3`)
	if err != nil {
		fail(w, 500, err)
		return
	}
	defer rows.Close()
	type m struct {
		Version string  `json:"model_version"`
		Metric  string  `json:"metric"`
		Scope   string  `json:"scope"`
		Value   float64 `json:"value"`
	}
	out := []m{}
	for rows.Next() {
		var x m
		if rows.Scan(&x.Version, &x.Metric, &x.Scope, &x.Value) == nil {
			x.Value = round(x.Value, 2)
			out = append(out, x)
		}
	}
	res := map[string]any{"metrics": out}
	if resp, err := http.Get(s.ForecasterURL + "/model"); err == nil {
		var info map[string]any
		if json.NewDecoder(resp.Body).Decode(&info) == nil {
			delete(info, "evaluation")
			res["model"] = info
		}
		resp.Body.Close()
	}
	writeJSON(w, res)
}
