package api

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"

	"ssb-backend/internal/db"
	"ssb-backend/internal/domain"
)

type store struct {
	ID       string             `json:"id"`
	Name     string             `json:"name"`
	SensorID string             `json:"sensor_id"`
	Label    string             `json:"sensor_label"`
	Lat      float64            `json:"lat"`
	Lon      float64            `json:"lon"`
	Config   domain.StoreConfig `json:"config"`
}

func (s *Server) loadStore(ctx context.Context, id string) (store, error) {
	var st store
	var cfg []byte
	err := s.P.QueryRow(ctx, `SELECT st.id, st.name, st.sensor_id, se.label, st.lat, st.lon, st.config
		FROM stores st JOIN sensors se ON se.id = st.sensor_id WHERE st.id=$1`, id).
		Scan(&st.ID, &st.Name, &st.SensorID, &st.Label, &st.Lat, &st.Lon, &cfg)
	if err != nil {
		return st, fmt.Errorf("store %q: %w", id, err)
	}
	return st, json.Unmarshal(cfg, &st.Config)
}

func (s *Server) loadCampaign(ctx context.Context, id string) (domain.CalmConfig, bool, error) {
	var c domain.CalmConfig
	var cfg []byte
	var active bool
	if err := s.P.QueryRow(ctx, `SELECT config, active FROM campaigns WHERE store_id=$1`, id).Scan(&cfg, &active); err != nil {
		return c, false, err
	}
	return c, active, json.Unmarshal(cfg, &c)
}

func (s *Server) getStores(w http.ResponseWriter, r *http.Request) {
	rows, err := s.P.Query(r.Context(), `SELECT id FROM stores ORDER BY name`)
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
	out := []store{}
	for _, id := range ids {
		if st, err := s.loadStore(r.Context(), id); err == nil {
			out = append(out, st)
		}
	}
	writeJSON(w, out)
}

func midnight(t time.Time) time.Time {
	t = t.In(db.Berlin)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, db.Berlin)
}

// dayDensity: hourly density for one day: actuals for past hours, latest forecast for future hours, typical as fallback.
func (s *Server) dayDensity(ctx context.Context, sensor string, day, now time.Time) ([24]float64, [24]string) {
	var d [24]float64
	var src [24]string
	if p := s.typicalProfile(ctx)[sensor]; p != nil {
		for h := 0; h < 24; h++ {
			d[h], src[h] = p[db.Weekday(day)][h], "typical"
		}
	}
	end := day.AddDate(0, 0, 1)
	if runID, _, _, err := s.latestRun(ctx, now); err == nil {
		rows, err := s.P.Query(ctx, `SELECT ts, p50 FROM forecasts WHERE run_id=$1 AND sensor_id=$2 AND ts >= $3 AND ts < $4`, runID, sensor, day, end)
		if err == nil {
			for rows.Next() {
				var ts time.Time
				var v float64
				if rows.Scan(&ts, &v) == nil {
					h := ts.In(db.Berlin).Hour()
					d[h], src[h] = v, "forecast"
				}
			}
			rows.Close()
		}
	}
	rows, err := s.P.Query(ctx, `SELECT date_trunc('hour', ts), avg(density) FROM readings
		WHERE sensor_id=$1 AND ts >= $2 AND ts < $3 AND ts <= $4 GROUP BY 1`, sensor, day, end, now)
	if err == nil {
		for rows.Next() {
			var ts time.Time
			var v float64
			if rows.Scan(&ts, &v) == nil && ts.Add(time.Hour).Before(now) {
				h := ts.In(db.Berlin).Hour()
				d[h], src[h] = v, "actual"
			}
		}
		rows.Close()
	}
	return d, src
}

