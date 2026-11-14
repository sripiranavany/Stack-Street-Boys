package ingest

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/twmb/franz-go/pkg/kgo"

	"ssb-backend/internal/db"
	"ssb-backend/internal/domain"
)

// Ingester consumes the sensor topics as one member of a consumer group and writes to the ebb schema.
type Ingester struct {
	P       *pgxpool.Pool // pool on the ebb schema
	Brokers []string
	Group   string

	known     map[string]bool               // active street sensors
	stores    map[string]domain.StoreConfig // store id -> config (opening hours for store_entries.is_open)
	knownAt   time.Time
	lastFetch string // last fetch error, to avoid logging the same one every poll
}

// batch is what one poll produced, after validation.
type batch struct {
	density []DensityMsg
	door    []DoorMsg
}

// Run blocks until ctx is cancelled. Records are committed only after they are written to the database
// (at-least-once delivery; the writes are idempotent).
func (in *Ingester) Run(ctx context.Context) {
	cl, err := kgo.NewClient(
		kgo.SeedBrokers(in.Brokers...),
		kgo.ConsumerGroup(in.Group),
		kgo.ConsumeTopics(TopicDensity, TopicDoor),
		kgo.DisableAutoCommit(),
		kgo.AllowAutoTopicCreation(),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()),
	)
	if err != nil {
		log.Printf("ingest: kafka client: %v", err)
		return
	}
	defer cl.Close()
	log.Printf("ingest: consuming %s, %s from %v as group %q", TopicDensity, TopicDoor, in.Brokers, in.Group)
	for {
		fs := cl.PollFetches(ctx)
		if ctx.Err() != nil {
			return
		}
		fs.EachError(func(topic string, part int32, err error) {
			if msg := fmt.Sprintf("%s/%d: %v", topic, part, err); msg != in.lastFetch {
				in.lastFetch = msg
				log.Printf("ingest: fetch %s", msg)
			}
		})
		if fs.NumRecords() == 0 {
			continue
		}
		in.refresh(ctx)
		var b batch
		fs.EachRecord(func(r *kgo.Record) { in.decode(cl, r, &b) })
		for wait := time.Second; ; wait = min(wait*2, 30*time.Second) {
			err := in.write(ctx, b)
			if err == nil {
				break
			}
			log.Printf("ingest: write failed, retrying in %s: %v", wait, err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(wait):
			}
		}
		if err := cl.CommitUncommittedOffsets(ctx); err != nil && ctx.Err() == nil {
			log.Printf("ingest: commit: %v", err)
		}
	}
}

// refresh reloads the known sensors and stores every few minutes.
func (in *Ingester) refresh(ctx context.Context) {
	if time.Since(in.knownAt) < 5*time.Minute && in.known != nil {
		return
	}
	known := map[string]bool{}
	if rows, err := in.P.Query(ctx, `SELECT id FROM sensors WHERE active`); err == nil {
		for rows.Next() {
			var id string
			if rows.Scan(&id) == nil {
				known[id] = true
			}
		}
		rows.Close()
	}
	stores := map[string]domain.StoreConfig{}
	if rows, err := in.P.Query(ctx, `SELECT id, config FROM stores`); err == nil {
		for rows.Next() {
			var id string
			var raw []byte
			var c domain.StoreConfig
			if rows.Scan(&id, &raw) == nil && json.Unmarshal(raw, &c) == nil {
				stores[id] = c
			}
		}
		rows.Close()
	}
	in.known, in.stores, in.knownAt = known, stores, time.Now()
}

// decode parses and validates one record; bad ones go to the dead-letter topic.
func (in *Ingester) decode(cl *kgo.Client, r *kgo.Record, b *batch) {
	now := time.Now()
	switch r.Topic {
	case TopicDensity:
		var m DensityMsg
		if err := json.Unmarshal(r.Value, &m); err != nil {
			in.dlq(cl, r, "invalid JSON: "+err.Error())
		} else if err := m.Validate(now); err != nil {
			in.dlq(cl, r, err.Error())
		} else if !in.known[m.SensorID] {
			in.dlq(cl, r, "unknown or inactive sensor "+m.SensorID)
		} else {
			b.density = append(b.density, m)
		}
	case TopicDoor:
		var m DoorMsg
		if err := json.Unmarshal(r.Value, &m); err != nil {
			in.dlq(cl, r, "invalid JSON: "+err.Error())
		} else if err := m.Validate(now); err != nil {
			in.dlq(cl, r, err.Error())
		} else if _, ok := in.stores[m.StoreID]; !ok {
			in.dlq(cl, r, "unknown store "+m.StoreID)
		} else {
			b.door = append(b.door, m)
		}
	}
}

