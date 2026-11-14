package api

import (
	"strings"
	"testing"
)

func openHours(from, to int) []dashHour {
	hs := make([]dashHour, 24)
	for h := range hs {
		hs[h] = dashHour{Hour: h, Open: h >= from && h < to}
	}
	return hs
}

func titles(as []dashAlert) string {
	var t []string
	for _, a := range as {
		t = append(t, a.Title)
	}
	return strings.Join(t, " | ")
}

func TestCauseAlerts(t *testing.T) {
	var dc dayCtx
	for h := 14; h < 22; h++ {
		w := dc.why(h)
		w.Weather, w.Precip, w.WeatherEffect = "9°C, 3.0 mm rain, 100% cloud", ptr(3), ptr(-0.15)
	}
	dc.why(18).Event, dc.why(18).EventMult = "Lichterfest", ptr(1.4)
	dc.why(19).Event = "Lichterfest"
	dc.SchoolHoliday = "Autumn Holidays"

	got := titles(causeAlerts(dc, openHours(7, 22), 14, "All Saints' Day"))
	for _, want := range []string{"Lichterfest from 18:00", "Weather keeps about 15% of people away", "School holidays: Autumn Holidays",
		"Closed tomorrow — All Saints' Day"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
	if strings.Count(got, "Lichterfest") != 1 {
		t.Errorf("event should be announced once: %q", got)
	}
}

func TestCauseAlertsPublicHoliday(t *testing.T) {
	dc := dayCtx{PublicHoliday: "Christmas Day", SchoolHoliday: "Christmas Holidays"}
	hours := openHours(0, 0)
	hours[10].Closed = "Christmas Day" // the store would normally be open
	if got := titles(causeAlerts(dc, hours, 0, "")); got != "Closed — Christmas Day" {
		t.Errorf("got %q", got)
	}
	// a holiday on a day the store is closed anyway (Sunday) needs no alert
	if got := causeAlerts(dc, openHours(0, 0), 0, ""); len(got) != 0 {
		t.Errorf("expected no alerts, got %q", titles(got))
	}
}

func TestCauseAlertsNothingSpecial(t *testing.T) {
	if got := causeAlerts(dayCtx{}, openHours(7, 22), 0, ""); len(got) != 0 {
		t.Errorf("expected no alerts, got %q", titles(got))
	}
}

func TestWeatherDesc(t *testing.T) {
	if got := weatherDesc(ptr(7.2), ptr(0), ptr(40)); got != "7°C, dry, 40% cloud" {
		t.Errorf("got %q", got)
	}
	if got := weatherDesc(nil, ptr(2.5), nil); got != "2.5 mm rain" {
		t.Errorf("got %q", got)
	}
}

func TestLookBack(t *testing.T) {
	hours := openHours(7, 22)
	var dc dayCtx
	for h := 7; h < 12; h++ {
		w := dc.why(h)
		w.MeasuredVsUsual, w.SchoolEffect, w.WeatherEffect = ptr(0.3), ptr(0.12), ptr(-0.03)
	}
	a := lookBack(dc, hours, 12)
	if a == nil || a.Title != "So far today: busier than usual" || !strings.Contains(a.Detail, "school holidays") {
		t.Fatalf("got %+v", a)
	}
	// unexplained and quieter
	var dc2 dayCtx
	for h := 7; h < 10; h++ {
		dc2.why(h).MeasuredVsUsual = ptr(-0.25)
	}
	a = lookBack(dc2, hours, 10)
	if a == nil || a.Title != "So far today: quieter than usual" || !strings.Contains(a.Detail, "No holiday") {
		t.Fatalf("got %+v", a)
	}
	// normal day
	var dc3 dayCtx
	dc3.why(8).MeasuredVsUsual = ptr(0.05)
	if a := lookBack(dc3, hours, 10); a != nil {
		t.Fatalf("expected nil, got %+v", a)
	}
}

func TestBasisText(t *testing.T) {
	cases := map[string]hourWhy{
		" Learned from Heilbronner Lichterfest 2026.":                                     {EventBasis: "learned", EventBasedOn: "Heilbronner Lichterfest 2026"},
		" Based on past festival events (no earlier occurrence of this event on record).": {EventBasis: "category", EventBasedOn: "past festival events"},
		" Rough estimate from the event size — no similar event on record yet.":           {EventBasis: "size"},
		"": {},
	}
	for want, w := range cases {
		if got := basisText(&w); got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
}

func TestMeanOf(t *testing.T) {
	m, h := meanOf(map[string]float64{"17": 1.5, "18": 3.5, "19": 1.0})
	if m == nil || *m != 2 || h == nil || *h != 18 {
		t.Fatalf("got %v %v", m, h)
	}
	if m, h := meanOf(nil); m != nil || h != nil {
		t.Fatal("empty map should give nil")
	}
}
