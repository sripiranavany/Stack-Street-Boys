package api

// Store manager dashboard (/api/v1/stores/overview and /api/v1/stores/{id}/dashboard).
//
// It combines the two kinds of sensor data the project has:
//   - in-store: the door counter (store_entries, entries per hour), turned into people inside with the store's
//     average visit length (domain.StoreConfig.Occupancy);
//   - outside: the street density sensors (readings), the one assigned to the store and the others nearby.
//
// Past hours use counted entries and measured density; future hours use the newest forecast run (store_forecasts,
// forecasts) and fall back to the capture-rate formula / typical profile, exactly like the store plan endpoint.

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"sort"
	"time"

	"ssb-backend/internal/db"
	"ssb-backend/internal/domain"
)

type dashHour struct {
	Hour        int      `json:"hour"`
	Open        bool     `json:"open"`
	Entries     float64  `json:"entries"`   // door entries in the hour (counted or forecast)
	Occupancy   float64  `json:"occupancy"` // average people inside during the hour
	OccLow      *float64 `json:"occupancy_low,omitempty"`
	OccHigh     *float64 `json:"occupancy_high,omitempty"`
	Source      string   `json:"source"` // counted | model | capture-rate ("" when closed)
	Level       string   `json:"level"`
	AreaDensity float64  `json:"area_density"`            // street sensor next to the store: people within ~10 m
	AreaSource  string   `json:"area_source"`             // actual | forecast | typical
	PassersBy   float64  `json:"passers_by"`              // people walking past per hour
	Staff       int      `json:"staff"`                   // suggested cashiers
	Why         *hourWhy `json:"why,omitempty"`           // weather, holiday and event context (dashcontext.go)
	Closed      string   `json:"closed_reason,omitempty"` // e.g. the public holiday's name
}

type nowBlock struct {
	Open         bool       `json:"open"`
	Occupancy    float64    `json:"occupancy"`
	Pct          float64    `json:"pct"`
	Level        string     `json:"level"`
	Source       string     `json:"source"`
	BasisFrom    *time.Time `json:"basis_from,omitempty"` // the counted hour the number comes from
	BasisTo      *time.Time `json:"basis_to,omitempty"`
	Entries      *float64   `json:"entries_last_hour,omitempty"`
	NextHour     *dashHour  `json:"next_hour,omitempty"`
	LiveAt       *time.Time `json:"live_at,omitempty"`           // newest live door-counter minute (source "live")
	EntriesHour  *float64   `json:"entries_this_hour,omitempty"` // live: entries since the start of this hour
	CountedToday float64    `json:"entries_counted_today"`
}

type areaBlock struct {
	SensorID     string     `json:"sensor_id"`
	Label        string     `json:"label"`
	Density      *float64   `json:"density"` // mean over the last hour, nil when the sensor is silent
	Typical      float64    `json:"typical"` // usual level for this weekday and hour
	VsNormal     *float64   `json:"vs_normal"`
	PassersBy    *float64   `json:"passers_by"`
	CaptureToday *float64   `json:"capture_today"` // counted entries / passers-by over today's counted hours
	Health       *db.Health `json:"health"`
}

type peakBlock struct {
	Hour      int      `json:"hour"`
	Occupancy float64  `json:"occupancy"`
	Pct       float64  `json:"pct"`
	Level     string   `json:"level"`
	Source    string   `json:"source"`
	Low       *float64 `json:"occupancy_low,omitempty"`
	High      *float64 `json:"occupancy_high,omitempty"`
}

type nearbyArea struct {
	SensorID  string   `json:"sensor_id"`
	Label     string   `json:"label"`
	DistanceM float64  `json:"distance_m"`
	Density   *float64 `json:"density"`
	Typical   float64  `json:"typical"`
	VsNormal  *float64 `json:"vs_normal"`
	Online    bool     `json:"online"`
	IsStore   bool     `json:"is_store_sensor"`
}

