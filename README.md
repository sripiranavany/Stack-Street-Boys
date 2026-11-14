# City Pulse – Future City Hackathon

A dashboard that turns people-density sensor data from Heilbronn into decisions for three audiences: **store managers** (staffing), **waste planners** (collection timing) and **citizens** (when and where it is calm).

It shows where the city is busy now, what is typical for a weekday, what is coming in the next 72 hours, and why spikes happen (weather, holidays, events). For store managers it also forecasts how many people will be inside each store, suggests cashier numbers, plans bakery batches and lets you try "what if" scenarios.

## Architecture

```
 Browser ──► frontend (Vue 3 + nginx) ──/api──► backend (Go) ──► TimescaleDB
                                                  │  ▲  ▲
                    sensors / door counters ─Kafka┘  │  └── forecaster (Python, Ebb model)
                                       worker: replay clock, forecast runs, alerts
```

| Part | Folder | What it is |
|---|---|---|
| Frontend | [frontend/](frontend/README.md) | Vue 3 + Vite + Pinia, ECharts, Leaflet, Tailwind. Map, sensor detail, weekly patterns, compare, forecast, role views |
| Backend | [backend/](backend/README.md) | Go REST API over the database (sensors, time series, heatmap, patterns, spikes, forecast, context, export), the store dashboards (`/api/v1`), the Kafka consumer and the worker (replay/live clock, forecast runs, alerts) |
| Forecaster | [forecaster/](forecaster/) | Python (FastAPI) service with the Ebb model: trains on the data, forecasts street density and store customers for 7 days with p10/p50/p90 bands and reasons (weather, holidays, events), learns event effects, serves what-if scenarios |
| Database | [db/](db/README.md) | TimescaleDB schema, data notes and load files |
| Kubernetes | [k8s/](k8s/) | Manifests for the database, backend, frontend, forecaster, Redpanda and shared config (Kustomize) |
| Scripts | [scripts/](scripts/) | Fetch weather / holiday context, generate the synthetic store door-counter entries |
| Docs | [docs/](docs/) | Hackathon presentation slides |
| Analysis | [analysis.ipynb](analysis.ipynb) | Data exploration that generated the schema and load files |

Each folder's README has the details; this page is the overview.

Sensor data arrives through Kafka (Redpanda in this setup): sensor gateways publish to `sensors.density` and `stores.door`, and the Go backend consumes and stores them. Details and a local simulator: [backend/README.md](backend/README.md#sensor-ingestion-kafka).

## Data in brief

- 12 sensors (9 currently active), people-density and battery readings, 2025-10-16 to 2026-11-07, about 192,000 rows.
- Timestamps are UTC; weekday and hour grouping uses Europe/Berlin.
- A missing reading (sensor offline) is stored as no row, never as `0`.
- The app has two clock modes: **replay** (the default seed: a recorded week plays back from `sim_now`) and **live** (`sim_now` is the wall-clock time, data comes from Kafka). Switch with `POST /api/v1/admin/clock` and `{"action":"live"}` or `{"action":"replay"}`.
- Stores are open Monday to Saturday, 07:00–22:00, and closed on public holidays (set per store in `ebb.stores.config`).

See [db/README.md](db/README.md) for the full data notes.

## Run it locally

Prerequisites: Docker, or a PostgreSQL/TimescaleDB with the schema loaded plus Go, Python and Node 22.18+ or 24.12+.

**With Docker Compose** (database, backend, forecaster, Redpanda, frontend):

```sh
docker compose up -d --build
docker compose run --rm forecaster python seed.py   # once, after the backend has created the ebb schema
docker compose restart forecaster                   # trains on the seeded data
# frontend: http://localhost:8088   backend: http://localhost:8081
docker compose --profile sim up -d --build sensorsim   # optional: fake live sensors
```

**Without Docker:**

```sh
# 1. Database: create tables, then load the CSVs (details in db/README.md)
psql ... -f schema.sql
psql ... -f db/schema_extra.sql      # weather, holidays, events, forecasts

# 2. Backend on :8080
cd backend
export POSTGRES_PASSWORD=...
go run .

# 3. Forecaster on :8000 (needs the same DB settings)
cd ../forecaster
pip install -r requirements.txt
python seed.py && uvicorn app:app --port 8000

# 4. Frontend on :5173 (proxies /api to :8080)
cd ../frontend
npm install
npm run dev
```

## Deploy

Images are built per component (`backend/Dockerfile`, `frontend/Dockerfile`, `forecaster/Dockerfile`) and deployed with Kustomize into the `fch26-stack-street-boys` namespace:

```sh
kubectl apply -k k8s --kubeconfig ~/.kube/stackit.yaml
```

The frontend Service is a LoadBalancer exposed at `fch26-stack-street-boys.fch26.stackit.run`. Its nginx forwards `/api/` to the backend Service, which reads database settings from the `app-config` ConfigMap and the `db-credentials` Secret. The backend talks to the `forecaster` Service for forecast runs and what-if scenarios. Remember to bump the image tags in `k8s/*.yaml` after pushing new images.

## Status

| Area | State |
|---|---|
| Sensors, time series, heatmap, patterns, spikes, export | Working |
| Weather, holidays, events | Loaded by `forecaster/seed.py` and `scripts/fetch_context.py`; managers can add or remove events, and effects are learned from past ones |
| Forecasts | Working: the forecaster writes a run every 6 h of (replay or live) time; forecast views show a 72 h band with reasons |
| Store dashboards | Working: people inside (live door counter or estimate), cashier suggestions, bakery plan, Calm Hours offers, alerts, typical week |
| What if… | Working: change the weather or add an event and compare against the normal forecast |
| Live mode | Working with a Kafka feed or the `sensorsim` simulator; store door-counter history is synthetic |
| Mobile layout | Responsive header, store cards and charts |

## Notes

- `credentials.md` holds cluster and database access details. Do not share it or commit secrets.
