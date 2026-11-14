package domain

import "testing"

func TestOccupancy(t *testing.T) {
	c := testStore()
	if got := c.Occupancy(240); got != 60 { // 240 entries/h x 15 min default stay
		t.Fatalf("Occupancy(240) = %v, want 60", got)
	}
	c.VisitMinutes = 20
	if got := c.Occupancy(90); got != 30 {
		t.Fatalf("Occupancy(90) with 20 min = %v, want 30", got)
	}
	if got := c.Occupancy(-5); got != 0 {
		t.Fatalf("negative entries should give 0, got %v", got)
	}
	if c.CapacityOrDefault() != DefaultCapacity {
		t.Fatalf("default capacity not used")
	}
	c.Capacity = 80
	if c.CapacityOrDefault() != 80 {
		t.Fatalf("configured capacity not used")
	}
}

func TestLevel(t *testing.T) {
	cases := []struct {
		occ  float64
		open bool
		want string
	}{
		{10, false, LevelClosed},
		{69, true, LevelComfortable},
		{70, true, LevelBusy},
		{89.9, true, LevelBusy},
		{90, true, LevelNearCapacity},
		{130, true, LevelNearCapacity},
	}
	for _, c := range cases {
		if got := Level(c.occ, 100, c.open); got != c.want {
			t.Errorf("Level(%v, 100, %v) = %q, want %q", c.occ, c.open, got, c.want)
		}
	}
}