type dashAlert struct {
	Severity string `json:"severity"` // warn | info | ok
	Title    string `json:"title"`
	Detail   string `json:"detail"`
}

func passersBy(c domain.StoreConfig, density float64) float64 {
	if c.DwellSeconds <= 0 {
		return 0
	}
	return density * 3600 / c.DwellSeconds
}

func ptr(v float64) *float64 { return &v }

// storeBands: p10/p90 of the store forecast for one day, as occupancy per hour.
func (s *Server) storeBands(ctx context.Context, st store, day, now time.Time) (low, high map[int]float64) {
	low, high = map[int]float64{}, map[int]float64{}
	runID, _, _, err := s.latestRun(ctx, now)
	if err != nil {
		return
	}
	rows, err := s.P.Query(ctx, `SELECT ts, p10, p90 FROM store_forecasts WHERE run_id=$1 AND store_id=$2 AND ts >= $3 AND ts < $4`,
		runID, st.ID, day, day.AddDate(0, 0, 1))
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var ts time.Time
		var l, h *float64
		if rows.Scan(&ts, &l, &h) == nil && l != nil && h != nil {
			hr := ts.In(db.Berlin).Hour()
			low[hr], high[hr] = st.Config.Occupancy(*l), st.Config.Occupancy(*h)
		}
	}
	return
}

// storeHours: the 24 hours of one day for the dashboard chart, plus that day's holiday / event / weather context.
// The store is closed all day on public holidays (as in the forecaster), whatever its opening hours say.
func (s *Server) storeHours(ctx context.Context, st store, day, now time.Time) ([]dashHour, dayCtx) {
	dc := s.dayContext(ctx, st.SensorID, day, now)
	dens, dsrc := s.dayDensity(ctx, st.SensorID, day, now)
	vis, vsrc := s.dayVisitors(ctx, st, day, now, dens)
	low, high := s.storeBands(ctx, st, day, now)
	capacity := st.Config.CapacityOrDefault()
	wd := db.Weekday(day)
	if typ := s.typicalProfile(ctx)[st.SensorID]; typ != nil {
		for h := 0; h < 24; h++ {
			if t := typ[wd][h]; dsrc[h] == "actual" && t > 0 {
				dc.why(h).MeasuredVsUsual = ptr(round(dens[h]/t-1, 3))
			}
		}
	}
	out := make([]dashHour, 24)
	for h := range out {
		open := st.Config.IsOpen(wd, h) && dc.PublicHoliday == ""
		x := dashHour{Hour: h, Open: open, AreaDensity: round(dens[h], 2), AreaSource: dsrc[h],
			PassersBy: round(passersBy(st.Config, dens[h]), 0), Why: dc.Why[h]}
		if dc.PublicHoliday != "" && st.Config.IsOpen(wd, h) {
			x.Closed = dc.PublicHoliday
		}
		if open {
			x.Entries = round(vis[h], 0)
			x.Occupancy = round(st.Config.Occupancy(vis[h]), 0)
			x.Source = vsrc[h]
			x.Staff = st.Config.Cashiers(vis[h])
			if vsrc[h] == "model" {
				if l, ok := low[h]; ok {
					x.OccLow, x.OccHigh = ptr(round(l, 0)), ptr(round(high[h], 0))
				}
			}
		}
		x.Level = domain.Level(x.Occupancy, capacity, open)
		out[h] = x
	}
	return out, dc
}

func peakOf(hours []dashHour, capacity int) *peakBlock {
	var p *peakBlock
	for _, h := range hours {
		if h.Open && (p == nil || h.Occupancy > p.Occupancy) {
			p = &peakBlock{Hour: h.Hour, Occupancy: h.Occupancy, Pct: round(h.Occupancy/float64(capacity), 3),
				Level: h.Level, Source: h.Source, Low: h.OccLow, High: h.OccHigh}
		}
	}
	return p
}

