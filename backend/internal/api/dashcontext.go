package api

// Why a store hour looks the way it does: public / school holidays, events near the store and the weather, plus the
// forecaster's own estimate of how much each of them moves foot traffic (forecasts.reasons).

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	"ssb-backend/internal/db"
)

// hourWhy explains one hour. Effects are the forecaster's estimate of how much a cause changes street foot traffic
// (0.12 = +12 %): from the newest run for future hours, from the run made before the hour for past hours.
type hourWhy struct {
	Weather       string   `json:"weather,omitempty"`
	Temp          *float64 `json:"temp,omitempty"`
	Precip        *float64 `json:"precip,omitempty"`
	WeatherEffect *float64 `json:"weather_effect,omitempty"`
	HolidayEffect *float64 `json:"holiday_effect,omitempty"`
	SchoolEffect  *float64 `json:"school_effect,omitempty"`
	Event         string   `json:"event,omitempty"`
	EventMult     *float64 `json:"event_mult,omitempty"`
	EventBasis    string   `json:"event_basis,omitempty"`    // profile | learned | category | size (forecaster/events.py)
	EventBasedOn  string   `json:"event_based_on,omitempty"` // e.g. "Heilbronner Lichterfest 2026"
	// Past hours only: when the forecast that gave these effects was made, and how far measured foot traffic
	// actually was from the usual level for that weekday and hour (0.3 = 30 % busier).
	EffectsAsOf     *time.Time `json:"effects_as_of,omitempty"`
	MeasuredVsUsual *float64   `json:"measured_vs_usual,omitempty"`
}

type dayEvent struct {
	Name     string    `json:"name"`
	Category string    `json:"category"`
	Start    time.Time `json:"start"`
	End      time.Time `json:"end"`
}

// dayCtx is the calendar and weather context of one day at the store.
type dayCtx struct {
	PublicHoliday string       `json:"public_holiday,omitempty"`
	SchoolHoliday string       `json:"school_holiday,omitempty"`
	Events        []dayEvent   `json:"events"`
	Why           [24]*hourWhy `json:"-"`
}

func (d *dayCtx) why(h int) *hourWhy {
	if d.Why[h] == nil {
		d.Why[h] = &hourWhy{}
	}
	return d.Why[h]
}

func weatherDesc(temp, precip, cloud *float64) string {
	var parts []string
	if temp != nil {
		parts = append(parts, fmt.Sprintf("%.0f°C", *temp))
	}
	if precip != nil {
		if *precip >= 0.1 {
			parts = append(parts, fmt.Sprintf("%.1f mm rain", *precip))
		} else {
			parts = append(parts, "dry")
		}
	}
	if cloud != nil {
		parts = append(parts, fmt.Sprintf("%.0f%% cloud", *cloud))
	}
	return strings.Join(parts, ", ")
}

// holidays returns the public and school holiday names on day ("" when none).
func (s *Server) holidays(ctx context.Context, day time.Time) (public, school string) {
	rows, err := s.P.Query(ctx, `SELECT kind, name FROM holidays WHERE $1::date BETWEEN start_date AND end_date`, day.Format("2006-01-02"))
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var kind, name string
		if rows.Scan(&kind, &name) == nil {
			if kind == "public" {
				public = name
			} else if school == "" {
				school = name
			}
		}
	}
	return
}

