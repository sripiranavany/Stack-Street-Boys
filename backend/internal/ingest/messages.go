// Package ingest consumes sensor data from Kafka and writes it to the ebb tables.
//
// Topics (keyed by sensor / store id, JSON values):
//
//	sensors.density  DensityMsg: street people-density sensors (one message per reading)
//	stores.door      DoorMsg:    door counters (one message per door and minute)
//	sensors.dlq      messages that could not be parsed or failed validation, with the reason in a header
//
// Writes are idempotent (upserts keyed by sensor/store, door and timestamp), so redelivery after a crash, a replay
// from the start of a topic, or several backend replicas in the same consumer group are all safe.
package ingest

import (
	"errors"
	"fmt"
	"time"
)

const (
	TopicDensity = "sensors.density"
	TopicDoor    = "stores.door"
	TopicDLQ     = "sensors.dlq"

	// SchemaVersion of the messages below. Bump it on breaking changes; the consumer rejects newer versions.
	SchemaVersion = 1

	maxFuture = 10 * time.Minute // sensor clocks drift; anything further ahead is rejected
)

// DensityMsg is one street-sensor reading. Density, battery or both may be present.
type DensityMsg struct {
	SchemaVersion int       `json:"schema_version"`
	SensorID      string    `json:"sensor_id"`
	TS            time.Time `json:"ts"`
	Density       *float64  `json:"density,omitempty"` // people within ~10 m
	Battery       *float64  `json:"battery,omitempty"` // percent
}

// DoorMsg counts people passing one store door during one minute.
type DoorMsg struct {
	SchemaVersion int       `json:"schema_version"`
	StoreID       string    `json:"store_id"`
	Door          string    `json:"door"`
	TS            time.Time `json:"ts"` // start of the minute
	In            int       `json:"in"`
	Out           int       `json:"out"`
}

func checkCommon(version int, ts, now time.Time) error {
	if version > SchemaVersion {
		return fmt.Errorf("schema_version %d is newer than supported (%d)", version, SchemaVersion)
	}
	if ts.IsZero() {
		return errors.New("ts missing")
	}
	if ts.After(now.Add(maxFuture)) {
		return fmt.Errorf("ts %s is in the future", ts.Format(time.RFC3339))
	}
	return nil
}

// Validate checks a reading and normalises it (timestamp truncated to the minute, like the seeded history).
func (m *DensityMsg) Validate(now time.Time) error {
	if m.SensorID == "" {
		return errors.New("sensor_id missing")
	}
	if err := checkCommon(m.SchemaVersion, m.TS, now); err != nil {
		return err
	}
	if m.Density == nil && m.Battery == nil {
		return errors.New("neither density nor battery given")
	}
	if m.Density != nil && (*m.Density < 0 || *m.Density > 10000) {
		return fmt.Errorf("density %v out of range", *m.Density)
	}
	if m.Battery != nil && (*m.Battery < 0 || *m.Battery > 100) {
		return fmt.Errorf("battery %v out of range", *m.Battery)
	}
	m.TS = m.TS.Truncate(time.Minute)
	return nil
}

// Validate checks a door count and normalises it.
func (m *DoorMsg) Validate(now time.Time) error {
	if m.StoreID == "" {
		return errors.New("store_id missing")
	}
	if err := checkCommon(m.SchemaVersion, m.TS, now); err != nil {
		return err
	}
	if m.In < 0 || m.Out < 0 || m.In > 10000 || m.Out > 10000 {
		return fmt.Errorf("in/out %d/%d out of range", m.In, m.Out)
	}
	if m.Door == "" {
		m.Door = "main"
	}
	m.TS = m.TS.Truncate(time.Minute)
	return nil
}
