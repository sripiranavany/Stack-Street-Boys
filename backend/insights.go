package main

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

// ---------- context data (weather, holidays, events) ----------
// These tables are filled by the Python ETL job. See db/schema_extra.sql.

type weatherRow struct {
	Time         time.Time `json:"time"`
	TemperatureC *float64  `json:"temperature_c"`
	PrecipitMM   *float64  `json:"precipitation_mm"`
	WindSpeedMS  *float64  `json:"wind_speed_ms"`
}

type holidayRow struct {
	Date   string  `json:"date"`
	Name   string  `json:"name"`
	Region *string `json:"region"`
}

type eventRow struct {
	Name      string     `json:"name"`
	StartsAt  time.Time  `json:"starts_at"`
	EndsAt    *time.Time `json:"ends_at"`
	Venue     *string    `json:"venue"`
	Latitude  *float64   `json:"latitude"`
	Longitude *float64   `json:"longitude"`
}

type contextData struct {
	Weather  []weatherRow `json:"weather"`
	Holidays []holidayRow `json:"holidays"`
	Events   []eventRow   `json:"events"`
	// Missing lists context tables that do not exist yet.
	Missing []string `json:"missing_tables"`
}

// loadContext reads weather, holidays and events. A table that does not exist yet
// is not an error: it is listed in Missing and its list stays empty.
func (a *app) loadContext(ctx context.Context, from, to time.Time) (*contextData, error) {
	out := &contextData{Weather: []weatherRow{}, Holidays: []holidayRow{}, Events: []eventRow{}, Missing: []string{}}

	// weather
	rows, err := a.db.Query(ctx, `
		SELECT time, temperature_c, precipitation_mm, wind_speed_ms
		FROM weather_hourly WHERE time >= $1 AND time < $2 ORDER BY time`, from, to)
	switch {
	case isUndefinedTable(err):
		out.Missing = append(out.Missing, "weather_hourly")
	case err != nil:
		return nil, err
	default:
		for rows.Next() {
			var w weatherRow
			if err := rows.Scan(&w.Time, &w.TemperatureC, &w.PrecipitMM, &w.WindSpeedMS); err != nil {
				rows.Close()
				return nil, err
			}
			out.Weather = append(out.Weather, w)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}

	// holidays (a holiday is a calendar date, so compare with local dates)
	fromDay := from.In(berlin).Format("2006-01-02")
	toDay := to.In(berlin).Format("2006-01-02")
	rows, err = a.db.Query(ctx, `
		SELECT date, name, region FROM holidays
		WHERE date >= $1::text::date AND date <= $2::text::date ORDER BY date`, fromDay, toDay)
	switch {
	case isUndefinedTable(err):
		out.Missing = append(out.Missing, "holidays")
	case err != nil:
		return nil, err
	default:
		for rows.Next() {
			var d time.Time
			var h holidayRow
			if err := rows.Scan(&d, &h.Name, &h.Region); err != nil {
				rows.Close()
				return nil, err
			}
			h.Date = d.Format("2006-01-02")
			out.Holidays = append(out.Holidays, h)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}

	// events that overlap the range
	rows, err = a.db.Query(ctx, `
		SELECT name, starts_at, ends_at, venue, latitude, longitude FROM events
		WHERE starts_at < $2 AND coalesce(ends_at, starts_at) >= $1 ORDER BY starts_at`, from, to)
	switch {
	case isUndefinedTable(err):
		out.Missing = append(out.Missing, "events")
	case err != nil:
		return nil, err
	default:
		for rows.Next() {
			var e eventRow
			if err := rows.Scan(&e.Name, &e.StartsAt, &e.EndsAt, &e.Venue, &e.Latitude, &e.Longitude); err != nil {
				rows.Close()
				return nil, err
			}
			out.Events = append(out.Events, e)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// GET /api/context?from&to
func (a *app) context(w http.ResponseWriter, r *http.Request) error {
	from, to, err := a.timeRange(r, week)
	if err != nil {
		return err
	}
	data, err := a.loadContext(r.Context(), from, to)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"from": from, "to": to, "timezone": tzName,
		"weather": data.Weather, "holidays": data.Holidays, "events": data.Events,
		"missing_tables": data.Missing,
	})
	return nil
}

// ---------- GET /api/events/spikes ----------

type spike struct {
	SensorID  string    `json:"sensor_id"`
	Name      string    `json:"name"`
	Latitude  float64   `json:"latitude"`
	Longitude float64   `json:"longitude"`
	Time      time.Time `json:"time"`
	Value     float64   `json:"value"`
	Expected  float64   `json:"expected"`
	ZScore    float64   `json:"z_score"`
	Reasons   []string  `json:"likely_reasons"`
}

// A spike is an hour whose average is far above what the same sensor usually shows
// in the same weekday and hour (z-score), judged against the 8 weeks before the end of the range.
func (a *app) spikes(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	q := r.URL.Query()
	sensor := q.Get("sensor_id")

	from, to, err := a.timeRange(r, 30*24*time.Hour)
	if err != nil {
		return err
	}
	z := 3.0
	if v := q.Get("z"); v != "" {
		z, err = strconv.ParseFloat(v, 64)
		if err != nil || z < 1 || z > 20 {
			return badRequest("z must be a number from 1 to 20")
		}
	}
	limit, err := parseIntParam(q.Get("limit"), 100, 1, 1000)
	if err != nil {
		return badRequest("limit: %v", err)
	}

	baseStart := from
	if b := to.Add(-56 * 24 * time.Hour); b.Before(baseStart) {
		baseStart = b
	}

	sq := &qb{}
	sq.add(`WITH hourly AS (
		SELECT sensor_id, time_bucket('1 hour', time) AS hr, avg(value) AS v
		FROM readings
		WHERE type = 'densityData' AND quality_flag = 0 AND value IS NOT NULL
		  AND time >= ` + sq.p(baseStart) + ` AND time < ` + sq.p(to))
	if sensor != "" {
		sq.add(` AND sensor_id = ` + sq.p(sensor))
	}
	sq.add(`GROUP BY 1, 2
	), keyed AS (
		SELECT sensor_id, hr, v,
		       extract(isodow FROM hr AT TIME ZONE 'Europe/Berlin')::int AS wd,
		       extract(hour   FROM hr AT TIME ZONE 'Europe/Berlin')::int AS h
		FROM hourly
	), base AS (
		SELECT sensor_id, wd, h, avg(v) AS mu, stddev_samp(v) AS sd, count(*) AS n
		FROM keyed GROUP BY 1, 2, 3
	)
	SELECT k.sensor_id, s.name, s.latitude, s.longitude, k.hr, k.v, b.mu,
	       (k.v - b.mu) / b.sd AS z
	FROM keyed k
	JOIN base b USING (sensor_id, wd, h)
	JOIN sensors s USING (sensor_id)
	WHERE k.hr >= ` + sq.p(from) + ` AND k.hr < ` + sq.p(to) + `
	  AND b.n >= 4 AND b.sd > 0 AND NOT s.is_test
	  AND (k.v - b.mu) / b.sd >= ` + sq.p(z) + `
	ORDER BY z DESC
	LIMIT ` + sq.p(limit))

	rows, err := a.db.Query(ctx, sq.String(), sq.args...)
	if err != nil {
		return err
	}
	defer rows.Close()

	spikes := []spike{}
	for rows.Next() {
		var s spike
		if err := rows.Scan(&s.SensorID, &s.Name, &s.Latitude, &s.Longitude, &s.Time, &s.Value, &s.Expected, &s.ZScore); err != nil {
			return err
		}
		s.Reasons = []string{}
		spikes = append(spikes, s)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	// Attach likely reasons from holidays, events and weather (if those tables exist).
	data, err := a.loadContext(ctx, from, to)
	if err != nil {
		return err
	}
	holidayByDate := map[string][]holidayRow{}
	for _, h := range data.Holidays {
		holidayByDate[h.Date] = append(holidayByDate[h.Date], h)
	}
	weatherByHour := map[int64]weatherRow{}
	for _, wr := range data.Weather {
		weatherByHour[wr.Time.Truncate(time.Hour).Unix()] = wr
	}
	for i := range spikes {
		s := &spikes[i]
		for _, h := range holidayByDate[s.Time.In(berlin).Format("2006-01-02")] {
			s.Reasons = append(s.Reasons, "public holiday: "+h.Name)
		}
		for _, e := range data.Events {
			end := e.StartsAt
			if e.EndsAt != nil {
				end = *e.EndsAt
			}
			if s.Time.Before(end.Add(time.Hour)) && s.Time.Add(time.Hour).After(e.StartsAt) {
				s.Reasons = append(s.Reasons, "event: "+e.Name)
			}
		}
		if wr, ok := weatherByHour[s.Time.Truncate(time.Hour).Unix()]; ok {
			if wr.PrecipitMM != nil && *wr.PrecipitMM >= 1 {
				s.Reasons = append(s.Reasons, fmt.Sprintf("rain: %.1f mm", *wr.PrecipitMM))
			}
			if wr.TemperatureC != nil && *wr.TemperatureC >= 25 {
				s.Reasons = append(s.Reasons, fmt.Sprintf("hot weather: %.0f °C", *wr.TemperatureC))
			}
			if wr.TemperatureC != nil && *wr.TemperatureC <= 0 {
				s.Reasons = append(s.Reasons, fmt.Sprintf("freezing: %.0f °C", *wr.TemperatureC))
			}
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"from": from, "to": to, "z_threshold": z,
		"spikes":         spikes,
		"missing_tables": data.Missing,
	})
	return nil
}

// ---------- GET /api/forecast/{id}?horizon=72h ----------

type forecastPoint struct {
	Time  time.Time `json:"time"`
	Value float64   `json:"value"`
	Lower *float64  `json:"lower"`
	Upper *float64  `json:"upper"`
}

// The forecast worker (Python) writes rows into the table "forecasts".
// This endpoint returns the part that lies after the newest real reading.
func (a *app) forecast(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	id := r.PathValue("id")

	horizon := 72 * time.Hour
	if v := r.URL.Query().Get("horizon"); v != "" {
		var err error
		if horizon, err = parseHorizon(v); err != nil {
			return badRequest("%v", err)
		}
	}
	if horizon > 14*24*time.Hour {
		return badRequest("horizon can be at most 14 days")
	}
	name, err := a.sensorName(ctx, id)
	if err != nil {
		return err
	}

	var anchor *time.Time
	if err := a.db.QueryRow(ctx, `
		SELECT max(time) FROM readings WHERE sensor_id = $1 AND type = 'densityData'`, id).Scan(&anchor); err != nil {
		return err
	}
	if anchor == nil {
		return notFound("sensor %q has no readings", id)
	}

	rows, err := a.db.Query(ctx, `
		SELECT time, yhat, yhat_lower, yhat_upper
		FROM forecasts
		WHERE sensor_id = $1 AND time > $2 AND time <= $3
		ORDER BY time`, id, *anchor, anchor.Add(horizon))
	if isUndefinedTable(err) {
		writeJSON(w, http.StatusOK, map[string]any{
			"sensor_id": id, "name": name, "based_on_data_until": *anchor,
			"horizon": horizon.String(), "points": []forecastPoint{},
			"note": "table \"forecasts\" does not exist yet (run db/schema_extra.sql)",
		})
		return nil
	}
	if err != nil {
		return err
	}
	defer rows.Close()

	points := []forecastPoint{}
	for rows.Next() {
		var p forecastPoint
		if err := rows.Scan(&p.Time, &p.Value, &p.Lower, &p.Upper); err != nil {
			return err
		}
		points = append(points, p)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	resp := map[string]any{
		"sensor_id": id, "name": name,
		"based_on_data_until": *anchor,
		"horizon":             horizon.String(),
		"points":              points,
	}
	if len(points) == 0 {
		resp["note"] = "no forecast stored for this period yet"
	}
	writeJSON(w, http.StatusOK, resp)
	return nil
}
