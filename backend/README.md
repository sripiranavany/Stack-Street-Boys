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

Errors are JSON with an `error` message and an appropriate status (400 invalid input, 404 unknown sensor).

### Data notes

- Only readings with `quality_flag = 0` and non-null values are used.
- `weather_hourly`, `holidays`, `events` and `forecasts` are filled by separate ETL/forecast jobs. If a table is missing or empty, the endpoint still responds (empty lists plus a `missing_tables` list or `note`) instead of failing.

## Code map

| File | Contents |
|---|---|
| `main.go` | Config, DB pool, routing, graceful shutdown, request logging |
| `sensors.go` | Sensors, timeseries, heatmap, patterns |
| `insights.go` | Context, spikes, forecast |
| `export.go` | CSV/JSON export |
| `util.go` | Parsing, time range, error helpers, percentile, bucket filling (tests in `util_test.go`) |

## Deployment

```sh
docker build -t <registry>/ssb-backend:<tag> .
```

The multi-stage `Dockerfile` produces a static binary in a distroless image (port 8080). The Kubernetes `backend` Service exposes it on port 80, which is what the frontend's nginx proxies `/api/` to.
