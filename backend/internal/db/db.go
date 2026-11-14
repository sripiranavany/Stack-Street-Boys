// Package db holds the Postgres pool, app state (sim clock) and cached reference profiles.
package db

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "time/tzdata"
)

// Berlin is the local time zone of all sensors.
var Berlin, _ = time.LoadLocation("Europe/Berlin")

// TrainStart is the first day all sensors deliver data.
var TrainStart = time.Date(2026, 6, 22, 0, 0, 0, 0, Berlin)

// Connect opens a pool, retrying until the database is reachable.
func Connect(ctx context.Context, dsn string) *pgxpool.Pool {
	for i := 0; ; i++ {
		pool, err := pgxpool.New(ctx, dsn)
		if err == nil {
			if err = pool.Ping(ctx); err == nil {
				return pool
			}
			pool.Close()
		}
		if i%10 == 0 {
			log.Printf("waiting for database: %v", err)
		}
		time.Sleep(2 * time.Second)
	}
}

// State is the replay / live clock and settings stored in app_state.
type State struct {
	Mode        string            `json:"mode"`
	Running     bool              `json:"running"`
	Speed       float64           `json:"speed"`
	SimNow      time.Time         `json:"sim_now"`
	ReplayStart time.Time         `json:"replay_start"`
	ReplayEnd   time.Time         `json:"replay_end"`
	Raw         map[string]string `json:"-"`
}

func GetState(ctx context.Context, p *pgxpool.Pool) (State, error) {
	rows, err := p.Query(ctx, "SELECT key, value FROM app_state")
	if err != nil {
		return State{}, err
	}
	defer rows.Close()
	raw := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return State{}, err
		}
		raw[k] = v
	}
	s := State{Mode: raw["mode"], Running: raw["running"] == "true", Raw: raw}
	s.Speed, _ = strconv.ParseFloat(raw["speed"], 64)
	s.SimNow = parseT(raw["sim_now"])
	s.ReplayStart = parseT(raw["replay_start"])
	s.ReplayEnd = parseT(raw["replay_end"])
	if s.Mode == "live" {
		s.SimNow = time.Now().In(Berlin)
	}
	return s, nil
}

func parseT(v string) time.Time {
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return time.Time{}
	}
	return t.In(Berlin)
}

func SetState(ctx context.Context, p *pgxpool.Pool, kv map[string]string) error {
	for k, v := range kv {
		if _, err := p.Exec(ctx, `INSERT INTO app_state VALUES ($1,$2) ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`, k, v); err != nil {
			return err
		}
	}
	return nil
}

func FormatT(t time.Time) string { return t.In(Berlin).Format(time.RFC3339) }

// FormatClock keeps sub-second precision for the replay clock. The worker advances it several times a second;
// with whole seconds every small step was rounded away and the clock ran slower than its speed setting.
func FormatClock(t time.Time) string { return t.In(Berlin).Format(time.RFC3339Nano) }

// Profile is a per sensor weekday(0=Mon) x hour table.
type Profile map[string]*[7][24]float64

// LoadTypical returns the median hourly density per sensor x weekday x hour over the 5 weeks before `before`
// ("normal" reference: recent, so it carries the current season).
func LoadTypical(ctx context.Context, p *pgxpool.Pool, before time.Time) (Profile, error) {
	rows, err := p.Query(ctx, `
		WITH h AS (
		  SELECT sensor_id, date_trunc('hour', ts AT TIME ZONE 'Europe/Berlin') AS hr, avg(density) AS v
		  FROM readings WHERE NOT is_estimated AND ts >= $1 AND ts < $2 GROUP BY 1, 2)
		SELECT sensor_id, (extract(isodow FROM hr)::int - 1) AS dow, extract(hour FROM hr)::int AS hod,
		       percentile_cont(0.5) WITHIN GROUP (ORDER BY v)
		FROM h GROUP BY 1, 2, 3`, before.AddDate(0, 0, -35), before)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := Profile{}
	for rows.Next() {
		var s string
		var d, h int
		var v float64
		if err := rows.Scan(&s, &d, &h, &v); err != nil {
			return nil, err
		}
		if out[s] == nil {
			out[s] = &[7][24]float64{}
		}
		out[s][d][h] = v
	}
	return out, rows.Err()
}

// LoadNeighborRatio returns median(sensor / neighbour) hourly density per weekday x hour, used for gap-filling.
func LoadNeighborRatio(ctx context.Context, p *pgxpool.Pool, before time.Time) (Profile, error) {
	rows, err := p.Query(ctx, `
		WITH h AS (
		  SELECT sensor_id, date_trunc('hour', ts AT TIME ZONE 'Europe/Berlin') AS hr, avg(density) AS v
		  FROM readings WHERE NOT is_estimated AND ts >= $1 AND ts < $2 GROUP BY 1, 2)
		SELECT s.id, (extract(isodow FROM a.hr)::int - 1), extract(hour FROM a.hr)::int,
		       percentile_cont(0.5) WITHIN GROUP (ORDER BY (a.v + 1) / (b.v + 1))
		FROM sensors s JOIN h a ON a.sensor_id = s.id JOIN h b ON b.sensor_id = s.neighbor_id AND b.hr = a.hr
		WHERE s.active GROUP BY 1, 2, 3`, TrainStart, before)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := Profile{}
	for rows.Next() {
		var s string
		var d, h int
		var v float64
		if err := rows.Scan(&s, &d, &h, &v); err != nil {
			return nil, err
		}
		if out[s] == nil {
			out[s] = &[7][24]float64{}
		}
		out[s][d][h] = v
	}
	return out, rows.Err()
}

// Weekday converts Go's Sunday=0 to Monday=0.
func Weekday(t time.Time) int { return (int(t.In(Berlin).Weekday()) + 6) % 7 }

// MustEnv returns an env-style default.
func Or(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

var _ = fmt.Sprintf
