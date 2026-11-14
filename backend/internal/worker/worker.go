// Package worker runs Ebb's background loop: sim clock, forecast runs, gap-fill, sensor health and alerts.
package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"ssb-backend/internal/db"
)

type Worker struct {
	P             *pgxpool.Pool
	ForecasterURL string
	LiveURL       string // optional live PAX feed (JSON array of {sensor_id,time,density,battery})

	ratio      db.Profile
	busy       atomic.Bool
	lastHour   time.Time
	lastTick   time.Time
	lastHealth time.Time
	lastLive   time.Time
	client     *http.Client
}

func (w *Worker) Run(ctx context.Context) {
	w.client = &http.Client{Timeout: 120 * time.Second}
	st, err := db.GetState(ctx, w.P)
	if err != nil {
		log.Printf("worker: state not readable yet: %v", err)
	}
	if w.ratio, err = db.LoadNeighborRatio(ctx, w.P, st.ReplayStart); err != nil {
		log.Printf("neighbour ratios: %v", err)
	}
	w.lastTick = time.Now()
	t := time.NewTicker(time.Second)
	defer t.Stop()
	log.Printf("worker started: mode=%s sim_now=%s speed=%v", st.Mode, st.SimNow, st.Speed)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := w.tick(ctx); err != nil {
				log.Printf("tick: %v", err)
			}
		}
	}
}

func (w *Worker) tick(ctx context.Context) error {
	st, err := db.GetState(ctx, w.P)
	if err != nil {
		return err
	}
	if st.SimNow.IsZero() { // not seeded yet: nothing to do
		w.lastTick = time.Now()
		return nil
	}
	real := time.Since(w.lastTick)
	w.lastTick = time.Now()
	now := st.SimNow
	if st.Mode == "replay" && st.Running {
		now = now.Add(time.Duration(float64(real) * st.Speed))
		upd := map[string]string{"sim_now": db.FormatClock(now)}
		if !now.Before(st.ReplayEnd) {
			now = st.ReplayEnd
			upd = map[string]string{"sim_now": db.FormatClock(now), "running": "false"}
		}
		if err := db.SetState(ctx, w.P, upd); err != nil {
			return err
		}
	}
	if st.Mode == "live" && w.LiveURL != "" && time.Since(w.lastLive) > time.Minute {
		w.lastLive = time.Now()
		w.pollLive(ctx)
	}

	// forecast every 6 simulated hours (or when forced, e.g. after a new event)
	b := floor6h(now)
	if st.Raw["force_forecast"] == "true" {
		_ = db.SetState(ctx, w.P, map[string]string{"force_forecast": "false"})
		w.predict(ctx, now.Truncate(time.Hour))
	} else {
		var exists bool
		_ = w.P.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM forecast_runs WHERE as_of = $1)", b).Scan(&exists)
		if !exists {
			w.predict(ctx, b)
		}
	}

	// hourly jobs
	h := now.Truncate(time.Hour)
	if !h.Equal(w.lastHour) {
		w.lastHour = h
		w.gapFill(ctx, h)
		w.anomalies(ctx, h)
	}
	if time.Since(w.lastHealth) > 3*time.Second {
		w.lastHealth = time.Now()
		w.health(ctx, now)
	}
	return nil
}

func floor6h(t time.Time) time.Time {
	t = t.In(db.Berlin)
	return time.Date(t.Year(), t.Month(), t.Day(), t.Hour()/6*6, 0, 0, 0, db.Berlin)
}

