# City Pulse – Forecaster (Ebb model)

Python (FastAPI) service that forecasts how busy the streets will be and how many shoppers will enter each store. The Go backend calls it; it reads and writes the same PostgreSQL/TimescaleDB database (schema `ebb`, with a mirror into `public.forecasts` for the City Pulse forecast views).

| File | What it does |
|---|---|
| [app.py](app.py) | HTTP API, training/refresh jobs, evaluation, publishing to City Pulse |
| [ebbmodel.py](ebbmodel.py) | Features, model training, prediction, explanations ("why"), store model, evaluation |
| [events.py](events.py) | Learns what past events did, projects recurring events, applies event effects |
| [seed.py](seed.py) | One-off script that fills the `ebb` schema from `public.readings` and the CSVs in [context/](context/) |
| [test_events.py](test_events.py) | Unit tests for event-name matching |

## How a forecast is made

```
 readings (street density, hourly mean) ─┐
 weather (actual + forecast by lead day) ┤                       ┌─► p10 / p50 / p90 per sensor, 168 h ─► forecasts
 holidays (public + school)              ├─► features ─► GBM ──► × event multiplier ─► + reasons (why)    │
 same hour last week, last week's mean   ┤                       └─► store model ─► shoppers per open hour ─► store_forecasts
 events (explicit, learned, projected)   ┘
```

### 1. Street forecast (one model for all active sensors)

- **Target:** the hourly mean people-density of a sensor, modelled as `log1p(value)`.
- **Algorithm:** scikit-learn `HistGradientBoostingRegressor`, three models per variant: a median model and two quantile models (10 % and 90 %). The quantile outputs are sorted so `p10 ≤ p50 ≤ p90`.
- **Features:** sensor id (categorical), hour of day, weekday, temperature, rain, cloud, sunshine, wind, public holiday, school holiday, plus two lag features: the value at the same hour a week earlier (`lag7`) and that day's mean a week earlier (`dmean7`).
- **Two variants:** the `lag` model uses the lag features. When there is no data from a week earlier (for example a sensor that was offline), the `nolag` model predicts from calendar and weather only.
- **Training data:** only real readings (`NOT is_estimated`) from `2026-06-12` up to the cutoff. The outage and the Lichterfest days (2026-06-17 to 2026-06-21) are excluded, and so are hours covered by past events, because event effects are applied separately (see below).
- **Weather at forecast time:** the weather forecast available at `as_of` is used, picked by lead time (1–7 days). If there is none, it falls back to actual weather, then to the monthly/hourly climate average.

### 2. Events

[events.py](events.py) turns events into multipliers on the street forecast. For each future event hour, in this order:

1. an **explicit profile** on the event (for example the hand-labelled 2026 Lichterfest, built in `seed.py` from measured data);
2. the **learned profile** of the event's series;
3. the average learned effect of past events of the same **category** (for example festivals);
4. the **size default**: S ×1.3, M ×2, L ×4.

How learning works:

- For each past event it compares measured street density and store door entries with the usual level for that weekday and hour (median of the 8 weeks around it, event days left out).
- Occurrences of the same event in different years form a **series**. Names are normalised (year removed, umlauts folded) and matched by their words, so "Heilbronner Lichterfest 2026" and "Lichterfest" match, but "Heilbronner Weindorf" does not.
- Multipliers are pulled towards 1 while there are few measured days, so one noisy evening doesn't dominate. They are clipped to 0.3–8.
- **Recurring events** get their next occurrence added automatically, 52 weeks later on the same weekday (`source: projected`), up to 400 days ahead.
- Each forecast hour records which rule was used (`basis`: profile, learned, category or size) and what it was based on.

### 3. Store customers

A second model predicts **door-counter entries per open hour** for each store:

- **Features:** store, hour, weekday, weather, holiday, school holiday, "eve of a public holiday" (stock-up day), the forecast street density next to the store, and last week's entries at that hour.
- Same algorithm and the same p10 / p50 / p90 quantile approach.
- Only open hours are forecast: the store's `open_days`, `open_from`/`open_to`, and no public holidays.
- For event hours with a learned shopper effect, the model predicts from the street density **without** the event, then applies the shopper multiplier measured in past events. That's how "foot traffic +40 %, shoppers +7 %" at the Lichterfest comes out right: crowds outside don't always shop.
- It trains only if there are at least 500 counted hours. If it isn't trained, or fails, the street forecast still works and the backend falls back to its capture-rate formula.
- The door-counter history for the seeded stores is **synthetic** (`scripts/make_store_entries.py`).

