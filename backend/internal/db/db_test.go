package db

import (
	"testing"
	"time"
)

// The worker adds a fraction of a second to the replay clock on every tick; the stored value must keep it.
func TestClockKeepsFractions(t *testing.T) {
	start := time.Date(2026, 11, 2, 14, 0, 0, 0, Berlin)
	now := start
	for i := 0; i < 10; i++ {
		now = parseT(FormatClock(now.Add(400 * time.Millisecond)))
	}
	if got := now.Sub(start); got != 4*time.Second {
		t.Fatalf("10 ticks of 0.4 s advanced the clock by %s, want 4s", got)
	}
}