func (w *Worker) predict(ctx context.Context, asOf time.Time) {
	if !w.busy.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer w.busy.Store(false)
		body, _ := json.Marshal(map[string]any{"as_of": db.FormatT(asOf)})
		resp, err := w.client.Post(w.ForecasterURL+"/predict", "application/json", bytes.NewReader(body))
		if err != nil {
			log.Printf("predict: %v", err)
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			log.Printf("predict %s: status %d (model not ready?)", asOf.Format(time.RFC3339), resp.StatusCode)
			return
		}
		var r struct {
			RunID int `json:"run_id"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&r)
		log.Printf("forecast run %d as of %s", r.RunID, asOf.Format("2006-01-02 15:04"))
		w.prezero(context.Background(), asOf, r.RunID)
	}()
}

// gapFill estimates the previous hour for sensors that sent nothing, from their nearest neighbour.
func (w *Worker) gapFill(ctx context.Context, h time.Time) {
	from := h.Add(-time.Hour)
	rows, err := w.P.Query(ctx, `
		SELECT s.id, s.neighbor_id,
		       (SELECT avg(density) FROM readings WHERE sensor_id = s.neighbor_id AND NOT is_estimated AND ts >= $1 AND ts < $2)
		FROM sensors s
		WHERE s.active AND NOT EXISTS (SELECT 1 FROM readings WHERE sensor_id = s.id AND ts >= $1 AND ts < $2)`, from, h)
	if err != nil {
		log.Printf("gapfill: %v", err)
		return
	}
	type est struct {
		id string
		v  float64
	}
	var ests []est
	for rows.Next() {
		var id, nb string
		var nv *float64
		if rows.Scan(&id, &nb, &nv) != nil || nv == nil {
			continue
		}
		ratio := 1.0
		if r := w.ratio[id]; r != nil {
			if x := r[db.Weekday(from)][from.In(db.Berlin).Hour()]; x > 0 {
				ratio = x
			}
		}
		ests = append(ests, est{id, math.Max(0, (*nv+1)*ratio-1)})
	}
	rows.Close()
	for _, e := range ests {
		_, _ = w.P.Exec(ctx, `INSERT INTO readings (sensor_id, ts, density, source, is_estimated) VALUES ($1,$2,$3,'estimate',true)
		                      ON CONFLICT DO NOTHING`, e.id, from.Add(30*time.Minute), e.v)
	}
}

func (w *Worker) health(ctx context.Context, now time.Time) {
	hs, err := db.SensorHealth(ctx, w.P, now)
	if err != nil {
		log.Printf("health: %v", err)
		return
	}
	for _, h := range hs {
		if !h.Online && !h.LastSeen.IsZero() {
			w.alert(ctx, "offline", h.SensorID, now, fmt.Sprintf("offline:%s:%d", h.SensorID, h.LastSeen.Unix()),
				fmt.Sprintf("%s is offline", h.Label),
				fmt.Sprintf("No reading since %s (battery %.0f%%). Values are being estimated from the neighbouring sensor.",
					h.LastSeen.Format("Mon 15:04"), h.Battery), h)
		} else if h.Online {
			_, _ = w.P.Exec(ctx, `UPDATE alerts SET status='resolved' WHERE kind='offline' AND sensor_id=$1 AND status='open'`, h.SensorID)
		}
		if h.Online && (h.Risk == "critical" || h.Risk == "watch") {
			detail := fmt.Sprintf("Battery at %.0f%%, draining %.1f%%/day.", h.Battery, h.DrainPerDay)
			if h.DaysToCritical != nil {
				detail += fmt.Sprintf(" Reaches 10%% in ~%.0f days.", *h.DaysToCritical)
			}
			detail += " Below 10% sensors drop out ~5x more often: schedule a battery swap."
			w.alert(ctx, "battery", h.SensorID, now, "battery:"+h.SensorID+":"+h.Risk,
				fmt.Sprintf("%s battery %s", h.Label, h.Risk), detail, h)
		}
	}
}

// anomalies flags hours where >= 2 sensors run far above their forecast for 2 consecutive hours, with no known event.
func (w *Worker) anomalies(ctx context.Context, h time.Time) {
	from := h.Add(-2 * time.Hour)
	rows, err := w.P.Query(ctx, `
		WITH act AS (SELECT sensor_id, date_trunc('hour', ts) hr, avg(density) v FROM readings
		             WHERE NOT is_estimated AND ts >= $1 AND ts < $2 GROUP BY 1, 2),
		     fc AS (SELECT DISTINCT ON (f.sensor_id, f.ts) f.sensor_id, f.ts, f.p50, f.p90
		            FROM forecasts f JOIN forecast_runs r ON r.id = f.run_id
		            WHERE f.ts >= $1 AND f.ts < $2 AND r.as_of <= f.ts - interval '1 hour'
		            ORDER BY f.sensor_id, f.ts, r.as_of DESC)
		SELECT a.sensor_id, count(*), avg(a.v / greatest(fc.p50, 1))
		FROM act a JOIN fc ON fc.sensor_id = a.sensor_id AND fc.ts = a.hr
		WHERE a.v > 1.5 * fc.p90 AND a.v > 10
		GROUP BY 1 HAVING count(*) = 2`, from, h)
	if err != nil {
		log.Printf("anomaly: %v", err)
		return
	}
	var ids []string
	maxRatio := 0.0
	for rows.Next() {
		var id string
		var n int
		var r float64
		if rows.Scan(&id, &n, &r) == nil {
			ids = append(ids, id)
			maxRatio = math.Max(maxRatio, r)
		}
	}
	rows.Close()
	if len(ids) < 2 {
		return
	}
	var known bool
	_ = w.P.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM events WHERE start_ts <= $1 AND end_ts > $2)`, h, from).Scan(&known)
	if known {
		return
	}
	key := fmt.Sprintf("anomaly:%s:%d", from.Format("2006-01-02"), from.In(db.Berlin).Hour()/4)
	w.alert(ctx, "anomaly", "", h, key, "Unexplained crowd spike",
		fmt.Sprintf("%d locations at up to %.1fx their forecast since %s. Label it so Ebb can plan for it next time.",
			len(ids), maxRatio, from.In(db.Berlin).Format("Mon 15:04")),
		map[string]any{"sensor_ids": ids, "start": db.FormatT(from), "ratio": maxRatio})
}