// nowStatus: people inside now. Prefers the live door-counter headcount, then the newest fully counted hour
// (up to 2 h old), then the estimate for the current hour.
func (s *Server) nowStatus(ctx context.Context, st store, today []dashHour, now time.Time) nowBlock {
	capacity := st.Config.CapacityOrDefault()
	h := now.In(db.Berlin).Hour()
	nb := nowBlock{Open: today[h].Open}
	for _, x := range today {
		if x.Source == "counted" {
			nb.CountedToday += x.Entries
		}
	}
	if h+1 < 24 && today[h+1].Open {
		nh := today[h+1]
		nb.NextHour = &nh
	}
	if !nb.Open {
		nb.Level = domain.LevelClosed
		return nb
	}
	// Live headcount from the door counters (Kafka): entries minus exits since midnight, if the counter reported
	// within the last 10 minutes.
	var lastTS *time.Time
	var inside, inHour *float64
	if err := s.P.QueryRow(ctx, `SELECT max(ts), sum(entries - exits)::float8, (sum(entries) FILTER (WHERE ts >= $3))::float8
		FROM store_door_counts WHERE store_id=$1 AND ts >= $2 AND ts <= $4`,
		st.ID, midnight(now), now.Truncate(time.Hour), now).Scan(&lastTS, &inside, &inHour); err == nil &&
		lastTS != nil && inside != nil && now.Sub(*lastTS) <= 10*time.Minute {
		at := lastTS.In(db.Berlin).Add(time.Minute)
		nb.Occupancy, nb.Source, nb.LiveAt, nb.EntriesHour = math.Max(*inside, 0), "live", &at, inHour
		nb.Pct = round(nb.Occupancy/float64(capacity), 3)
		nb.Level = domain.Level(nb.Occupancy, capacity, true)
		return nb
	}
	var ts time.Time
	var entries float64
	err := s.P.QueryRow(ctx, `SELECT ts, entries::float8 FROM store_entries
		WHERE store_id=$1 AND is_open AND ts + interval '1 hour' <= $2 AND ts > $2 - interval '2 hours' ORDER BY ts DESC LIMIT 1`,
		st.ID, now).Scan(&ts, &entries)
	if err == nil {
		from, to := ts.In(db.Berlin), ts.In(db.Berlin).Add(time.Hour)
		nb.Occupancy, nb.Source, nb.BasisFrom, nb.BasisTo, nb.Entries = round(st.Config.Occupancy(entries), 0), "counted", &from, &to, ptr(entries)
	} else {
		nb.Occupancy, nb.Source = today[h].Occupancy, today[h].Source
	}
	nb.Pct = round(nb.Occupancy/float64(capacity), 3)
	nb.Level = domain.Level(nb.Occupancy, capacity, true)
	return nb
}

// lastHourDensity: mean street density per active sensor over the hour before now.
func (s *Server) lastHourDensity(ctx context.Context, now time.Time) (map[string]float64, error) {
	rows, err := s.P.Query(ctx, `SELECT sensor_id, avg(density) FROM readings
		WHERE ts <= $1 AND ts > $1 - interval '1 hour' AND density IS NOT NULL GROUP BY 1`, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]float64{}
	for rows.Next() {
		var id string
		var v float64
		if rows.Scan(&id, &v) == nil {
			out[id] = v
		}
	}
	return out, rows.Err()
}

func vsNormal(d *float64, typical float64) *float64 {
	if d == nil || typical <= 0 {
		return nil
	}
	return ptr(round(*d/typical-1, 3))
}

func (s *Server) areaStatus(ctx context.Context, st store, today []dashHour, dens map[string]float64, health map[string]*db.Health, now time.Time) areaBlock {
	a := areaBlock{SensorID: st.SensorID, Label: st.Label, Health: health[st.SensorID]}
	if p := s.typicalProfile(ctx)[st.SensorID]; p != nil {
		a.Typical = round(p[db.Weekday(now)][now.In(db.Berlin).Hour()], 2)
	}
	if v, ok := dens[st.SensorID]; ok {
		a.Density = ptr(round(v, 2))
		a.PassersBy = ptr(round(passersBy(st.Config, v), 0))
	}
	a.VsNormal = vsNormal(a.Density, a.Typical)
	var in, past float64
	for _, h := range today {
		if h.Source == "counted" && h.AreaSource == "actual" {
			in += h.Entries
			past += h.PassersBy
		}
	}
	if past > 0 {
		a.CaptureToday = ptr(round(in/past, 4))
	}
	return a
}

