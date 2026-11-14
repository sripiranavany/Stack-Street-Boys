package db

import (
	"context"
	"math"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Health is the maintenance view of one sensor at sim time `now`.
type Health struct {
	SensorID       string    `json:"sensor_id"`
	Label          string    `json:"label"`
	LastSeen       time.Time `json:"last_seen"`
	MinutesSilent  float64   `json:"minutes_silent"`
	Online         bool      `json:"online"`
	Battery        float64   `json:"battery"`
	DrainPerDay    float64   `json:"battery_drain_per_day"`
	DaysToCritical *float64  `json:"days_to_critical"` // days until battery < 10 %
	Risk           string    `json:"risk"`             // ok | watch | critical | offline
}

// OfflineAfter: three missed 15-minute cycles.
const OfflineAfter = 45 * time.Minute

// SensorHealth computes health for all active sensors using only data up to now.
func SensorHealth(ctx context.Context, p *pgxpool.Pool, now time.Time) ([]Health, error) {
	rows, err := p.Query(ctx, `
		SELECT s.id, s.label, l.ts, coalesce(l.battery, 0),
		       coalesce((SELECT regr_slope(battery, extract(epoch FROM ts)) FROM readings
		                 WHERE sensor_id = s.id AND NOT is_estimated AND battery IS NOT NULL
		                   AND ts <= $1 AND ts > $1 - interval '14 days'), 0)
		FROM sensors s
		LEFT JOIN LATERAL (SELECT ts, battery FROM readings WHERE sensor_id = s.id AND NOT is_estimated AND ts <= $1
		                   ORDER BY ts DESC LIMIT 1) l ON true
		WHERE s.active ORDER BY s.label`, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Health
	for rows.Next() {
		var h Health
		var last *time.Time
		var slope float64
		if err := rows.Scan(&h.SensorID, &h.Label, &last, &h.Battery, &slope); err != nil {
			return nil, err
		}
		if last != nil {
			h.LastSeen = last.In(Berlin)
			h.MinutesSilent = math.Round(now.Sub(*last).Minutes())
		}
		h.Online = last != nil && now.Sub(*last) <= OfflineAfter
		h.DrainPerDay = math.Round(-slope*86400*100) / 100
		if h.DrainPerDay > 0.01 && h.Battery > 10 {
			d := math.Round((h.Battery-10)/h.DrainPerDay*10) / 10
			h.DaysToCritical = &d
		}
		switch {
		case !h.Online:
			h.Risk = "offline"
		case h.Battery <= 10:
			h.Risk = "critical"
		case h.Battery <= 20 || (h.DaysToCritical != nil && *h.DaysToCritical < 7):
			h.Risk = "watch"
		default:
			h.Risk = "ok"
		}
		out = append(out, h)
	}
	return out, rows.Err()
}
