# City Pulse – Frontend

Vue 3 dashboard for the city's footfall sensors: live map, sensor detail, weekly patterns, comparison and 72-hour forecast, with role-based views (Store manager / Waste planner / Citizen).

## Stack

Vue 3 (`<script setup>`, TypeScript) · Vite · Pinia · Vue Router · Apache ECharts · Leaflet · Tailwind CSS v4

## Getting started

Requires Node `^22.18` or `>=24.12`.

```sh
npm install
npm run dev          # http://localhost:5173
```

The dev server proxies `/api` to `http://localhost:8080` (the Go backend). To use another backend:

```sh
VITE_API_PROXY=https://your-host npm run dev
```

| Script | Purpose |
|---|---|
| `npm run dev` | Dev server with hot reload |
| `npm run type-check` | `vue-tsc` type check |
| `npm run build` | Type check + production build into `dist/` |
| `npm run preview` | Serve the production build locally |

`VITE_API_BASE` (default `/api`) sets the API prefix at build time.

## Screens

| Route | Screen |
|---|---|
| `/` | **City map** – circles sized/coloured by busyness, time slider with play, Now / Typical / Forecast |
| `/sensors/:id?` | **Sensor detail** – time series with temperature, rain, holidays, events, and spike list |
| `/patterns/:id?` | **Pattern heatmap** – hour × weekday grid |
| `/compare` | **Compare** – two sensors or two periods side by side |
| `/forecast/:id?` | **Forecast** – 72 h with confidence band and recommendation card |

The role switcher in the header changes default map mode, rankings, wording and recommendations. The choice is stored in `localStorage`.

## Project layout

```
src/
  api/          typed fetch client, endpoint functions, response types
  stores/       Pinia stores: sensors (shared list, "now" anchor), role
  composables/  useAsyncData (abortable loading), useMapFrames, useSensorParam, ECharts setup
  lib/          pure helpers: time (Europe/Berlin), colours, roles, recommendations, spikes
  components/   layout/, common/, charts/, map/
  views/        one file per route (lazy-loaded)
  router/
```

## Notes

- The data is historical, so **"now" is the newest reading**, not the wall clock (see `stores/sensors.ts`). Sensors more than 14 days behind it are hidden.
- Weather, holidays, events and forecasts come from tables filled by separate jobs. Until they have data, the UI shows empty states.
- Map tiles come from OpenStreetMap's public server; use a dedicated tile provider for real traffic.
- Adding a role: extend `src/lib/roles.ts` and the text map in `src/lib/recommendation.ts`.

## Deployment

`Dockerfile` builds the app and serves `dist/` with nginx. `nginx.conf` forwards `/api/` to the `backend` service and falls back to `index.html` for client-side routes. Kubernetes manifest: [`../k8s/frontend.yaml`](../k8s/frontend.yaml).

```sh
docker build -t <registry>/ssb-frontend:<tag> .
```