// dayContext loads holidays, events at the store's sensor and hourly weather / forecast effects for one day.
func (s *Server) dayContext(ctx context.Context, sensor string, day, now time.Time) dayCtx {
	d := dayCtx{Events: []dayEvent{}}
	end := day.AddDate(0, 0, 1)
	d.PublicHoliday, d.SchoolHoliday = s.holidays(ctx, day)

	// events touching this sensor
	if rows, err := s.P.Query(ctx, `SELECT name, category, start_ts, end_ts FROM events
		WHERE $1 = ANY(sensor_ids) AND start_ts < $3 AND end_ts > $2 ORDER BY start_ts`, sensor, day, end); err == nil {
		for rows.Next() {
			var e dayEvent
			if rows.Scan(&e.Name, &e.Category, &e.Start, &e.End) == nil {
				e.Start, e.End = e.Start.In(db.Berlin), e.End.In(db.Berlin)
				d.Events = append(d.Events, e)
				for h := 0; h < 24; h++ {
					hs := day.Add(time.Duration(h) * time.Hour)
					if hs.Before(e.End) && hs.Add(time.Hour).After(e.Start) {
						d.why(h).Event = e.Name
					}
				}
			}
		}
		rows.Close()
	}

	// measured weather for past hours
	if rows, err := s.P.Query(ctx, `SELECT ts, temp, precip, cloud FROM weather
		WHERE kind='actual' AND ts >= $1 AND ts < $2 AND ts <= $3`, day, end, now); err == nil {
		for rows.Next() {
			var ts time.Time
			var t, p, c *float64
			if rows.Scan(&ts, &t, &p, &c) == nil {
				w := d.why(ts.In(db.Berlin).Hour())
				w.Weather, w.Temp, w.Precip = weatherDesc(t, p, c), t, p
			}
		}
		rows.Close()
	}

	// The forecaster's reasons. Future hours: the newest run. Past hours: the newest run issued before that hour, so a
	// manager can see afterwards what the model expected the weather / holiday / event to do. Measured weather stays.
	apply := func(ts time.Time, raw []byte, past bool, asOf time.Time) {
		var r struct {
			Temp    *float64 `json:"temp"`
			Precip  *float64 `json:"precip"`
			Cloud   *float64 `json:"cloud"`
			Weather *float64 `json:"weather"`
			Holiday *float64 `json:"holiday"`
			School  *float64 `json:"school"`
			Event   *struct {
				Name    string  `json:"name"`
				Mult    float64 `json:"mult"`
				Basis   string  `json:"basis"`
				BasedOn string  `json:"based_on"`
			} `json:"event"`
		}
		if json.Unmarshal(raw, &r) != nil {
			return
		}
		w := d.why(ts.In(db.Berlin).Hour())
		if !past || w.Weather == "" {
			w.Weather, w.Temp, w.Precip = weatherDesc(r.Temp, r.Precip, r.Cloud), r.Temp, r.Precip
		}
		w.WeatherEffect, w.HolidayEffect, w.SchoolEffect = effect(r.Weather), effect(r.Holiday), effect(r.School)
		if r.Event != nil {
			w.Event, w.EventMult = r.Event.Name, ptr(round(r.Event.Mult, 2))
			w.EventBasis, w.EventBasedOn = r.Event.Basis, r.Event.BasedOn
		}
		if past {
			a := asOf.In(db.Berlin)
			w.EffectsAsOf = &a
		}
	}
	if runID, _, _, err := s.latestRun(ctx, now); err == nil {
		if rows, err := s.P.Query(ctx, `SELECT ts, reasons FROM forecasts WHERE run_id=$1 AND sensor_id=$2 AND ts >= $3 AND ts < $4 AND ts > $5`,
			runID, sensor, day, end, now); err == nil {
			for rows.Next() {
				var ts time.Time
				var raw []byte
				if rows.Scan(&ts, &raw) == nil {
					apply(ts, raw, false, time.Time{})
				}
			}
			rows.Close()
		}
	}
	if rows, err := s.P.Query(ctx, `SELECT DISTINCT ON (f.ts) f.ts, f.reasons, r.as_of FROM forecasts f JOIN forecast_runs r ON r.id = f.run_id
		WHERE f.sensor_id=$1 AND f.ts >= $2 AND f.ts < $3 AND f.ts <= $4 AND r.as_of <= f.ts
		ORDER BY f.ts, r.as_of DESC, r.id DESC`, sensor, day, end, now); err == nil {
		for rows.Next() {
			var ts, asOf time.Time
			var raw []byte
			if rows.Scan(&ts, &raw, &asOf) == nil {
				apply(ts, raw, true, asOf)
			}
		}
		rows.Close()
	}
	return d
}

// effect keeps effects of at least 1 %; smaller ones are noise for a manager.
func effect(v *float64) *float64 {
	if v == nil || math.Abs(*v) < 0.01 {
		return nil
	}
	return ptr(round(*v, 3))
}

