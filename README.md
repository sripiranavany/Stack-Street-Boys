# City Pulse – Future City Hackathon

A dashboard that turns people-density sensor data from Heilbronn into decisions for three audiences: **store managers** (staffing), **waste planners** (collection timing) and **citizens** (when and where it is calm).

It shows where the city is busy now, what is typical for a weekday, what is coming in the next 72 hours, and why spikes happen (weather, holidays, events).

## Architecture

```
 Browser ──► frontend (Vue 3 + nginx) ──/api──► backend (Go) ──► TimescaleDB
                                                   ▲
                      ETL / forecast jobs (weather, holidays, events, forecasts) ──┘
```

| Part | Folder | What it is |
|---|---|---|
| Frontend | [frontend/](frontend/README.md) | Vue 3 + Vite + Pinia, ECharts, Leaflet, Tailwind. Map, sensor detail, weekly patterns, compare, forecast, role views |
| Backend | [backend/](backend/README.md) | Go REST API over the database (sensors, time series, heatmap, patterns, spikes, forecast, context, export) |
| Database | [db/](db/README.md) | TimescaleDB schema, data notes and load files |
| Kubernetes | [k8s/](k8s/) | Manifests for the database, backend, frontend and shared config (Kustomize) |
| Analysis | [analysis.ipynb](analysis.ipynb) | Data exploration that generated the schema and load files |

Each folder's README has the details; this page is the overview.

Sensor data arrives through Kafka (Redpanda in this setup): sensor gateways publish to `sensors.density` and `stores.door`, and the Go backend consumes and stores them. Details and a local simulator: [backend/README.md](backend/README.md#sensor-ingestion-kafka).

## Data in brief

- 12 sensors (9 currently active), people-density and battery readings, 2025-10-16 to 2026-11-07, about 192,000 rows.
- Timestamps are UTC; weekday and hour grouping uses Europe/Berlin.
- A missing reading (sensor offline) is stored as no row, never as `0`.
- The data is historical, so the app treats the **newest reading as "now"**.

See [db/README.md](db/README.md) for the full data notes.

## Run it locally

Prerequisites: a PostgreSQL/TimescaleDB with the schema loaded, Go, and Node 22.18+ or 24.12+.

```sh
# 1. Database: create tables, then load the CSVs (details in db/README.md)
psql ... -f schema.sql
psql ... -f db/schema_extra.sql      # weather, holidays, events, forecasts

# 2. Backend on :8080
cd backend
export POSTGRES_PASSWORD=...
go run .

# 3. Frontend on :5173 (proxies /api to :8080)
cd ../frontend
npm install
npm run dev
```

## Deploy

Images are built per component (`backend/Dockerfile`, `frontend/Dockerfile`) and deployed with Kustomize into the `fch26-stack-street-boys` namespace:

```sh
kubectl apply -k k8s --kubeconfig ~/.kube/stackit.yaml
```

The frontend Service is a LoadBalancer exposed at `fch26-stack-street-boys.fch26.stackit.run`. Its nginx forwards `/api/` to the backend Service, which reads database settings from the `app-config` ConfigMap and the `db-credentials` Secret. Remember to bump the image tags in `k8s/*.yaml` after pushing new images.

## Status

| Area | State |
|---|---|
| Sensors, time series, heatmap, patterns, spikes, export | Working |
| Weather, holidays, events | Tables and endpoints exist; data to be loaded by the ETL job |
| Forecasts | Endpoint exists; forecast worker not yet writing rows, so forecast views show an empty state |

## Notes

- `credentials.md` holds cluster and database access details. Do not share it or commit secrets.