// pickDay: explicit ?day=YYYY-MM-DD, otherwise today if the store is still open later, else the next open day.
func pickDay(r *http.Request, cfg domain.StoreConfig, now time.Time) (time.Time, error) {
	if v := r.URL.Query().Get("day"); v != "" {
		t, err := time.ParseInLocation("2006-01-02", v, db.Berlin)
		return t, err
	}
	day := midnight(now)
	if cfg.IsOpen(db.Weekday(now), now.Hour()) && now.Hour() < cfg.OpenTo-2 {
		return day, nil
	}
	for i := 1; i <= 7; i++ {
		d := day.AddDate(0, 0, i)
		if cfg.IsOpen(db.Weekday(d), cfg.OpenFrom) {
			return d, nil
		}
	}
	return day.AddDate(0, 0, 1), nil
}

// averages of a normal week for the fixed baseline plan and the loyalty share.
func (s *Server) normalWeek(ctx context.Context, st store) (avgOpenHourVisitors, avgDayBake, weekVisitors float64) {
	p := s.typicalProfile(ctx)[st.SensorID]
	if p == nil {
		return 0, 0, 0
	}
	n, days := 0, 0
	for d := 0; d < 7; d++ {
		open := false
		for h := 0; h < 24; h++ {
			if st.Config.IsOpen(d, h) {
				v := st.Config.Visitors(p[d][h])
				weekVisitors += v
				avgDayBake += v * st.Config.BakeryBuyRate * st.Config.ItemsPerBuyer
				n++
				open = true
			}
		}
		if open {
			days++
		}
	}
	if n > 0 {
		avgOpenHourVisitors = weekVisitors / float64(n)
	}
	if days > 0 {
		avgDayBake /= float64(days)
	}
	return
}

func (s *Server) getPlan(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	st0, err := s.now(ctx)
	if err != nil {
		fail(w, 500, err)
		return
	}
	st, err := s.loadStore(ctx, r.PathValue("id"))
	if err != nil {
		fail(w, 404, err)
		return
	}
	day, err := pickDay(r, st.Config, st0.SimNow)
	if err != nil {
		fail(w, 400, err)
		return
	}
	dens, src := s.dayDensity(ctx, st.SensorID, day, st0.SimNow)
	avgV, avgBake, _ := s.normalWeek(ctx, st)
	hours, k := domain.Plan(st.Config, db.Weekday(day), dens, avgV, avgBake)
	for i := range hours {
		hours[i].Density = round(hours[i].Density, 2)
		hours[i].Visitors = round(hours[i].Visitors, 1)
		hours[i].BakeDemand = round(hours[i].BakeDemand, 1)
	}
	k.VisitorsTotal, k.PeakVisitors, k.BakeDemand = round(k.VisitorsTotal, 0), round(k.PeakVisitors, 0), round(k.BakeDemand, 0)
	k.WasteEbb, k.WasteFixed, k.MissedFixed = round(k.WasteEbb, 0), round(k.WasteFixed, 0), round(k.MissedFixed, 0)
	writeJSON(w, map[string]any{"store": st, "day": day.Format("2006-01-02"), "weekday": day.Weekday().String(),
		"hours": hours, "sources": src, "kpis": k, "normal_open_hour_visitors": round(avgV, 1)})
}

func (s *Server) putStoreConfig(w http.ResponseWriter, r *http.Request) {
	var cfg domain.StoreConfig
	if err := readJSON(r, &cfg); err != nil {
		fail(w, 400, err)
		return
	}
	b, _ := json.Marshal(cfg)
	if _, err := s.P.Exec(r.Context(), `UPDATE stores SET config=$2 WHERE id=$1`, r.PathValue("id"), b); err != nil {
		fail(w, 500, err)
		return
	}
	writeJSON(w, cfg)
}

func (s *Server) getCampaign(w http.ResponseWriter, r *http.Request) {
	c, active, err := s.loadCampaign(r.Context(), r.PathValue("id"))
	if err != nil {
		fail(w, 404, err)
		return
	}
	writeJSON(w, map[string]any{"store_id": r.PathValue("id"), "active": active, "config": c})
}

