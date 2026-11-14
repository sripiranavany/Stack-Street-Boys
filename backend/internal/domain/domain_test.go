package domain

import "testing"

func testStore() StoreConfig {
	return StoreConfig{OpenDays: []int{0, 1, 2, 3, 4, 5}, OpenFrom: 7, OpenTo: 22, DwellSeconds: 30, CaptureRate: 0.03,
		CustomersPerCashierH: 40, MinStaff: 1, BakeryBuyRate: 0.3, ItemsPerBuyer: 2, BatchSize: 40, BakeLeadMin: 30}
}

func testCalm() CalmConfig {
	return CalmConfig{CapPctOfPeak: 0.8, Acceptance: 0.25, Tiers: []int{10, 15, 20}, FlexHours: 4,
		CustomersPerWeek: 3000, WeeklyBudgetEUR: 600, AvgDiscountedSpendEUR: 6}
}

// a Saturday-like curve: quiet morning, sharp 16-19h peak
func peakyDay() ([24]float64, [24]bool) {
	var load [24]float64
	var open [24]bool
	vals := map[int]float64{7: 20, 8: 30, 9: 40, 10: 50, 11: 60, 12: 70, 13: 70, 14: 75, 15: 90, 16: 130, 17: 160, 18: 150, 19: 110, 20: 70, 21: 40}
	for h, v := range vals {
		load[h] = v
		open[h] = true
	}
	return load, open
}

func TestVisitorsAndCashiers(t *testing.T) {
	s := testStore()
	if v := s.Visitors(20); v < 71.9 || v > 72.1 { // 20 * 120 * 0.03
		t.Fatalf("visitors = %v", v)
	}
	if n := s.Cashiers(72); n != 2 {
		t.Fatalf("cashiers = %d", n)
	}
	if n := s.Cashiers(0); n != 1 {
		t.Fatalf("min staff not applied: %d", n)
	}
}

func TestPlanBakeCoversDemand(t *testing.T) {
	var d [24]float64
	for h := 7; h < 22; h++ {
		d[h] = 20
	}
	_, k := Plan(testStore(), 5, d, 72, 600)
	if float64(k.BakeItemsEbb) < k.BakeDemand {
		t.Fatalf("baked %d < demand %.0f", k.BakeItemsEbb, k.BakeDemand)
	}
	if k.WasteEbb >= 40 {
		t.Fatalf("waste should be below one batch, got %.1f", k.WasteEbb)
	}
}

func TestEbbNeverExceedsCapButNaiveCreatesNewPeak(t *testing.T) {
	load, open := peakyDay()
	c := testCalm()
	c.Acceptance = 0.6 // aggressive response makes herding visible
	r := Simulate(c, testStore(), load, open, 0.5)
	if r.Ebb.Peak > r.None.Peak {
		t.Fatalf("ebb peak %.1f above baseline %.1f", r.Ebb.Peak, r.None.Peak)
	}
	for h := 0; h < 24; h++ {
		if open[h] && load[h] <= r.Cap && r.Ebb.Load[h] > r.Cap+1e-6 {
			t.Fatalf("ebb overloaded previously-fine hour %d: %.1f > cap %.1f", h, r.Ebb.Load[h], r.Cap)
		}
	}
	if r.Naive.PeakHour == r.None.PeakHour && r.Naive.Peak <= r.Ebb.Peak {
		t.Fatalf("naive should either move the peak or stay worse than ebb")
	}
	if r.PeakReductionPct <= 0 {
		t.Fatalf("expected peak reduction, got %.1f", r.PeakReductionPct)
	}
}

func TestAllocateSpreadsCustomers(t *testing.T) {
	load, open := peakyDay()
	c := testCalm()
	var extra, removed [24]float64
	seen := map[int]bool{}
	for i := 0; i < 200; i++ {
		slot, _, ok := c.Allocate(load, open, 17, extra, removed)
		if !ok {
			break
		}
		seen[slot] = true
		extra[slot] += c.Acceptance
		removed[17] += c.Acceptance
	}
	if len(seen) < 2 {
		t.Fatalf("allocator herded everyone into %v", seen)
	}
}
