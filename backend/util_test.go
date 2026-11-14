package main

import (
	"math"
	"testing"
	"time"
)

func TestParseTime(t *testing.T) {
	cases := map[string]string{
		"2026-11-07":                       "2026-11-07T00:00:00Z",
		"2026-11-07T12:30:00Z":             "2026-11-07T12:30:00Z",
		"2026-11-07T12:30:00+01:00":        "2026-11-07T11:30:00Z",
		"2026-11-07T12:30:00 01:00":        "2026-11-07T11:30:00Z", // "+" decoded as a space in a URL
		"2026-11-07 12:30:00":              "2026-11-07T12:30:00Z",
		"2026-06-12T12:27:15.278588+00:00": "2026-06-12T12:27:15.278588Z",
	}
	for in, want := range cases {
		got, err := parseTime(in)
		if err != nil {
			t.Errorf("parseTime(%q): %v", in, err)
			continue
		}
		if g := got.Format(time.RFC3339Nano); g != want {
			t.Errorf("parseTime(%q) = %s, want %s", in, g, want)
		}
	}
	if _, err := parseTime("yesterday"); err == nil {
		t.Error("expected an error for 'yesterday'")
	}
}

func TestParseHorizon(t *testing.T) {
	cases := map[string]time.Duration{
		"72h":  72 * time.Hour,
		"3d":   72 * time.Hour,
		"1.5d": 36 * time.Hour,
		"90m":  90 * time.Minute,
	}
	for in, want := range cases {
		got, err := parseHorizon(in)
		if err != nil || got != want {
			t.Errorf("parseHorizon(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "abc", "-5h", "0d"} {
		if _, err := parseHorizon(bad); err == nil {
			t.Errorf("parseHorizon(%q) should fail", bad)
		}
	}
}

func TestPercentile(t *testing.T) {
	vals := []float64{5, 1, 3, 2, 4}
	if got := percentile(vals, 0.5); got != 3 {
		t.Errorf("median = %v", got)
	}
	if got := percentile(vals, 1); got != 5 {
		t.Errorf("max = %v", got)
	}
	if got := percentile(vals, 0.95); math.Abs(got-4.8) > 1e-9 {
		t.Errorf("p95 = %v", got)
	}
	if got := percentile(nil, 0.95); got != 0 {
		t.Errorf("empty = %v", got)
	}
	if vals[0] != 5 {
		t.Error("percentile must not reorder its input")
	}
}

func TestFillBuckets(t *testing.T) {
	from := time.Date(2026, 11, 7, 10, 7, 0, 0, time.UTC) // starts inside a bucket
	to := time.Date(2026, 11, 7, 11, 0, 0, 0, time.UTC)
	v := 4.2
	got := map[int64]point{
		time.Date(2026, 11, 7, 10, 15, 0, 0, time.UTC).Unix(): {Time: time.Date(2026, 11, 7, 10, 15, 0, 0, time.UTC), Value: &v, N: 3},
	}
	pts := fillBuckets(from, to, 15*time.Minute, got)
	if len(pts) != 4 { // 10:00, 10:15, 10:30, 10:45
		t.Fatalf("got %d points, want 4", len(pts))
	}
	if pts[0].Value != nil || pts[2].Value != nil || pts[3].Value != nil {
		t.Error("missing buckets must be null, not 0")
	}
	if pts[1].Value == nil || *pts[1].Value != 4.2 || pts[1].N != 3 {
		t.Errorf("existing bucket lost: %+v", pts[1])
	}
	if !pts[0].Time.Equal(time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)) {
		t.Errorf("first bucket = %v", pts[0].Time)
	}
	days := fillBuckets(time.Date(2026, 11, 1, 5, 0, 0, 0, time.UTC), time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC), 24*time.Hour, nil)
	if len(days) != 3 || !days[0].Time.Equal(time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("daily buckets wrong: %+v", days)
	}
}

func TestQueryBuilder(t *testing.T) {
	q := &qb{}
	q.add("SELECT 1 WHERE a = " + q.p("x") + " AND b = " + q.p(5))
	if got := q.String(); got != "SELECT 1 WHERE a = $1 AND b = $2 " {
		t.Errorf("sql = %q", got)
	}
	if len(q.args) != 2 || q.args[0] != "x" || q.args[1] != 5 {
		t.Errorf("args = %v", q.args)
	}
}

func TestBerlinTimeZone(t *testing.T) {
	if berlin.String() != "Europe/Berlin" {
		t.Fatalf("time zone database missing: %s", berlin)
	}
	// 2026-11-07 is winter time (UTC+1)
	local := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC).In(berlin)
	if local.Hour() != 13 {
		t.Errorf("hour = %d", local.Hour())
	}
}
