package main

import (
	"testing"
	"time"
)

func TestWeeksBack(t *testing.T) {
	last := time.Date(2026, 11, 7, 23, 0, 0, 0, time.UTC)
	if got := weeksBack(last.Add(-time.Hour), last); got != 0 {
		t.Errorf("before end: got %s", got)
	}
	if got := weeksBack(last.Add(6*24*time.Hour), last); got != week {
		t.Errorf("6 days later: got %s", got)
	}
	if got := weeksBack(last.Add(8*24*time.Hour), last); got != 2*week {
		t.Errorf("8 days later: got %s", got)
	}
}

func TestMinuteIn(t *testing.T) {
	h := time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)
	hourly := map[int64]int{h.Unix(): 125}
	sum := 0
	for m := 0; m < 60; m++ {
		n := minuteIn(hourly, h.Add(time.Duration(m)*time.Minute))
		if n < 2 || n > 3 {
			t.Fatalf("minute %d: %d", m, n)
		}
		sum += n
	}
	if sum != 125 {
		t.Fatalf("minutes add up to %d, want 125", sum)
	}
	if minuteIn(hourly, h.Add(time.Hour)) != 0 {
		t.Fatal("hour without data should give 0")
	}
}
