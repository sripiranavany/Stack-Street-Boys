# City Pulse – Backend

Go HTTP API (standard library `net/http` + `pgx`) that serves sensor data from TimescaleDB/PostgreSQL. Times are returned in UTC/RFC 3339; weekday and hour grouping uses `Europe/Berlin`.

## Run locally

Requires Go (see `go.mod`) and a database created from [`../schema.sql`](../schema.sql) and [`../db/schema_extra.sql`](../db/schema_extra.sql).

```sh
export POSTGRES_PASSWORD=...
go run .             # listens on :8080
go test ./...
```

### Configuration (environment variables)

| Variable | Default | Notes |
|---|---|---|
| `DB_HOST` | `localhost` | |
| `DB_PORT` | `5432` | |
| `POSTGRES_DB` | `energy` | |
| `POSTGRES_USER` | `app` | |
| `POSTGRES_PASSWORD` | – | In Kubernetes from Secret `db-credentials` |
| `PORT` | `8080` | |

On start the app retries the database for about a minute. In Kubernetes, config comes from ConfigMap `app-config` and Secret `db-credentials` (see [`../k8s/backend.yaml`](../k8s/backend.yaml)).

## API

All endpoints are `GET`. Time parameters accept `2026-11-01` or `2026-11-01T12:00:00Z`. When `to` is omitted it defaults to the newest reading in the database (the data is historical), and `from` defaults to `to` minus the endpoint's default span.