func (s *Server) putCampaign(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Active bool              `json:"active"`
		Config domain.CalmConfig `json:"config"`
	}
	if err := readJSON(r, &req); err != nil {
		fail(w, 400, err)
		return
	}
	b, _ := json.Marshal(req.Config)
	if _, err := s.P.Exec(r.Context(), `INSERT INTO campaigns VALUES ($1,$2,$3)
		ON CONFLICT (store_id) DO UPDATE SET config=EXCLUDED.config, active=EXCLUDED.active`, r.PathValue("id"), b, req.Active); err != nil {
		fail(w, 500, err)
		return
	}
	writeJSON(w, req)
}

// ---------------------------------------------------------------- Calm Hours
func visitorsDay(cfg domain.StoreConfig, day time.Time, dens [24]float64) ([24]float64, [24]bool) {
	var load [24]float64
	var open [24]bool
	wd := db.Weekday(day)
	for h := 0; h < 24; h++ {
		open[h] = cfg.IsOpen(wd, h)
		if open[h] {
			load[h] = cfg.Visitors(dens[h])
		}
	}
	return load, open
}

func coupon() string {
	const al = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	for i := range b {
		b[i] = al[int(b[i])%len(al)]
	}
	return "EBB-" + string(b)
}

func (s *Server) postOffer(w http.ResponseWriter, r *http.Request) {
	var req struct {
		StoreID     string `json:"store_id"`
		CustomerRef string `json:"customer_ref"`
		UsualDow    int    `json:"usual_dow"`  // 0=Mon
		UsualHour   int    `json:"usual_hour"` // -1 = the day's forecast peak hour (demo customer)
	}
	if err := readJSON(r, &req); err != nil {
		fail(w, 400, err)
		return
	}
	ctx := r.Context()
	st0, err := s.now(ctx)
	if err != nil {
		fail(w, 500, err)
		return
	}
	now := st0.SimNow
	st, err := s.loadStore(ctx, req.StoreID)
	if err != nil {
		fail(w, 404, err)
		return
	}
	c, active, err := s.loadCampaign(ctx, req.StoreID)
	if err != nil || !active {
		writeJSON(w, map[string]any{"offer": nil, "reason": "No Calm Hours campaign active for this store."})
		return
	}
	// next occurrence of the customer's usual weekday that is still ahead
	day := midnight(now)
	for i := 0; i < 8 && (db.Weekday(day) != req.UsualDow || !day.AddDate(0, 0, 1).After(now)); i++ {
		day = day.AddDate(0, 0, 1)
	}
	dens, _ := s.dayDensity(ctx, st.SensorID, day, now)
	load, open := visitorsDay(st.Config, day, dens)
	usual := req.UsualHour
	if usual < 0 {
		for h := 0; h < 24; h++ {
			if open[h] && (usual < 0 || load[h] > load[usual]) {
				usual = h
			}
		}
	}
	if usual < 0 || !open[usual] {
		writeJSON(w, map[string]any{"offer": nil, "reason": "Store is closed at that time."})
		return
	}
	var extra, removed [24]float64
	rows, err := s.P.Query(ctx, `SELECT usual_slot, offer_slot FROM offers WHERE store_id=$1 AND offer_slot >= $2 AND offer_slot < $3`,
		st.ID, day, day.AddDate(0, 0, 1))
	if err == nil {
		for rows.Next() {
			var u, o time.Time
			if rows.Scan(&u, &o) == nil {
				extra[o.In(db.Berlin).Hour()] += c.Acceptance
				removed[u.In(db.Berlin).Hour()] += c.Acceptance
			}
		}
		rows.Close()
	}
	cap := c.Cap(load, open)
	usualSlot := day.Add(time.Duration(usual) * time.Hour)
	slot, disc, ok := c.Allocate(load, open, usual, extra, removed)
	if !ok {
		writeJSON(w, map[string]any{"offer": nil, "usual_slot": usualSlot, "usual_load": round(load[usual]+extra[usual]-removed[usual], 0),
			"cap": round(cap, 0), "reason": fmt.Sprintf("%s at %02d:00 is not expected to be overcrowded. Come as usual!", day.Weekday(), usual)})
		return
	}
	offerSlot := day.Add(time.Duration(slot) * time.Hour)
	code := coupon()
	if _, err := s.P.Exec(ctx, `INSERT INTO offers (store_id, customer_ref, usual_slot, offer_slot, discount, coupon_code) VALUES ($1,$2,$3,$4,$5,$6)`,
		st.ID, req.CustomerRef, usualSlot, offerSlot, disc, code); err != nil {
		fail(w, 500, err)
		return
	}
	slotLoad := load[slot] + extra[slot] - removed[slot]
	writeJSON(w, map[string]any{
		"offer": map[string]any{
			"store": st.Name, "slot_start": offerSlot, "slot_end": offerSlot.Add(time.Hour), "discount_pct": disc,
			"coupon_code": code, "applies_to": "bakery & fresh produce",
			"reason": fmt.Sprintf("Your usual %s %02d:00 is forecast to be busy (~%.0f shoppers/h). %02d:00 is calmer (~%.0f/h).",
				day.Weekday(), usual, load[usual]+extra[usual]-removed[usual], slot, slotLoad),
		},
		"usual_slot": usualSlot, "usual_load": round(load[usual]+extra[usual]-removed[usual], 0),
		"slot_load": round(slotLoad, 0), "cap": round(cap, 0),
	})
}