func (in *Ingester) dlq(cl *kgo.Client, r *kgo.Record, reason string) {
	cl.Produce(context.Background(), &kgo.Record{
		Topic: TopicDLQ, Key: r.Key, Value: r.Value,
		Headers: []kgo.RecordHeader{{Key: "reason", Value: []byte(reason)}, {Key: "source_topic", Value: []byte(r.Topic)},
			{Key: "source_offset", Value: []byte(fmt.Sprintf("%d/%d", r.Partition, r.Offset))}},
	}, func(_ *kgo.Record, err error) {
		if err != nil {
			log.Printf("ingest: dead-letter produce: %v", err)
		}
	})
	log.Printf("ingest: dead-lettered %s/%d@%d: %s", r.Topic, r.Partition, r.Offset, reason)
}

// storeHour identifies one hour of one store's door counts.
type storeHour struct {
	store string
	hour  time.Time
}

// touchedHours lists the distinct store hours a batch of door counts falls into, in order.
func touchedHours(door []DoorMsg) []storeHour {
	seen := map[storeHour]bool{}
	var out []storeHour
	for _, m := range door {
		k := storeHour{m.StoreID, m.TS.UTC().Truncate(time.Hour)} // Berlin offsets are whole hours
		if !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].store != out[j].store {
			return out[i].store < out[j].store
		}
		return out[i].hour.Before(out[j].hour)
	})
	return out
}

// write stores one batch in a single transaction.
func (in *Ingester) write(ctx context.Context, b batch) error {
	if len(b.density) == 0 && len(b.door) == 0 {
		return nil
	}
	tx, err := in.P.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	q := &pgx.Batch{}
	for _, m := range b.density {
		// A real density reading replaces a gap-fill estimate; a battery-only message leaves the density alone.
		q.Queue(`INSERT INTO readings (sensor_id, ts, density, battery, source) VALUES ($1, $2, $3, $4, 'kafka')
			ON CONFLICT (sensor_id, ts) DO UPDATE SET
			  density = coalesce(EXCLUDED.density, readings.density),
			  battery = coalesce(EXCLUDED.battery, readings.battery),
			  is_estimated = CASE WHEN EXCLUDED.density IS NOT NULL THEN false ELSE readings.is_estimated END,
			  source = CASE WHEN EXCLUDED.density IS NOT NULL THEN 'kafka' ELSE readings.source END`,
			m.SensorID, m.TS, m.Density, m.Battery)
	}
	for _, m := range b.door {
		q.Queue(`INSERT INTO store_door_counts (store_id, door, ts, entries, exits) VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (store_id, door, ts) DO UPDATE SET entries = EXCLUDED.entries, exits = EXCLUDED.exits`,
			m.StoreID, m.Door, m.TS, m.In, m.Out)
	}
	// hourly door-counter totals, recomputed from the minutes (so a redelivered minute never double counts)
	for _, k := range touchedHours(b.door) {
		local := k.hour.In(db.Berlin)
		open := in.stores[k.store].IsOpen(db.Weekday(local), local.Hour())
		q.Queue(`INSERT INTO store_entries (store_id, ts, entries, is_open)
			SELECT $1, $2, coalesce(sum(entries), 0), $3 OR coalesce(sum(entries), 0) > 0
			FROM store_door_counts WHERE store_id = $1 AND ts >= $2 AND ts < $2 + interval '1 hour'
			ON CONFLICT (store_id, ts) DO UPDATE SET entries = EXCLUDED.entries, is_open = EXCLUDED.is_open`,
			k.store, k.hour, open)
	}
	if err := tx.SendBatch(ctx, q).Close(); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
