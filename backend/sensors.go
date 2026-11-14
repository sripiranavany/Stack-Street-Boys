package main

import (
	"net/http"
	"time"
)

const week = 7 * 24 * time.Hour

// ---------- GET /api/sensors ----------

type sensorOut struct {
	ID          string     `json:"sensor_id"`
	Name        string     `json:"name"`
	Latitude    float64    `json:"latitude"`
	Longitude   float64    `json:"longitude"`
	FirstSeen   *time.Time `json:"first_seen"`
	LastSeen    *time.Time `json:"last_seen"`
	IsTest      bool       `json:"is_test"`
	LatestValue *float64   `json:"latest_value"`
	LatestTime  *time.Time `json:"latest_time"`
	// Busyness is the latest value divided by the sensor's own 95th percentile
	// over the 14 days before it. 1.0 means "as busy as it usually gets".
	Busyness *float64 `json:"busyness"`
}

func (a *app) listSensors(w http.ResponseWriter, r *http.Request) error {
	includeTest := parseBool(r.URL.Query().Get("include_test"), false)

	rows, err := a.db.Query(r.Context(), `
		SELECT s.sensor_id, s.name, s.latitude, s.longitude, s.first_seen, s.last_seen, s.is_test,
		       d.time, d.value, p.p95
		FROM sensors s
		LEFT JOIN LATERAL (
			SELECT time, value FROM readings
			WHERE sensor_id = s.sensor_id AND type = 'densityData'
			  AND value IS NOT NULL AND quality_flag = 0
			ORDER BY time DESC LIMIT 1
		) d ON true
		LEFT JOIN LATERAL (
			SELECT percentile_cont(0.95) WITHIN GROUP (ORDER BY value) AS p95
			FROM readings
			WHERE sensor_id = s.sensor_id AND type = 'densityData'
			  AND value IS NOT NULL AND quality_flag = 0
			  AND time > d.time - interval '14 days' AND time <= d.time
		) p ON d.time IS NOT NULL
		WHERE $1 OR NOT s.is_test
		ORDER BY s.name`, includeTest)
	if err != nil {
		return err
	}
	defer rows.Close()

	out := []sensorOut{}
	for rows.Next() {
		var s sensorOut
		var p95 *float64
		if err := rows.Scan(&s.ID, &s.Name, &s.Latitude, &s.Longitude, &s.FirstSeen, &s.LastSeen,
			&s.IsTest, &s.LatestTime, &s.LatestValue, &p95); err != nil {
			return err
		}
		if s.LatestValue != nil && p95 != nil && *p95 > 0 {
			b := *s.LatestValue / *p95
			s.Busyness = &b
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

// ---------- GET /api/sensors/{id}/timeseries ----------

const maxPoints = 100000

func (a *app) timeseries(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	id := r.PathValue("id")
	q := r.URL.Query()

	metric, err := parseMetric(q.Get("type"))
	if err != nil {
		return err
	}
	aggName := q.Get("agg")
	if aggName == "" {
		aggName = "15m"
	}
	ag, ok := aggs[aggName]
	if !ok {
		return badRequest("agg must be 15m, 1h or 1d")
	}
	normalize := parseBool(q.Get("normalize"), false)
	fill := parseBool(q.Get("fill"), true)

	from, to, err := a.timeRange(r, week)
	if err != nil {
		return err
	}
	if to.Sub(from)/ag.step > maxPoints {
		return badRequest("range too long for agg=%s (max %d points); use a larger agg or a shorter range", aggName, maxPoints)
	}
	name, err := a.sensorName(ctx, id)
	if err != nil {
		return err
	}

	rows, err := a.db.Query(ctx, `
		SELECT time_bucket($1::text::interval, time) AS bucket, avg(value), count(value)
		FROM readings
		WHERE sensor_id = $2 AND type = $3::text::metric_type
		  AND time >= $4 AND time < $5
		  AND quality_flag = 0 AND value IS NOT NULL
		GROUP BY bucket
		ORDER BY bucket`, ag.sql, id, metric, from, to)
	if err != nil {
		return err
	}
	defer rows.Close()

	got := map[int64]point{}
	var ordered []point
	var values []float64
	for rows.Next() {
		var t time.Time
		var avg *float64
		var n int64
		if err := rows.Scan(&t, &avg, &n); err != nil {
			return err
		}
		p := point{Time: t, Value: avg, N: int(n)}
		got[t.Unix()] = p
		ordered = append(ordered, p)
		if avg != nil {
			values = append(values, *avg)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	points := ordered
	if fill {
		points = fillBuckets(from, to, ag.step, got)
	}
	if points == nil {
		points = []point{}
	}

	// normalize=true: divide by the sensor's own 95th percentile in this range,
	// so sensors with different sizes can be compared on one chart.
	var scale *float64
	if normalize && len(values) > 0 {
		s := percentile(values, 0.95)
		if s > 0 {
			scale = &s
			for i := range points {
				if points[i].Value != nil {
					n := *points[i].Value / s
					points[i].Normalized = &n
				}
			}
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"sensor_id": id,
		"name":      name,
		"type":      metric,
		"agg":       aggName,
		"from":      from,
		"to":        to,
		"normalize": normalize,
		"scale_p95": scale,
		"points":    points,
	})
	return nil
}

// ---------- GET /api/heatmap ----------

type heatPoint struct {
	SensorID  string  `json:"sensor_id"`
	Name      string  `json:"name"`
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	Value     float64 `json:"value"`
	// Intensity is the value divided by the sensor's own 95th percentile in the range,
	// capped at 1, so every sensor can be drawn on the same 0..1 colour scale.
	Intensity float64 `json:"intensity"`
}

type heatFrame struct {
	Time   time.Time   `json:"time"`
	Points []heatPoint `json:"points"`
}

func (a *app) heatmap(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	q := r.URL.Query()

	agg := q.Get("agg")
	var interval string
	var maxSpan time.Duration
	switch agg {
	case "", "hourly":
		agg, interval, maxSpan = "hourly", "1 hour", 31*24*time.Hour
	case "daily":
		interval, maxSpan = "1 day", 366*24*time.Hour
	default:
		return badRequest("agg must be hourly or daily")
	}
	includeTest := parseBool(q.Get("include_test"), false)

	from, to, err := a.timeRange(r, week)
	if err != nil {
		return err
	}
	if to.Sub(from) > maxSpan {
		return badRequest("range too long for agg=%s (max %d days)", agg, int(maxSpan.Hours()/24))
	}

	// sensors (small table)
	type meta struct {
		name     string
		lat, lon float64
	}
	metas := map[string]meta{}
	srows, err := a.db.Query(ctx, `SELECT sensor_id, name, latitude, longitude FROM sensors WHERE $1 OR NOT is_test`, includeTest)
	if err != nil {
		return err
	}
	for srows.Next() {
		var id string
		var m meta
		if err := srows.Scan(&id, &m.name, &m.lat, &m.lon); err != nil {
			srows.Close()
			return err
		}
		metas[id] = m
	}
	srows.Close()
	if err := srows.Err(); err != nil {
		return err
	}

	rows, err := a.db.Query(ctx, `
		SELECT time_bucket($1::text::interval, time) AS bucket, sensor_id, avg(value)
		FROM readings
		WHERE type = 'densityData' AND quality_flag = 0 AND value IS NOT NULL
		  AND time >= $2 AND time < $3
		GROUP BY bucket, sensor_id
		ORDER BY bucket`, interval, from, to)
	if err != nil {
		return err
	}
	defer rows.Close()

	type cell struct {
		t   time.Time
		id  string
		val float64
	}
	var cells []cell
	perSensor := map[string][]float64{}
	for rows.Next() {
		var c cell
		if err := rows.Scan(&c.t, &c.id, &c.val); err != nil {
			return err
		}
		if _, ok := metas[c.id]; !ok {
			continue // test sensor or unknown sensor
		}
		cells = append(cells, c)
		perSensor[c.id] = append(perSensor[c.id], c.val)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	scale := map[string]float64{}
	for id, vals := range perSensor {
		scale[id] = percentile(vals, 0.95)
	}

	frames := []heatFrame{}
	for _, c := range cells {
		if len(frames) == 0 || !frames[len(frames)-1].Time.Equal(c.t) {
			frames = append(frames, heatFrame{Time: c.t, Points: []heatPoint{}})
		}
		m := metas[c.id]
		intensity := 0.0
		if s := scale[c.id]; s > 0 {
			intensity = c.val / s
			if intensity > 1 {
				intensity = 1
			}
		}
		f := &frames[len(frames)-1]
		f.Points = append(f.Points, heatPoint{
			SensorID: c.id, Name: m.name, Latitude: m.lat, Longitude: m.lon,
			Value: c.val, Intensity: intensity,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"agg": agg, "from": from, "to": to, "frames": frames,
	})
	return nil
}

// ---------- GET /api/patterns/{id} ----------

var dayNames = []string{"Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"}

func (a *app) patterns(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	id := r.PathValue("id")

	metric, err := parseMetric(r.URL.Query().Get("type"))
	if err != nil {
		return err
	}
	from, to, err := a.timeRange(r, 90*24*time.Hour)
	if err != nil {
		return err
	}
	name, err := a.sensorName(ctx, id)
	if err != nil {
		return err
	}

	rows, err := a.db.Query(ctx, `
		SELECT extract(isodow FROM time AT TIME ZONE 'Europe/Berlin')::int - 1 AS wd,
		       extract(hour   FROM time AT TIME ZONE 'Europe/Berlin')::int     AS hr,
		       avg(value), count(value)
		FROM readings
		WHERE sensor_id = $1 AND type = $2::text::metric_type
		  AND time >= $3 AND time < $4
		  AND quality_flag = 0 AND value IS NOT NULL
		GROUP BY 1, 2`, id, metric, from, to)
	if err != nil {
		return err
	}
	defer rows.Close()

	matrix := make([][]*float64, 7)
	counts := make([][]int, 7)
	for d := range matrix {
		matrix[d] = make([]*float64, 24)
		counts[d] = make([]int, 24)
	}
	for rows.Next() {
		var wd, hr int
		var avg *float64
		var n int64
		if err := rows.Scan(&wd, &hr, &avg, &n); err != nil {
			return err
		}
		if wd < 0 || wd > 6 || hr < 0 || hr > 23 {
			continue
		}
		matrix[wd][hr] = avg
		counts[wd][hr] = int(n)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"sensor_id": id, "name": name, "type": metric,
		"timezone": tzName, "from": from, "to": to,
		"days":   dayNames, // rows of the matrix
		"hours":  24,       // columns of the matrix (0..23, local time)
		"matrix": matrix,   // average value, null = no data
		"counts": counts,
	})
	return nil
}