func (s *Server) postRedeem(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Code string `json:"coupon_code"`
	}
	if err := readJSON(r, &req); err != nil {
		fail(w, 400, err)
		return
	}
	tag, err := s.P.Exec(r.Context(), `UPDATE offers SET status='redeemed' WHERE coupon_code=$1 AND status='issued'`, req.Code)
	if err != nil {
		fail(w, 500, err)
		return
	}
	writeJSON(w, map[string]any{"redeemed": tag.RowsAffected() == 1})
}

func (s *Server) getOffers(w http.ResponseWriter, r *http.Request) {
	rows, err := s.P.Query(r.Context(), `SELECT customer_ref, usual_slot, offer_slot, discount, coupon_code, status
		FROM offers WHERE store_id=$1 ORDER BY id DESC`, r.URL.Query().Get("store_id"))
	if err != nil {
		fail(w, 500, err)
		return
	}
	defer rows.Close()
	type o struct {
		Customer string    `json:"customer_ref"`
		Usual    time.Time `json:"usual_slot"`
		Slot     time.Time `json:"offer_slot"`
		Discount int       `json:"discount_pct"`
		Code     string    `json:"coupon_code"`
		Status   string    `json:"status"`
	}
	var all []o
	bySlot := map[string]int{}
	redeemed := 0
	for rows.Next() {
		var x o
		if rows.Scan(&x.Customer, &x.Usual, &x.Slot, &x.Discount, &x.Code, &x.Status) == nil {
			x.Usual, x.Slot = x.Usual.In(db.Berlin), x.Slot.In(db.Berlin)
			all = append(all, x)
			bySlot[x.Slot.Format("Mon 15:04")]++
			if x.Status == "redeemed" {
				redeemed++
			}
		}
	}
	res := map[string]any{"issued": len(all), "redeemed": redeemed, "by_slot": bySlot, "recent": all}
	if len(all) > 0 {
		res["realised_acceptance"] = round(float64(redeemed)/float64(len(all)), 3)
	}
	if len(all) > 20 {
		res["recent"] = all[:20]
	}
	writeJSON(w, res)
}

