// Command sensorsim pretends to be the street sensors and store door counters: it replays the recorded data,
// shifted forward by whole weeks so it lands on "now", and publishes it to Kafka in the same format a real
// sensor gateway would (see internal/ingest/messages.go).
//
//	street sensors: readings_load.csv (time,sensor_id,type,value), one message per reading
//	door counters:  store_entries.csv (hourly entries per store), spread over the minutes of each hour; people
//	                leave VISIT_MINUTES after they came in. One message per store door and minute.
//
// Environment: KAFKA_BROKERS (localhost:19092), READINGS_CSV, ENTRIES_CSV, SENSORS_CSV (optional, skips test
// sensors), BACKFILL (6h of past data sent at start), INTERVAL (30s), VISIT_MINUTES (15).
package main

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"io"
	"log"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"syscall"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"

	"ssb-backend/internal/ingest"
)

const week = 7 * 24 * time.Hour

type reading struct {
	ts      time.Time
	density *float64
	battery *float64
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func envDur(k string, def time.Duration) time.Duration {
	if d, err := time.ParseDuration(os.Getenv(k)); err == nil {
		return d
	}
	return def
}

func openCSV(path string) (*csv.Reader, func(), map[string]int) {
	f, err := os.Open(path)
	if err != nil {
		log.Fatalf("open %s: %v", path, err)
	}
	r := csv.NewReader(f)
	head, err := r.Read()
	if err != nil {
		log.Fatalf("read %s: %v", path, err)
	}
	col := map[string]int{}
	for i, h := range head {
		col[h] = i
	}
	return r, func() { f.Close() }, col
}

// testSensors: ids marked is_test in sensors_load.csv (optional).
func testSensors(path string) map[string]bool {
	out := map[string]bool{}
	if path == "" {
		return out
	}
	r, done, col := openCSV(path)
	defer done()
	for {
		rec, err := r.Read()
		if err != nil {
			break
		}
		if rec[col["is_test"]] == "True" {
			out[rec[col["sensor_id"]]] = true
		}
	}
	return out
}

// loadReadings merges density and battery rows with the same sensor and timestamp.
func loadReadings(path string, skip map[string]bool) (map[string][]reading, time.Time) {
	r, done, col := openCSV(path)
	defer done()
	byKey := map[string]map[int64]*reading{}
	var last time.Time
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			continue
		}
		id := rec[col["sensor_id"]]
		if skip[id] {
			continue
		}
		ts, err := time.Parse("2006-01-02 15:04:05-0700", rec[col["time"]])
		if err != nil {
			continue
		}
		v, err := strconv.ParseFloat(rec[col["value"]], 64)
		if err != nil {
			continue
		}
		if byKey[id] == nil {
			byKey[id] = map[int64]*reading{}
		}
		x := byKey[id][ts.UnixMicro()]
		if x == nil {
			x = &reading{ts: ts}
			byKey[id][ts.UnixMicro()] = x
		}
		switch rec[col["type"]] {
		case "densityData":
			x.density = &v
		case "batteryData":
			x.battery = &v
		}
		if ts.After(last) {
			last = ts
		}
	}
	out := map[string][]reading{}
	for id, m := range byKey {
		for _, x := range m {
			out[id] = append(out[id], *x)
		}
		sort.Slice(out[id], func(i, j int) bool { return out[id][i].ts.Before(out[id][j].ts) })
	}
	return out, last
}

// loadEntries: store -> UTC hour start -> door entries in that hour.
func loadEntries(path string) (map[string]map[int64]int, time.Time) {
	r, done, col := openCSV(path)
	defer done()
	out := map[string]map[int64]int{}
	var last time.Time
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			continue
		}
		ts, err := time.Parse(time.RFC3339, rec[col["ts"]])
		if err != nil {
			continue
		}
		n, _ := strconv.Atoi(rec[col["entries"]])
		id := rec[col["store_id"]]
		if out[id] == nil {
			out[id] = map[int64]int{}
		}
		out[id][ts.UTC().Unix()] = n
		if ts.After(last) {
			last = ts
		}
	}
	return out, last
}

