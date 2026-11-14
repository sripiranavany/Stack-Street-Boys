package ingest

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func f(v float64) *float64 { return &v }

func TestDensityValidate(t *testing.T) {
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	ok := DensityMsg{SchemaVersion: 1, SensorID: "f4928534", TS: now.Add(-90 * time.Second), Density: f(12.5)}
	if err := ok.Validate(now); err != nil {
		t.Fatal(err)
	}
	if !ok.TS.Equal(now.Add(-2 * time.Minute)) {
		t.Errorf("ts not truncated to the minute: %s", ok.TS)
	}
	bad := []struct {
		m    DensityMsg
		want string
	}{
		{DensityMsg{TS: now, Density: f(1)}, "sensor_id"},
		{DensityMsg{SensorID: "x", Density: f(1)}, "ts missing"},
		{DensityMsg{SensorID: "x", TS: now.Add(time.Hour), Density: f(1)}, "future"},
		{DensityMsg{SensorID: "x", TS: now}, "neither"},
		{DensityMsg{SensorID: "x", TS: now, Density: f(-1)}, "density"},
		{DensityMsg{SensorID: "x", TS: now, Battery: f(140)}, "battery"},
		{DensityMsg{SchemaVersion: 9, SensorID: "x", TS: now, Density: f(1)}, "schema_version"},
	}
	for _, c := range bad {
		if err := c.m.Validate(now); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%+v: got %v, want error containing %q", c.m, err, c.want)
		}
	}
}

func TestDoorValidate(t *testing.T) {
	now := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	m := DoorMsg{StoreID: "s", TS: now, In: 3, Out: 1}
	if err := m.Validate(now); err != nil || m.Door != "main" {
		t.Fatalf("got %v, door %q", err, m.Door)
	}
	m = DoorMsg{StoreID: "s", TS: now, In: -1}
	if err := m.Validate(now); err == nil {
		t.Fatal("negative count accepted")
	}
}

func TestMessagesJSON(t *testing.T) {
	var m DensityMsg
	if err := json.Unmarshal([]byte(`{"schema_version":1,"sensor_id":"a","ts":"2026-11-14T12:00:30+01:00","density":4.2}`), &m); err != nil {
		t.Fatal(err)
	}
	if m.Battery != nil || *m.Density != 4.2 {
		t.Fatalf("got %+v", m)
	}
}

func TestTouchedHours(t *testing.T) {
	base := time.Date(2026, 11, 14, 9, 0, 0, 0, time.UTC)
	got := touchedHours([]DoorMsg{
		{StoreID: "b", TS: base.Add(5 * time.Minute)},
		{StoreID: "a", TS: base.Add(70 * time.Minute)},
		{StoreID: "a", TS: base.Add(10 * time.Minute)},
		{StoreID: "a", TS: base.Add(59 * time.Minute)},
	})
	if len(got) != 3 || got[0].store != "a" || !got[0].hour.Equal(base) || !got[1].hour.Equal(base.Add(time.Hour)) || got[2].store != "b" {
		t.Fatalf("got %+v", got)
	}
}