// prezero raises "extra collection" alerts when a forecast day runs well above the recent normal for that weekday.
func (w *Worker) prezero(ctx context.Context, asOf time.Time, runID int) {
	rows, err := w.P.Query(ctx, `
		WITH fc AS (SELECT f.sensor_id, (f.ts AT TIME ZONE 'Europe/Berlin')::date d, sum(f.p50) s, count(*) n
		            FROM forecasts f WHERE f.run_id = $1 GROUP BY 1, 2 HAVING count(*) = 24),
		     act AS (SELECT sensor_id, (ts AT TIME ZONE 'Europe/Berlin')::date d, avg(density) * 24 s
		             FROM readings WHERE NOT is_estimated AND ts <= $2 AND ts > $2 - interval '28 days' GROUP BY 1, 2),
		     norm AS (SELECT sensor_id, extract(isodow FROM d) dow, percentile_cont(0.5) WITHIN GROUP (ORDER BY s) s
		              FROM act GROUP BY 1, 2)
		SELECT fc.sensor_id, s.label, fc.d, fc.s / greatest(norm.s, 1)
		FROM fc JOIN norm ON norm.sensor_id = fc.sensor_id AND norm.dow = extract(isodow FROM fc.d)
		JOIN sensors s ON s.id = fc.sensor_id
		WHERE fc.s > 1.3 * norm.s`, runID, asOf)
	if err != nil {
		log.Printf("prezero: %v", err)
		return
	}
	defer rows.Close()
	type a struct {
		id, label string
		d         time.Time
		r         float64
	}
	var as []a
	for rows.Next() {
		var x a
		if rows.Scan(&x.id, &x.label, &x.d, &x.r) == nil {
			as = append(as, x)
		}
	}
	for _, x := range as {
		day := time.Date(x.d.Year(), x.d.Month(), x.d.Day(), 0, 0, 0, 0, db.Berlin)
		next := day.AddDate(0, 0, 1).Add(6 * time.Hour)
		w.alert(ctx, "prezero", x.id, asOf, fmt.Sprintf("prezero:%s:%s", x.id, day.Format("2006-01-02")),
			fmt.Sprintf("Extra bin collection: %s", x.label),
			fmt.Sprintf("%s is forecast at +%.0f%% vs a normal %s. Schedule a collection %s.",
				x.label, (x.r-1)*100, day.Format("Monday"), next.Format("Mon 02 Jan 15:04")),
			map[string]any{"date": day.Format("2006-01-02"), "ratio": x.r, "collect_at": db.FormatT(next)})
	}
}

func (w *Worker) alert(ctx context.Context, kind, sensor string, ts time.Time, key, title, detail string, payload any) {
	pj, _ := json.Marshal(payload)
	var sid any = sensor
	if sensor == "" {
		sid = nil
	}
	_, err := w.P.Exec(ctx, `INSERT INTO alerts (kind, sensor_id, ts, title, detail, payload, dedupe_key)
	                        VALUES ($1,$2,$3,$4,$5,$6,$7) ON CONFLICT (dedupe_key) DO NOTHING`,
		kind, sid, ts, title, detail, pj, key)
	if err != nil {
		log.Printf("alert: %v", err)
	}
}

func (w *Worker) pollLive(ctx context.Context) {
	resp, err := w.client.Get(w.LiveURL)
	if err != nil {
		log.Printf("live: %v", err)
		return
	}
	defer resp.Body.Close()
	var rs []struct {
		SensorID string    `json:"sensor_id"`
		Time     time.Time `json:"time"`
		Density  float64   `json:"density"`
		Battery  *float64  `json:"battery"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rs); err != nil {
		log.Printf("live decode: %v", err)
		return
	}
	for _, r := range rs {
		_, _ = w.P.Exec(ctx, `INSERT INTO readings (sensor_id, ts, density, battery, source) VALUES ($1,$2,$3,$4,'live')
		                      ON CONFLICT DO NOTHING`, r.SensorID, r.Time, r.Density, r.Battery)
	}
}