func distanceM(lat1, lon1, lat2, lon2 float64) float64 {
	const r = 6371000.0
	rad := math.Pi / 180
	dLat, dLon := (lat2-lat1)*rad, (lon2-lon1)*rad
	x := math.Sin(dLat/2)*math.Sin(dLat/2) + math.Cos(lat1*rad)*math.Cos(lat2*rad)*math.Sin(dLon/2)*math.Sin(dLon/2)
	return 2 * r * math.Asin(math.Sqrt(x))
}

// nearby: the active street sensors around the store, closest first.
func (s *Server) nearby(ctx context.Context, st store, dens map[string]float64, health map[string]*db.Health, now time.Time, limit int) []nearbyArea {
	out := []nearbyArea{}
	rows, err := s.P.Query(ctx, `SELECT id, label, lat, lon FROM sensors WHERE active`)
	if err != nil {
		return out
	}
	defer rows.Close()
	typ := s.typicalProfile(ctx)
	wd, h := db.Weekday(now), now.In(db.Berlin).Hour()
	for rows.Next() {
		var a nearbyArea
		var lat, lon float64
		if rows.Scan(&a.SensorID, &a.Label, &lat, &lon) != nil {
			continue
		}
		a.DistanceM = math.Round(distanceM(st.Lat, st.Lon, lat, lon))
		a.IsStore = a.SensorID == st.SensorID
		if p := typ[a.SensorID]; p != nil {
			a.Typical = round(p[wd][h], 2)
		}
		if v, ok := dens[a.SensorID]; ok {
			a.Density = ptr(round(v, 2))
		}
		a.VsNormal = vsNormal(a.Density, a.Typical)
		if hc := health[a.SensorID]; hc != nil {
			a.Online = hc.Online
		} else {
			a.Online = a.Density != nil
		}
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].DistanceM < out[j].DistanceM })
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// week: usual people inside per weekday x open hour (door-counter median, capture-rate formula as fallback).
func (s *Server) week(ctx context.Context, st store) map[string]any {
	counted, ok := s.typicalEntries(ctx, st.ID)
	prof := s.typicalProfile(ctx)[st.SensorID]
	hours := []int{}
	for h := st.Config.OpenFrom; h < st.Config.OpenTo; h++ {
		hours = append(hours, h)
	}
	grid := make([][]*float64, 7)
	for d := 0; d < 7; d++ {
		grid[d] = make([]*float64, len(hours))
		for i, h := range hours {
			if !st.Config.IsOpen(d, h) {
				continue
			}
			var e float64
			switch {
			case ok:
				e = counted[d][h]
			case prof != nil:
				e = st.Config.Visitors(prof[d][h])
			default:
				continue
			}
			grid[d][i] = ptr(round(st.Config.Occupancy(e), 0))
		}
	}
	src := "door-counter"
	if !ok {
		src = "capture-rate"
	}
	return map[string]any{"days": []string{"Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"}, "hours": hours, "occupancy": grid, "source": src}
}

func hh(h int) string { return fmt.Sprintf("%02d:00", h) }