// causeAlerts: heads-up about holidays, events and weather for the hours from fromHour on.
func causeAlerts(dc dayCtx, hours []dashHour, fromHour int, tomorrowHoliday string) []dashAlert {
	out := []dashAlert{}
	// only when the holiday closes the store on a day it would normally open
	for _, h := range hours {
		if h.Closed != "" {
			out = append(out, dashAlert{"info", "Closed — " + dc.PublicHoliday, "Public holiday: the store stays closed, so there is no forecast for this day."})
			break
		}
	}
	// events: first upcoming hour of each
	seen := map[string]bool{}
	for h := fromHour; h < 24; h++ {
		w := dc.Why[h]
		if w == nil || w.Event == "" || seen[w.Event] {
			continue
		}
		seen[w.Event] = true
		detail := "Event near the store — expect more people outside."
		if w.EventMult != nil {
			detail = fmt.Sprintf("Foot traffic around the store expected about %+.0f%% during the event.", (*w.EventMult-1)*100)
		}
		detail += basisText(w)
		out = append(out, dashAlert{"warn", fmt.Sprintf("%s from %s", w.Event, hh(h)), detail})
	}
	// weather: average effect over the remaining open hours
	var sum float64
	var n int
	var wettest *hourWhy
	for h := fromHour; h < 24 && h < len(hours); h++ {
		w := dc.Why[h]
		if !hours[h].Open || w == nil || w.WeatherEffect == nil {
			continue
		}
		sum += *w.WeatherEffect
		n++
		if wettest == nil || (w.Precip != nil && (wettest.Precip == nil || *w.Precip > *wettest.Precip)) {
			wettest = w
		}
	}
	if n > 0 && wettest != nil {
		avg := sum / float64(n)
		switch {
		case avg <= -0.05:
			out = append(out, dashAlert{"info", fmt.Sprintf("Weather keeps about %.0f%% of people away", -avg*100),
				"Forecast: " + wettest.Weather + ". Fewer shoppers than usual for these hours."})
		case avg >= 0.05:
			out = append(out, dashAlert{"info", fmt.Sprintf("Good weather: about %.0f%% more people out", avg*100),
				"Forecast: " + wettest.Weather + ". Expect more passers-by than usual."})
		}
	}
	// looking back: how the hours so far compared with usual, and the likely causes the forecaster saw
	if back := lookBack(dc, hours, fromHour); back != nil {
		out = append(out, *back)
	}
	if dc.SchoolHoliday != "" && dc.PublicHoliday == "" {
		detail := "Daytime patterns can shift: more families, fewer school-run peaks."
		for h := fromHour; h < 24; h++ {
			if w := dc.Why[h]; w != nil && w.SchoolEffect != nil {
				detail = fmt.Sprintf("The forecast expects about %+.0f%% foot traffic because of it.", *w.SchoolEffect*100)
				break
			}
		}
		out = append(out, dashAlert{"info", "School holidays: " + dc.SchoolHoliday, detail})
	}
	if tomorrowHoliday != "" {
		out = append(out, dashAlert{"info", "Closed tomorrow — " + tomorrowHoliday, "Public holiday. Expect shoppers to stock up today."})
	}
	return out
}

// closedTomorrowFor: the public holiday that closes the store tomorrow although it would normally open ("" otherwise).
func (s *Server) closedTomorrowFor(ctx context.Context, st store, today time.Time) string {
	t := today.AddDate(0, 0, 1)
	if !st.Config.IsOpen(db.Weekday(t), st.Config.OpenFrom) {
		return ""
	}
	public, _ := s.holidays(ctx, t)
	return public
}

// lookBack summarises the measured foot traffic of today's open hours before fromHour against the usual level.
// Returns nil when nothing was unusual (within ±15 %) or there are no measured hours yet.
func lookBack(dc dayCtx, hours []dashHour, fromHour int) *dashAlert {
	var sum float64
	var n int
	causes := map[string]float64{}
	for h := 0; h < fromHour && h < len(hours); h++ {
		w := dc.Why[h]
		if !hours[h].Open || w == nil || w.MeasuredVsUsual == nil {
			continue
		}
		sum += *w.MeasuredVsUsual
		n++
		if w.Event != "" {
			m := 0.5
			if w.EventMult != nil {
				m = *w.EventMult - 1
			}
			causes[w.Event] += m
		}
		if w.WeatherEffect != nil {
			causes["weather"] += *w.WeatherEffect
		}
		if w.HolidayEffect != nil {
			causes["holiday"] += *w.HolidayEffect
		}
		if w.SchoolEffect != nil {
			causes["school holidays"] += *w.SchoolEffect
		}
	}
	if n == 0 {
		return nil
	}
	avg := sum / float64(n)
	if math.Abs(avg) < 0.15 {
		return nil
	}
	// the cause pushing most in the same direction as what was measured
	best, bestV := "", 0.0
	for c, v := range causes {
		if v*avg > bestV*avg && math.Abs(v/float64(n)) >= 0.02 {
			best, bestV = c, v
		}
	}
	dir, word := "busier", "above"
	if avg < 0 {
		dir, word = "quieter", "below"
	}
	detail := fmt.Sprintf("Foot traffic outside was %.0f%% %s the usual level over %d open hours.", math.Abs(avg)*100, word, n)
	if best != "" {
		detail += fmt.Sprintf(" Most likely cause: %s (forecast effect %+.0f%%).", best, bestV/float64(n)*100)
	} else {
		detail += " No holiday, event or weather effect explains it — worth noting what happened."
	}
	return &dashAlert{"info", "So far today: " + dir + " than usual", detail}
}

type upcomingEvent struct {
	ID       int       `json:"id"`
	Name     string    `json:"name"`
	Category string    `json:"category"`
	Start    time.Time `json:"start"`
	End      time.Time `json:"end"`
	Size     *string   `json:"size"`
	Source   string    `json:"source"`
}