| Endpoint | Parameters | Returns |
|---|---|---|
| `/healthz`, `/readyz` | – | Liveness; readiness also pings the database |
| `/api/sensors` | `include_test` | Sensors with latest value and `busyness` (latest ÷ the sensor's own 95th percentile over the prior 14 days) |
| `/api/sensors/{id}/timeseries` | `from`, `to`, `agg=15m\|1h\|1d`, `type=density\|battery`, `normalize`, `fill` | Aggregated points; missing buckets are `null`, never `0`. Default span 7 days |
| `/api/heatmap` | `from`, `to`, `agg=hourly\|daily`, `include_test` | Per-time frames of all sensors with `intensity` (0–1, relative to each sensor's p95). Max 31 days hourly / 366 daily |
| `/api/patterns/{id}` | `from`, `to`, `type` | 7×24 weekday × hour average matrix (default 90 days) |
| `/api/events/spikes` | `from`, `to`, `sensor_id`, `z` (default 3), `limit` | Hours well above the usual weekday/hour level (z-score vs. previous 8 weeks), with likely reasons (holiday, event, weather) |
| `/api/forecast/{id}` | `horizon` (`72h`, `3d`; max 14 d) | Stored forecast after the newest reading, with lower/upper bounds |
| `/api/context` | `from`, `to` | Weather, holidays and events for the range |
| `/api/export` | `format=csv\|json`, `agg=raw\|15m\|1h\|1d`, `sensor_id`, `type`, `from`, `to`, `include_test` | Data download (max 200,000 rows) |

### Store manager dashboard (`/api/v1`)

| Endpoint | Parameters | Returns |
|---|---|---|
| `/api/v1/stores/overview` | – | One row per store: people inside now, outside foot traffic vs usual, today's peak, hourly occupancy, alerts |
| `/api/v1/stores/{id}/dashboard` | `day=today\|tomorrow\|YYYY-MM-DD` | Everything for one store: now, hourly occupancy + passers-by, peak, next hours with suggested cashiers, nearby street sensors, typical week, alerts |

People inside = door-counter entries per hour × the store's `visit_minutes` (default 15). Levels use the store's `capacity` (default 150): under 70 % comfortable, 70–89 % busy, 90 %+ near capacity. Both are optional fields of the store config (`PUT /api/v1/stores/{id}/config`). Code: `internal/api/dashboard.go`, `internal/domain/occupancy.go`.

Each hour also carries a `why` (weather, the forecaster's weather / holiday / school-holiday effect, event name and multiplier) and each day a `context` (public holiday, school holiday, events at the store's sensor). Past hours also get `measured_vs_usual` (measured foot traffic vs the usual weekday/hour level) and the effects from the forecast run made before that hour (`effects_as_of`). On public holidays the store is treated as closed, as in the forecaster. The dashboard response lists `upcoming_events` (next 7 days at the store's sensor); managers add or remove them through `POST /api/v1/events` / `DELETE /api/v1/events/{id}`, which triggers a re-forecast. Code: `internal/api/dashcontext.go`.

Errors are JSON with an `error` message and an appropriate status (400 invalid input, 404 unknown sensor).

### Data notes

- Only readings with `quality_flag = 0` and non-null values are used.
- `weather_hourly`, `holidays`, `events` and `forecasts` are filled by separate ETL/forecast jobs. If a table is missing or empty, the endpoint still responds (empty lists plus a `missing_tables` list or `note`) instead of failing.

## Sensor ingestion (Kafka)

Sensors don't talk to the API directly. A gateway (or the `sensorsim` simulator) publishes JSON messages to Kafka, and every backend replica consumes them as consumer group `ssb-ingest` (`internal/ingest`):

| Topic | Key | Message | Written to |
|---|---|---|---|
| `sensors.density` | sensor id | `{schema_version, sensor_id, ts, density?, battery?}` | `ebb.readings` (a real reading replaces a gap-fill estimate) |
| `stores.door` | store id | `{schema_version, store_id, door, ts, in, out}` per door and minute | `ebb.store_door_counts`, hourly totals recomputed into `ebb.store_entries` |
| `sensors.dlq` | – | messages that fail parsing or validation, with `reason` / `source_topic` / `source_offset` headers | – |

- Offsets are committed only after the database write, and every write is an upsert keyed by sensor/store, door and minute, so redelivery, replaying a topic from the start and several replicas are all safe.
- Validation: known active sensor / known store, timestamp present and at most 10 min in the future, density 0–10000, battery 0–100, counts 0–10000, `schema_version` not newer than supported.
- In live mode the store dashboard's "In store now" is the real headcount: door entries minus exits since midnight, when the counter reported in the last 10 minutes (`source: "live"`). Otherwise it falls back to the last counted hour, then the estimate.
- `KAFKA_BROKERS` (comma-separated) turns the consumer on; `KAFKA_GROUP` overrides the group name. Without it nothing changes.

Try it locally:

```sh
docker compose up -d --build redpanda backend frontend
docker compose --profile sim up -d --build sensorsim      # fake sensors: last week's recordings, replayed as "now"
curl -X POST localhost:8081/api/v1/admin/clock -H 'Content-Type: application/json' -d '{"action":"live"}'
# back to the recorded week:  -d '{"action":"replay"}'
```

`cmd/sensorsim` reads `readings_load.csv` and `forecaster/context/store_entries.csv`, shifts them forward by whole weeks so they land on today, sends the last 6 h at start (`BACKFILL`) and then new data every 30 s (`INTERVAL`). Door entries are spread evenly over each hour's minutes and people leave `VISIT_MINUTES` (15) later. The door-counter history is synthetic.

## Events: learned from history

The forecaster (`forecaster/events.py`) learns what past events did and applies it to future ones:

- **Learning.** After every forecast run it compares each past event's measured street density and door entries with the usual level for that weekday and hour (median of the 8 weeks around it, event days left out). Occurrences of the same event in different years form a series: "Heilbronner Lichterfest 2026", "… 2027" and "Lichterfest" all match, "Heilbronner Weindorf" does not (all words of the shorter name must appear in the longer one, and one of them must be distinctive). Per sensor × hour and store × hour multipliers are pulled towards 1 while there are few measured days, so a single noisy evening doesn't dominate. Results: `event_profiles` (`learned:<series>`), `event_store_profiles`, `event_learned`, and `GET /api/v1/events/learned`.
- **Recurring events** get their next occurrence added automatically, 52 weeks later on the same weekday (`source: projected`), so next year's Lichterfest is in the forecast as soon as it is within the 7-day horizon.
- **Forecasting an event**, in order: an explicit profile on the event, the learned profile of its series, the average of learned events of the same kind (festival, market, …), the size default (S ×1.3, M ×2, L ×4). Each forecast hour's `reasons.event.basis` / `based_on` says which, and the dashboard shows it.
- **Shoppers.** Where a series has a learned shopper effect, the store forecast for event hours uses the street level without the event and applies that effect, instead of assuming bigger crowds outside mean more shoppers (Lichterfest: foot traffic at Neckarufer +40 %, shoppers +7 %).
- The base model is trained on normal hours only: past event hours are left out and added back as multipliers.

## Code map

| File | Contents |
|---|---|
| `main.go` | Config, DB pool, routing, graceful shutdown, request logging |
| `sensors.go` | Sensors, timeseries, heatmap, patterns |
| `insights.go` | Context, spikes, forecast |
| `export.go` | CSV/JSON export |
| `internal/ingest/` | Kafka consumer: message types, validation, dead-letter topic, idempotent writes |
| `cmd/sensorsim/` | Sensor simulator that publishes recorded data to Kafka |
| `util.go` | Parsing, time range, error helpers, percentile, bucket filling (tests in `util_test.go`) |

## Deployment

```sh
docker build -t <registry>/ssb-backend:<tag> .
```

The multi-stage `Dockerfile` produces a static binary in a distroless image (port 8080). The Kubernetes `backend` Service exposes it on port 80, which is what the frontend's nginx proxies `/api/` to.