// storeAlerts: plain-language heads-up for the manager.
func (s *Server) storeAlerts(ctx context.Context, st store, hours []dashHour, dc dayCtx, fromHour int, area areaBlock, now time.Time, tomorrowHoliday string) []dashAlert {
	capacity := st.Config.CapacityOrDefault()
	out := []dashAlert{}
	var near, busy *dashHour
	for i := fromHour; i < 24 && i < len(hours); i++ {
		h := hours[i]
		if h.Level == domain.LevelNearCapacity && near == nil {
			near = &hours[i]
		}
		if h.Level == domain.LevelBusy && busy == nil {
			busy = &hours[i]
		}
	}
	if near != nil {
		out = append(out, dashAlert{"warn", "Near capacity at " + hh(near.Hour),
			fmt.Sprintf("About %.0f people expected (%.0f%% of %d). Plan entry control and open extra tills from %s.",
				near.Occupancy, near.Occupancy/float64(capacity)*100, capacity, hh(max(near.Hour-1, 0)))})
	} else if busy != nil {
		out = append(out, dashAlert{"info", "Busy from " + hh(busy.Hour),
			fmt.Sprintf("About %.0f people expected; %d cashiers suggested.", busy.Occupancy, busy.Staff)})
	}
	if area.VsNormal != nil {
		pct := *area.VsNormal * 100
		switch {
		case pct >= 20:
			out = append(out, dashAlert{"warn", fmt.Sprintf("Foot traffic at %s is %.0f%% above normal", area.Label, pct),
				"More people outside than usual for this hour — expect more shoppers soon."})
		case pct <= -20:
			out = append(out, dashAlert{"info", fmt.Sprintf("Foot traffic at %s is %.0f%% below normal", area.Label, -pct),
				"Quieter outside than usual — staff could be freed up."})
		}
	}
	out = append(out, causeAlerts(dc, hours, fromHour, tomorrowHoliday)...)
	if area.Health != nil {
		switch area.Health.Risk {
		case "offline":
			out = append(out, dashAlert{"warn", "Street sensor " + area.Label + " is offline",
				fmt.Sprintf("No reading for %.0f minutes. Area numbers use the forecast and typical levels.", area.Health.MinutesSilent)})
		case "critical":
			out = append(out, dashAlert{"warn", "Street sensor " + area.Label + " battery low",
				fmt.Sprintf("Battery at %.0f%%.", area.Health.Battery)})
		default:
			out = append(out, dashAlert{"ok", "Street sensor " + area.Label + " online", "Area readings are live."})
		}
	}
	rows, err := s.P.Query(ctx, `SELECT kind, title, coalesce(detail, '') FROM alerts
		WHERE sensor_id=$1 AND status='open' AND ts <= $2 AND ts > $2 - interval '1 day' ORDER BY ts DESC LIMIT 3`, st.SensorID, now)
	if err == nil {
		for rows.Next() {
			var kind, title, detail string
			// only anomalies: offline/battery are covered above, prezero (bin collection) is for waste planners
			if rows.Scan(&kind, &title, &detail) == nil && kind == "anomaly" {
				out = append(out, dashAlert{"info", title, detail})
			}
		}
		rows.Close()
	}
	return out
}

func healthMap(ctx context.Context, s *Server, now time.Time) map[string]*db.Health {
	m := map[string]*db.Health{}
	hs, _ := db.SensorHealth(ctx, s.P, now)
	for i := range hs {
		m[hs[i].SensorID] = &hs[i]
	}
	return m
}

func dashDay(v string, now time.Time) (time.Time, error) {
	today := midnight(now)
	switch v {
	case "", "today":
		return today, nil
	case "tomorrow":
		return today.AddDate(0, 0, 1), nil
	}
	return time.ParseInLocation("2006-01-02", v, db.Berlin)
}