func (s *Server) postSimulate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		StoreID    string   `json:"store_id"`
		Day        string   `json:"day"`
		Acceptance *float64 `json:"acceptance"`
		CapPct     *float64 `json:"cap_pct"`
	}
	if err := readJSON(r, &req); err != nil {
		fail(w, 400, err)
		return
	}
	ctx := r.Context()
	st0, err := s.now(ctx)
	if err != nil {
		fail(w, 500, err)
		return
	}
	st, err := s.loadStore(ctx, req.StoreID)
	if err != nil {
		fail(w, 404, err)
		return
	}
	c, _, err := s.loadCampaign(ctx, req.StoreID)
	if err != nil {
		fail(w, 404, err)
		return
	}
	if req.Acceptance != nil {
		c.Acceptance = math.Min(math.Max(*req.Acceptance, 0.01), 1)
	}
	if req.CapPct != nil {
		c.CapPctOfPeak = math.Min(math.Max(*req.CapPct, 0.3), 1)
	}
	if req.Day != "" {
		q := r.URL.Query()
		q.Set("day", req.Day)
		r.URL.RawQuery = q.Encode()
	}
	day, err := pickDay(r, st.Config, st0.SimNow)
	if err != nil {
		fail(w, 400, err)
		return
	}
	dens, _ := s.dayDensity(ctx, st.SensorID, day, st0.SimNow)
	load, open := visitorsDay(st.Config, day, dens)
	_, _, weekVisitors := s.normalWeek(ctx, st)
	share := 0.5
	if weekVisitors > 0 {
		share = math.Min(float64(c.CustomersPerWeek)/weekVisitors, 0.9)
	}
	res := domain.Simulate(c, st.Config, load, open, share)
	writeJSON(w, map[string]any{"store": st.Name, "day": day.Format("2006-01-02"), "weekday": day.Weekday().String(),
		"acceptance": c.Acceptance, "cap_pct": c.CapPctOfPeak, "result": res})
}

// ---------------------------------------------------------------- ad slots & event planner
type slot struct {
	SensorID    string    `json:"sensor_id"`
	Label       string    `json:"label"`
	Start       time.Time `json:"start"`
	End         time.Time `json:"end"`
	AvgDensity  float64   `json:"avg_density"`
	Exposure    float64   `json:"exposure_people_hours"`
	Weather     string    `json:"weather"`
	WeatherEff  float64   `json:"weather_effect"`
	Event       string    `json:"event,omitempty"`
}

type fcRow struct {
	sensor, label string
	ts            time.Time
	p50           float64
	reasons       map[string]any
}

func (s *Server) futureForecast(ctx context.Context, now time.Time, sensors []string) ([]fcRow, error) {
	runID, _, _, err := s.latestRun(ctx, now)
	if err != nil {
		return nil, err
	}
	if sensors == nil {
		sensors = []string{} // NULL would match nothing
	}
	rows, err := s.P.Query(ctx, `SELECT f.sensor_id, se.label, f.ts, f.p50, f.reasons FROM forecasts f JOIN sensors se ON se.id=f.sensor_id
		WHERE f.run_id=$1 AND f.ts > $2 AND (cardinality($3::text[]) = 0 OR f.sensor_id = ANY($3)) ORDER BY f.sensor_id, f.ts`, runID, now, sensors)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []fcRow
	for rows.Next() {
		var x fcRow
		var rb []byte
		if rows.Scan(&x.sensor, &x.label, &x.ts, &x.p50, &rb) == nil {
			x.ts = x.ts.In(db.Berlin)
			_ = json.Unmarshal(rb, &x.reasons)
			out = append(out, x)
		}
	}
	return out, rows.Err()
}

func eventName(r map[string]any) string {
	if e, ok := r["event"].(map[string]any); ok {
		if n, ok := e["name"].(string); ok {
			return n
		}
	}
	return ""
}