// upcomingEvents: events at the store's sensor that are running now or start within the next 7 days.
func (s *Server) upcomingEvents(ctx context.Context, sensor string, now time.Time) []upcomingEvent {
	out := []upcomingEvent{}
	rows, err := s.P.Query(ctx, `SELECT id, name, category, start_ts, end_ts, size, source FROM events
		WHERE $1 = ANY(sensor_ids) AND end_ts > $2 AND start_ts < $2 + interval '7 days' ORDER BY start_ts`, sensor, now)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var e upcomingEvent
		if rows.Scan(&e.ID, &e.Name, &e.Category, &e.Start, &e.End, &e.Size, &e.Source) == nil {
			e.Start, e.End = e.Start.In(db.Berlin), e.End.In(db.Berlin)
			out = append(out, e)
		}
	}
	return out
}

// basisText says where an event's expected effect comes from.
func basisText(w *hourWhy) string {
	switch w.EventBasis {
	case "learned":
		if w.EventBasedOn != "" {
			return " Learned from " + w.EventBasedOn + "."
		}
		return " Learned from earlier occurrences."
	case "category":
		return " Based on " + w.EventBasedOn + " (no earlier occurrence of this event on record)."
	case "size":
		return " Rough estimate from the event size — no similar event on record yet."
	}
	return ""
}

// learnedEvent is what the forecaster learned from one past event series, seen from one store.
type learnedEvent struct {
	Series      string     `json:"series"`
	Name        string     `json:"name"`
	Category    string     `json:"category"`
	Occurrences int        `json:"occurrences"`
	LastStart   time.Time  `json:"last_start"`
	NextName    *string    `json:"next_name"`
	NextStart   *time.Time `json:"next_start"`
	StreetPeak  float64    `json:"street_peak"`
	StreetMean  float64    `json:"street_mean"`
	StoreMean   *float64   `json:"store_mean"`
	// For one store (dashboard only): mean multiplier at the store's own street sensor and for its shoppers,
	// over the hours the event affected, and the hour with the biggest street effect.
	AtStoreStreet *float64 `json:"at_store_street,omitempty"`
	AtStoreShop   *float64 `json:"at_store_shoppers,omitempty"`
	AtStorePeakH  *int     `json:"at_store_peak_hour,omitempty"`
}

func meanOf(m map[string]float64) (*float64, *int) {
	if len(m) == 0 {
		return nil, nil
	}
	var sum, best float64
	bestH := -1
	for k, v := range m {
		sum += v
		var h int
		if _, err := fmt.Sscan(k, &h); err == nil && (bestH < 0 || v > best) {
			best, bestH = v, h
		}
	}
	mean := round(sum/float64(len(m)), 2)
	if bestH < 0 {
		return &mean, nil
	}
	return &mean, &bestH
}

// learnedEvents: the forecaster's learned event series; with st set, only those touching the store's sensor,
// with the store-specific numbers filled in.
func (s *Server) learnedEvents(ctx context.Context, st *store) []learnedEvent {
	out := []learnedEvent{}
	rows, err := s.P.Query(ctx, `SELECT series, name, category, occurrences, last_start, next_name, next_start,
		street_peak, street_mean, store_mean, detail, sensor_ids FROM event_learned ORDER BY last_start DESC`)
	if err != nil { // forecaster has not created/filled the table yet
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var e learnedEvent
		var raw []byte
		var sensors []string
		if rows.Scan(&e.Series, &e.Name, &e.Category, &e.Occurrences, &e.LastStart, &e.NextName, &e.NextStart,
			&e.StreetPeak, &e.StreetMean, &e.StoreMean, &raw, &sensors) != nil {
			continue
		}
		e.LastStart = e.LastStart.In(db.Berlin)
		if e.NextStart != nil {
			t := e.NextStart.In(db.Berlin)
			e.NextStart = &t
		}
		e.StreetPeak, e.StreetMean = round(e.StreetPeak, 2), round(e.StreetMean, 2)
		if e.StoreMean != nil {
			e.StoreMean = ptr(round(*e.StoreMean, 2))
		}
		if st != nil {
			touches := false
			for _, id := range sensors {
				touches = touches || id == st.SensorID
			}
			if !touches {
				continue
			}
			var d struct {
				Sensors map[string]map[string]float64 `json:"sensors"`
				Stores  map[string]map[string]float64 `json:"stores"`
			}
			if json.Unmarshal(raw, &d) == nil {
				e.AtStoreStreet, e.AtStorePeakH = meanOf(d.Sensors[st.SensorID])
				e.AtStoreShop, _ = meanOf(d.Stores[st.ID])
			}
		}
		out = append(out, e)
	}
	return out
}

func (s *Server) getLearnedEvents(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.learnedEvents(r.Context(), nil))
}
