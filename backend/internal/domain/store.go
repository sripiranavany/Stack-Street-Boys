// Package domain holds Ebb's pure business logic (no I/O), so it can be unit-tested.
package domain

import "math"

// StoreConfig holds the explicit, configurable assumptions that translate street density into store numbers.
type StoreConfig struct {
	OpenDays             []int   `json:"open_days"` // 0=Mon ... 6=Sun
	OpenFrom             int     `json:"open_from"`
	OpenTo               int     `json:"open_to"`
	DwellSeconds         float64 `json:"dwell_seconds"`
	CaptureRate          float64 `json:"capture_rate"`
	CustomersPerCashierH float64 `json:"customers_per_cashier_h"`
	MinStaff             int     `json:"min_staff"`
	BakeryBuyRate        float64 `json:"bakery_buy_rate"`
	ItemsPerBuyer        float64 `json:"items_per_buyer"`
	BatchSize            int     `json:"batch_size"`
	BakeLeadMin          int     `json:"bake_lead_min"`
}

// IsOpen reports whether the store is open at weekday (0=Mon) and hour.
func (c StoreConfig) IsOpen(weekday, hour int) bool {
	day := false
	for _, d := range c.OpenDays {
		if d == weekday {
			day = true
		}
	}
	return day && hour >= c.OpenFrom && hour < c.OpenTo
}

// Visitors converts mean density (people within ~10 m) into expected store visitors for one hour.
// passers-by/h = density * 3600 / dwell ; visitors = passers-by * capture rate.
func (c StoreConfig) Visitors(density float64) float64 {
	if c.DwellSeconds <= 0 {
		return 0
	}
	return density * 3600 / c.DwellSeconds * c.CaptureRate
}

// Cashiers needed for an hour with the given visitors.
func (c StoreConfig) Cashiers(visitors float64) int {
	n := int(math.Ceil(visitors / math.Max(c.CustomersPerCashierH, 1)))
	if n < c.MinStaff {
		n = c.MinStaff
	}
	return n
}

// HourPlan is one hour of a store plan.
type HourPlan struct {
	Hour       int     `json:"hour"`
	Open       bool    `json:"open"`
	Density    float64 `json:"density"`
	Visitors   float64 `json:"visitors"`
	Cashiers   int     `json:"cashiers"`
	FixedCash  int     `json:"fixed_cashiers"`
	BakeDemand float64 `json:"bake_demand"`
	Batches    int     `json:"batches"`
}

// PlanKPIs summarise Ebb's plan against a fixed "average day" plan.
type PlanKPIs struct {
	VisitorsTotal     float64 `json:"visitors_total"`
	PeakHour          int     `json:"peak_hour"`
	PeakVisitors      float64 `json:"peak_visitors"`
	StaffHoursEbb     int     `json:"staff_hours_ebb"`
	StaffHoursFixed   int     `json:"staff_hours_fixed"`
	UnderstaffedFixed int     `json:"understaffed_hours_fixed"`
	OverstaffedFixed  int     `json:"overstaffed_hours_fixed"`
	BakeItemsEbb      int     `json:"bake_items_ebb"`
	BakeItemsFixed    int     `json:"bake_items_fixed"`
	BakeDemand        float64 `json:"bake_demand_total"`
	WasteEbb          float64 `json:"waste_items_ebb"`
	WasteFixed        float64 `json:"waste_items_fixed"`
	MissedFixed       float64 `json:"missed_sales_items_fixed"`
}

// Plan builds the staffing + bake plan for one day from hourly density (index = hour 0..23).
// avgOpenHourVisitors / avgDayBakeDemand describe an average day (the fixed baseline plan).
func Plan(c StoreConfig, weekday int, density [24]float64, avgOpenHourVisitors, avgDayBakeDemand float64) ([]HourPlan, PlanKPIs) {
	var k PlanKPIs
	fixedCash := c.Cashiers(avgOpenHourVisitors)
	out := make([]HourPlan, 24)
	for h := 0; h < 24; h++ {
		p := HourPlan{Hour: h, Open: c.IsOpen(weekday, h), Density: density[h]}
		if p.Open {
			p.Visitors = c.Visitors(density[h])
			p.Cashiers = c.Cashiers(p.Visitors)
			p.FixedCash = fixedCash
			p.BakeDemand = p.Visitors * c.BakeryBuyRate * c.ItemsPerBuyer
			k.VisitorsTotal += p.Visitors
			k.BakeDemand += p.BakeDemand
			k.StaffHoursEbb += p.Cashiers
			k.StaffHoursFixed += fixedCash
			if fixedCash < p.Cashiers {
				k.UnderstaffedFixed++
			} else if fixedCash > p.Cashiers {
				k.OverstaffedFixed++
			}
			if p.Visitors > k.PeakVisitors {
				k.PeakVisitors, k.PeakHour = p.Visitors, h
			}
		}
		out[h] = p
	}
	// Ebb bake plan: bake ahead of each hour's demand in whole batches, carrying stock forward.
	lead := int(math.Ceil(float64(c.BakeLeadMin) / 60))
	stock := 0.0
	bs := float64(max(c.BatchSize, 1))
	for h := 0; h < 24; h++ {
		if !out[h].Open {
			continue
		}
		need := out[h].BakeDemand - stock
		if need > 0 {
			b := int(math.Ceil(need / bs))
			at := h - lead
			if at < 0 {
				at = 0
			}
			out[at].Batches += b
			stock += float64(b) * bs
			k.BakeItemsEbb += b * int(bs)
		}
		stock -= out[h].BakeDemand
	}
	k.WasteEbb = math.Max(stock, 0)
	// Fixed plan: same quantity every day = average day demand, rounded up to batches.
	k.BakeItemsFixed = int(math.Ceil(avgDayBakeDemand/bs)) * int(bs)
	diff := float64(k.BakeItemsFixed) - k.BakeDemand
	k.WasteFixed = math.Max(diff, 0)
	k.MissedFixed = math.Max(-diff, 0)
	return out, k
}