func (s *Server) getAdSlots(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	st, err := s.now(ctx)
	if err != nil {
		fail(w, 500, err)
		return
	}
	var sensors []string
	if v := r.URL.Query().Get("location"); v != "" {
		sensors = strings.Split(v, ",")
	}
	rows, err := s.futureForecast(ctx, st.SimNow, sensors)
	if err != nil {
		writeJSON(w, []slot{})
		return
	}
	blocks := map[string]*slot{}
	counts := map[string]int{}
	for _, x := range rows {
		h := x.ts.Hour()
		if h < 6 {
			continue
		}
		start := time.Date(x.ts.Year(), x.ts.Month(), x.ts.Day(), h/3*3, 0, 0, 0, db.Berlin)
		key := x.sensor + start.Format(time.RFC3339)
		b := blocks[key]
		if b == nil {
			b = &slot{SensorID: x.sensor, Label: x.label, Start: start, End: start.Add(3 * time.Hour)}
			blocks[key] = b
		}
		b.Exposure += x.p50
		counts[key]++
		if we, ok := x.reasons["weather"].(float64); ok {
			b.WeatherEff += we
		}
		if h%3 == 1 {
			if d, ok := x.reasons["weather_desc"].(string); ok {
				b.Weather = d
			}
		}
		if e := eventName(x.reasons); e != "" {
			b.Event = e
		}
	}
	out := []slot{}
	for k, b := range blocks {
		n := float64(counts[k])
		if n < 3 {
			continue
		}
		b.AvgDensity = round(b.Exposure/n, 1)
		b.Exposure = round(b.Exposure, 0)
		b.WeatherEff = round(b.WeatherEff/n, 3)
		out = append(out, *b)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].AvgDensity > out[j].AvgDensity })
	if n := qInt(r, "limit", 15); len(out) > n {
		out = out[:n]
	}
	writeJSON(w, out)
}

func (s *Server) postEventPlanner(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SensorIDs []string `json:"sensor_ids"`
		Duration  int      `json:"duration_h"`
		Goal      string   `json:"goal"` // maximize | avoid
		FromHour  int      `json:"from_hour"`
		ToHour    int      `json:"to_hour"`
	}
	if err := readJSON(r, &req); err != nil {
		fail(w, 400, err)
		return
	}
	if req.Duration <= 0 {
		req.Duration = 3
	}
	if req.FromHour == 0 && req.ToHour == 0 {
		req.FromHour, req.ToHour = 10, 22
	}
	ctx := r.Context()
	st, err := s.now(ctx)
	if err != nil {
		fail(w, 500, err)
		return
	}
	rows, err := s.futureForecast(ctx, st.SimNow, req.SensorIDs)
	if err != nil {
		writeJSON(w, []any{})
		return
	}
	sum := map[time.Time]float64{}
	desc := map[time.Time]string{}
	for _, x := range rows {
		sum[x.ts] += x.p50
		if d, ok := x.reasons["weather_desc"].(string); ok {
			desc[x.ts] = d
		}
	}
	n := float64(max(len(req.SensorIDs), 1))
	if len(req.SensorIDs) == 0 {
		n = 8
	}
	type win struct {
		Start      time.Time `json:"start"`
		End        time.Time `json:"end"`
		AvgDensity float64   `json:"avg_density_per_location"`
		Weather    string    `json:"weather"`
	}
	var wins []win
	for t := range sum {
		if t.Hour() < req.FromHour || t.Hour()+req.Duration > req.ToHour {
			continue
		}
		tot, ok := 0.0, true
		for i := 0; i < req.Duration; i++ {
			v, has := sum[t.Add(time.Duration(i)*time.Hour)]
			if !has {
				ok = false
				break
			}
			tot += v
		}
		if ok {
			wins = append(wins, win{Start: t, End: t.Add(time.Duration(req.Duration) * time.Hour),
				AvgDensity: round(tot/float64(req.Duration)/n, 1), Weather: desc[t]})
		}
	}
	sort.Slice(wins, func(i, j int) bool {
		if req.Goal == "avoid" {
			return wins[i].AvgDensity < wins[j].AvgDensity
		}
		return wins[i].AvgDensity > wins[j].AvgDensity
	})
	var out []win
	for _, c := range wins { // pick non-overlapping windows
		clash := false
		for _, o := range out {
			if c.Start.Before(o.End) && o.Start.Before(c.End) {
				clash = true
				break
			}
		}
		if !clash {
			out = append(out, c)
		}
		if len(out) == 5 {
			break
		}
	}
	if out == nil {
		out = []win{}
	}
	writeJSON(w, out)
}