// weeksBack is the smallest whole number of weeks to shift recorded data (ending at last) forward so it covers now.
func weeksBack(now, last time.Time) time.Duration {
	if !now.After(last) {
		return 0
	}
	n := int((now.Sub(last) + week - 1) / week)
	return time.Duration(n) * week
}

// minuteIn spreads an hour's entries evenly over its minutes (integers that add up to the hour's total).
func minuteIn(hourly map[int64]int, t time.Time) int {
	h := t.UTC().Truncate(time.Hour)
	e := hourly[h.Unix()]
	i := int(t.Sub(h) / time.Minute)
	return e*(i+1)/60 - e*i/60
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	brokers := env("KAFKA_BROKERS", "localhost:19092")
	backfill := envDur("BACKFILL", 6*time.Hour)
	interval := envDur("INTERVAL", 30*time.Second)
	visit, _ := strconv.Atoi(env("VISIT_MINUTES", "15"))
	if visit <= 0 {
		visit = 15
	}

	readings, lastR := loadReadings(env("READINGS_CSV", "readings_load.csv"), testSensors(os.Getenv("SENSORS_CSV")))
	entries, lastE := loadEntries(env("ENTRIES_CSV", "entries.csv"))
	last := lastR
	if lastE.Before(last) {
		last = lastE
	}
	start := time.Now()
	offset := weeksBack(start, last)
	log.Printf("sensorsim: %d street sensors, %d stores; recorded data ends %s, replaying it %d week(s) later",
		len(readings), len(entries), last.Format(time.RFC3339), int(offset/week))

	cl, err := kgo.NewClient(kgo.SeedBrokers(brokers), kgo.AllowAutoTopicCreation(), kgo.ProducerLinger(20*time.Millisecond))
	if err != nil {
		log.Fatal(err)
	}
	defer cl.Close()
	for err := cl.Ping(ctx); err != nil; err = cl.Ping(ctx) {
		log.Printf("sensorsim: waiting for kafka at %s: %v", brokers, err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(3 * time.Second):
		}
	}

	street := start.Add(-backfill)                     // readings published up to here (wall clock)
	door := start.Add(-backfill).Truncate(time.Minute) // next door-counter minute to publish
	for {
		now := time.Now()
		var recs []*kgo.Record
		for id, rs := range readings {
			from, to := street.Add(-offset), now.Add(-offset)
			i := sort.Search(len(rs), func(i int) bool { return rs[i].ts.After(from) })
			for ; i < len(rs) && !rs[i].ts.After(to); i++ {
				m := ingest.DensityMsg{SchemaVersion: ingest.SchemaVersion, SensorID: id, TS: rs[i].ts.Add(offset),
					Density: rs[i].density, Battery: rs[i].battery}
				b, _ := json.Marshal(m)
				recs = append(recs, &kgo.Record{Topic: ingest.TopicDensity, Key: []byte(id), Value: b})
			}
		}
		nStreet := len(recs)
		d := door
		for ; !d.Add(time.Minute).After(now); d = d.Add(time.Minute) {
			src := d.Add(-offset)
			for id, hourly := range entries {
				m := ingest.DoorMsg{SchemaVersion: ingest.SchemaVersion, StoreID: id, Door: "main", TS: d,
					In: minuteIn(hourly, src), Out: minuteIn(hourly, src.Add(-time.Duration(visit)*time.Minute))}
				b, _ := json.Marshal(m)
				recs = append(recs, &kgo.Record{Topic: ingest.TopicDoor, Key: []byte(id), Value: b})
			}
		}
		if len(recs) > 0 {
			if err := cl.ProduceSync(ctx, recs...).FirstErr(); err != nil {
				if ctx.Err() != nil {
					return
				}
				log.Printf("sensorsim: produce failed, will resend: %v", err)
			} else {
				log.Printf("sensorsim: published %d street readings, %d door counts (up to %s)", nStreet, len(recs)-nStreet,
					now.Format("15:04:05"))
				street, door = now, d // advance only after Kafka acknowledged
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}