### 4. Explanations ("why")

Every forecast hour carries `reasons`, computed with the same model by changing one thing at a time:

- **weather effect:** forecast vs the same hour with typical, dry weather;
- **holiday / school-holiday effect:** forecast vs the same hour with the flag turned off;
- **event:** name, multiplier and its basis;
- plus the weather description (temperature, rain, cloud).

Effects are shown as a ratio, for example `+0.15` means 15 % more people because of that factor. The dashboards use these to say why an hour looks the way it does.

## When it runs

1. **On start-up** (background thread, retries every 30 s until the database is seeded): refreshes events, trains the production model, then publishes a 7-day forecast and runs an evaluation. `/readyz` stays unavailable until the model is trained.
2. **Forecast runs:** the Go worker calls `POST /predict` about every 6 hours of the (replay or live) clock, or immediately after a new event is added. Each run is stored in `forecast_runs` with its street forecasts and store forecasts.
3. **Latest forecast for City Pulse:** a model trained on all data produces a 7-day forecast after the newest reading and writes it to `public.forecasts`. This is separate from the replay runs, which only use data up to the replay clock.
4. **Retraining:** `POST /train` (used by the live-mode CronJob). In live mode the cutoff is "now"; in replay mode it is `train_cutoff` from `app_state`.

## API

| Endpoint | Body | What it does |
|---|---|---|
| `GET /healthz`, `GET /readyz` | – | Liveness; readiness is 503 until the model has trained |
| `POST /predict` | `{as_of, hours=168}` | Refreshes events, forecasts all sensors and stores, stores the run, returns `{run_id, rows, store_rows, model}` |
| `POST /whatif` | `{sensor_id, as_of, scenario, event_size?, event_name?, event_category?, event_start?, event_end?, hours}` | Baseline vs scenario for one sensor and the stores next to it. **Not stored.** Scenarios: `none`, `sunny`, `rain`, `cold`, `hot`. Giving an event name reuses what was learned from past occurrences |
| `POST /train` | `{cutoff?}` | Retrains the production model |
| `GET /model` | – | Model version, cutoff, features, algorithm, store-model info, last evaluation |

## Evaluation

`evaluate()` trains on data before `2026-10-01` and tests on 2026-10-01 to 2026-11-07 using actual weather. It reports, for daytime hours 08:00–22:00:

- **WAPE** (weighted absolute percentage error) hourly, per day and per 3-hour block, against two baselines: the **typical week** (median for that weekday and hour) and **last week**;
- bias, **coverage** of the p10–p90 band (should be about 80 %), and per-sensor WAPE;
- for stores, the model against the fixed capture-rate formula and the typical week.

Results are stored in `model_metrics` and returned by `GET /model`. Run it after changes to compare against the baselines.

## Run it locally

```sh
pip install -r requirements.txt
export POSTGRES_PASSWORD=...        # same settings as the Go service, or set DATABASE_URL
python seed.py                      # once, after the Go service has created the ebb schema
uvicorn app:app --port 8000
python -m pytest test_events.py   # event-matching tests (no database needed)
```

With Docker Compose: `docker compose run --rm forecaster python seed.py`, then `docker compose restart forecaster`.

Configuration: `DATABASE_URL`, or `DB_HOST`, `DB_PORT`, `POSTGRES_DB`, `POSTGRES_USER`, `POSTGRES_PASSWORD` (in Kubernetes from ConfigMap `app-config` and Secret `db-credentials`). The backend finds this service through `FORECASTER_URL`.

> `seed.py` **truncates** the `ebb` tables it fills (readings, sensors, weather, holidays, stores, events, app state, forecast runs). Don't run it against data you want to keep. In Kubernetes it currently runs as an init container of the forecaster pod (see [../k8s/ebb.yaml](../k8s/ebb.yaml)), which is why that deployment uses one replica until it is removed.

## Known limits

- One model for all sensors, trained on about five months of data (from 2026-06-12), so seasonal patterns beyond that are not learned. The climate fallback is built from the same period.
- Sensors count devices (Bluetooth-visible phones), so forecasts are relative crowd levels, not exact people counts.
- Store door-counter data is synthetic until real counters are connected.
- Forecast quality depends on the weather forecast supplied in `context/`; with a bad weather forecast the weather effect will be off too.
