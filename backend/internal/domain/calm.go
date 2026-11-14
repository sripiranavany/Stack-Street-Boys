package domain

import (
	"math"
	"sort"
)

// CalmConfig configures a Calm Hours campaign.
type CalmConfig struct {
	CapPctOfPeak          float64 `json:"cap_pct_of_peak"`          // target cap as share of the day's forecast peak
	Acceptance            float64 `json:"acceptance"`               // share of offered customers who actually shift
	Tiers                 []int   `json:"tiers"`                    // discount % tiers, small -> large
	FlexHours             int     `json:"flex_hours"`               // max shift from the usual hour (same day)
	CustomersPerWeek      int     `json:"customers_per_week"`       // loyalty customers of the store
	WeeklyBudgetEUR       float64 `json:"weekly_budget_eur"`        // coupon budget
	AvgDiscountedSpendEUR float64 `json:"avg_discounted_spend_eur"` // basket share the discount applies to
}

// Cap is the target maximum visitors per hour for a day.
func (c CalmConfig) Cap(load [24]float64, open [24]bool) float64 {
	peak := 0.0
	for h := 0; h < 24; h++ {
		if open[h] && load[h] > peak {
			peak = load[h]
		}
	}
	return peak * c.CapPctOfPeak
}

// Discount picks a tier: the more free room in the target slot, the bigger the discount.
func (c CalmConfig) Discount(headroom, cap float64) int {
	if len(c.Tiers) == 0 || cap <= 0 {
		return 0
	}
	f := headroom / cap
	switch {
	case f > 0.5 && len(c.Tiers) >= 3:
		return c.Tiers[2]
	case f > 0.25 && len(c.Tiers) >= 2:
		return c.Tiers[1]
	default:
		return c.Tiers[0]
	}
}

// Allocate picks the slot for ONE customer whose usual hour is overloaded.
// extra[h] is the expected load already sent to h by earlier offers; removed[h] the expected load moved away.
// Returns ok=false when the usual hour is fine or no slot has room (then no offer is made).
func (c CalmConfig) Allocate(load [24]float64, open [24]bool, usual int, extra, removed [24]float64) (slot, discount int, ok bool) {
	cap := c.Cap(load, open)
	if usual < 0 || usual > 23 || load[usual]+extra[usual]-removed[usual] <= cap {
		return -1, 0, false
	}
	best, bestRoom := -1, 0.0
	for h := 0; h < 24; h++ {
		if !open[h] || h == usual || abs(h-usual) > c.FlexHours {
			continue
		}
		room := cap - (load[h] + extra[h] - removed[h])
		if room > bestRoom+1e-9 {
			best, bestRoom = h, room
		}
	}
	if best < 0 || bestRoom < c.Acceptance {
		return -1, 0, false
	}
	return best, c.Discount(bestRoom, cap), true
}

// ModeResult is one simulated load curve.
type ModeResult struct {
	Load          [24]float64 `json:"load"`
	Peak          float64     `json:"peak"`
	PeakHour      int         `json:"peak_hour"`
	OverloadHours float64     `json:"overload_customer_hours"`
	StaffHours    int         `json:"staff_hours"`
	Offers        int         `json:"offers"`
	Shifted       float64     `json:"shifted_customers"`
	CouponCostEUR float64     `json:"coupon_cost_eur"`
}

// SimResult compares no campaign, a naive "everyone to the quietest hour" campaign and the Ebb allocator.
type SimResult struct {
	Cap               float64    `json:"cap"`
	Open              [24]bool   `json:"open"`
	None              ModeResult `json:"none"`
	Naive             ModeResult `json:"naive"`
	Ebb               ModeResult `json:"ebb"`
	PeakReductionPct  float64    `json:"peak_reduction_pct"`
	NaivePeakDeltaPct float64    `json:"naive_peak_change_pct"`
	StaffHoursSaved   int        `json:"staff_hours_saved"`
	LoyaltyShare      float64    `json:"loyalty_share"`
}

// Simulate runs the store's loyalty customers of one day through both campaign styles (expected values).
func Simulate(c CalmConfig, s StoreConfig, load [24]float64, open [24]bool, loyaltyShare float64) SimResult {
	cap := c.Cap(load, open)
	res := SimResult{Cap: cap, Open: open, LoyaltyShare: loyaltyShare}
	res.None = finish(c, s, load, open, cap, 0, 0, 0)

	// naive: every overloaded customer is offered the single quietest open hour
	nl := load
	quiet, qv := -1, math.MaxFloat64
	for h := 0; h < 24; h++ {
		if open[h] && load[h] < qv {
			quiet, qv = h, load[h]
		}
	}
	offersN, shiftN := 0.0, 0.0
	for h := 0; h < 24; h++ {
		if open[h] && h != quiet && load[h] > cap {
			movable := load[h] * loyaltyShare
			moved := movable * c.Acceptance
			nl[h] -= moved
			nl[quiet] += moved
			offersN += movable
			shiftN += moved
		}
	}
	tier := 0
	if len(c.Tiers) > 0 {
		tier = c.Tiers[len(c.Tiers)-1]
	}
	res.Naive = finish(c, s, nl, open, cap, int(math.Round(offersN)), shiftN, shiftN*c.AvgDiscountedSpendEUR*float64(tier)/100)

	// Ebb: offers one customer at a time to the slot with most remaining room, only as many as needed
	var extra, removed [24]float64
	order := make([]int, 0, 24)
	for h := 0; h < 24; h++ {
		if open[h] && load[h] > cap {
			order = append(order, h)
		}
	}
	sort.Slice(order, func(i, j int) bool { return load[order[i]] > load[order[j]] })
	offersE, shiftE, cost := 0, 0.0, 0.0
	for _, h := range order {
		movable := int(load[h] * loyaltyShare)
		for i := 0; i < movable; i++ {
			slot, disc, ok := c.Allocate(load, open, h, extra, removed)
			if !ok {
				break
			}
			extra[slot] += c.Acceptance
			removed[h] += c.Acceptance
			offersE++
			shiftE += c.Acceptance
			cost += c.Acceptance * c.AvgDiscountedSpendEUR * float64(disc) / 100
		}
	}
	var el [24]float64
	for h := 0; h < 24; h++ {
		el[h] = load[h] + extra[h] - removed[h]
	}
	res.Ebb = finish(c, s, el, open, cap, offersE, shiftE, cost)
	if res.None.Peak > 0 {
		res.PeakReductionPct = (res.None.Peak - res.Ebb.Peak) / res.None.Peak * 100
		res.NaivePeakDeltaPct = (res.Naive.Peak - res.None.Peak) / res.None.Peak * 100
	}
	res.StaffHoursSaved = res.None.StaffHours - res.Ebb.StaffHours
	return res
}

func finish(c CalmConfig, s StoreConfig, load [24]float64, open [24]bool, cap float64, offers int, shifted, cost float64) ModeResult {
	m := ModeResult{Load: load, Offers: offers, Shifted: shifted, CouponCostEUR: math.Round(cost*100) / 100}
	for h := 0; h < 24; h++ {
		if !open[h] {
			m.Load[h] = 0
			continue
		}
		if load[h] > m.Peak {
			m.Peak, m.PeakHour = load[h], h
		}
		m.OverloadHours += math.Max(0, load[h]-cap)
		m.StaffHours += s.Cashiers(load[h])
	}
	return m
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