// getStoreDashboard: everything the single-store dashboard shows, for ?day=today|tomorrow|YYYY-MM-DD.
func (s *Server) getStoreDashboard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	state, err := s.now(ctx)
	if err != nil {
		fail(w, 500, err)
		return
	}
	now := state.SimNow.In(db.Berlin)
	st, err := s.loadStore(ctx, r.PathValue("id"))
	if err != nil {
		fail(w, 404, err)
		return
	}
	day, err := dashDay(r.URL.Query().Get("day"), now)
	if err != nil {
		fail(w, 400, err)
		return
	}
	todayStart := midnight(now)
	today, todayCtx := s.storeHours(ctx, st, todayStart, now)
	hours, dc := today, todayCtx
	if !day.Equal(todayStart) {
		hours, dc = s.storeHours(ctx, st, day, now)
	}
	isToday := day.Equal(todayStart)
	capacity := st.Config.CapacityOrDefault()
	dens, _ := s.lastHourDensity(ctx, now)
	health := healthMap(ctx, s, now)
	area := s.areaStatus(ctx, st, today, dens, health, now)

	fromHour, tomorrowHoliday := 0, ""
	if isToday {
		fromHour = now.Hour()
		tomorrowHoliday = s.closedTomorrowFor(ctx, st, todayStart)
	}
	next := []dashHour{}
	if isToday {
		for h := now.Hour() + 1; h < 24 && len(next) < 4; h++ {
			if today[h].Open {
				next = append(next, today[h])
			}
		}
	}
	res := map[string]any{
		"store": st, "capacity": capacity, "visit_minutes": st.Config.VisitMinutesOrDefault(),
		"sim_now": now, "mode": state.Mode, "day": day.Format("2006-01-02"), "weekday": day.Weekday().String(), "is_today": isToday,
		"now": s.nowStatus(ctx, st, today, now), "area": area, "hours": hours, "peak": peakOf(hours, capacity), "next_hours": next,
		"nearby": s.nearby(ctx, st, dens, health, now, 5), "week": s.week(ctx, st),
		"alerts": s.storeAlerts(ctx, st, hours, dc, fromHour, area, now, tomorrowHoliday), "context": dc,
		"upcoming_events": s.upcomingEvents(ctx, st.SensorID, now),
		"learned_events":  s.learnedEvents(ctx, &st),
	}
	if id, asOf, ver, err := s.latestRun(ctx, now); err == nil {
		res["forecast_run"] = map[string]any{"id": id, "as_of": asOf, "model_version": ver}
	}
	writeJSON(w, res)
}

// getStoresOverview: one summary row per store for the regional overview.
func (s *Server) getStoresOverview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	state, err := s.now(ctx)
	if err != nil {
		fail(w, 500, err)
		return
	}
	now := state.SimNow.In(db.Berlin)
	rows, err := s.P.Query(ctx, `SELECT id FROM stores ORDER BY name`)
	if err != nil {
		fail(w, 500, err)
		return
	}
	var ids []string
	for rows.Next() {
		var id string
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	dens, _ := s.lastHourDensity(ctx, now)
	health := healthMap(ctx, s, now)
	type spark struct {
		Hour      int     `json:"hour"`
		Occupancy float64 `json:"occupancy"`
		Source    string  `json:"source"`
	}
	type summary struct {
		Store    store       `json:"store"`
		Capacity int         `json:"capacity"`
		Now      nowBlock    `json:"now"`
		Area     areaBlock   `json:"area"`
		Peak     *peakBlock  `json:"peak"`
		Hours    []spark     `json:"hours"`
		Alerts   []dashAlert `json:"alerts"`
		Context  dayCtx      `json:"context"`
	}
	out := []summary{}
	day := midnight(now)
	for _, id := range ids {
		st, err := s.loadStore(ctx, id)
		if err != nil {
			continue
		}
		hours, dc := s.storeHours(ctx, st, day, now)
		capacity := st.Config.CapacityOrDefault()
		area := s.areaStatus(ctx, st, hours, dens, health, now)
		sp := []spark{}
		for _, h := range hours {
			if h.Open {
				sp = append(sp, spark{h.Hour, h.Occupancy, h.Source})
			}
		}
		out = append(out, summary{Store: st, Capacity: capacity, Now: s.nowStatus(ctx, st, hours, now), Area: area,
			Peak: peakOf(hours, capacity), Hours: sp, Alerts: s.storeAlerts(ctx, st, hours, dc, now.Hour(), area, now, s.closedTomorrowFor(ctx, st, day)),
			Context: dc})
	}
	writeJSON(w, map[string]any{"sim_now": now, "mode": state.Mode, "stores": out})
}
