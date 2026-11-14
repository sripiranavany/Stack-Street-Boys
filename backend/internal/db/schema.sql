-- Ebb decision-layer schema. Lives in its own PostgreSQL schema "ebb" next to the City Pulse tables in "public". Idempotent.
CREATE TABLE IF NOT EXISTS sensors (
  id          text PRIMARY KEY,
  name        text NOT NULL,
  label       text NOT NULL,
  lat         double precision NOT NULL,
  lon         double precision NOT NULL,
  active      boolean NOT NULL DEFAULT true,
  neighbor_id text
);

-- staging table for the raw CSV (\copy target)
CREATE TABLE IF NOT EXISTS raw_readings (
  time timestamptz, sensor_id text, name text, longitude double precision,
  latitude double precision, type text, value double precision
);

CREATE TABLE IF NOT EXISTS readings (
  sensor_id    text NOT NULL,
  ts           timestamptz NOT NULL,
  density      double precision,
  battery      double precision,
  source       text NOT NULL DEFAULT 'csv',
  is_estimated boolean NOT NULL DEFAULT false,
  PRIMARY KEY (sensor_id, ts)
);
CREATE INDEX IF NOT EXISTS readings_ts ON readings (ts);

CREATE TABLE IF NOT EXISTS weather (
  ts        timestamptz NOT NULL,
  kind      text NOT NULL,            -- actual | forecast
  lead_days int  NOT NULL DEFAULT 0,  -- forecast issued N days before ts
  temp double precision, precip double precision, cloud double precision,
  sun double precision, wind double precision,
  PRIMARY KEY (ts, kind, lead_days)
);

CREATE TABLE IF NOT EXISTS holidays (
  start_date date NOT NULL, end_date date NOT NULL, kind text NOT NULL, name text NOT NULL
);

CREATE TABLE IF NOT EXISTS events (
  id         serial PRIMARY KEY,
  name       text NOT NULL,
  category   text NOT NULL DEFAULT 'event',
  start_ts   timestamptz NOT NULL,
  end_ts     timestamptz NOT NULL,
  sensor_ids text[] NOT NULL,
  size       text,                    -- S | M | L (used when no profile)
  profile    text,                    -- event_profiles.profile
  recurring  boolean NOT NULL DEFAULT false,
  source     text NOT NULL DEFAULT 'manual'
);

CREATE TABLE IF NOT EXISTS event_profiles (
  profile text NOT NULL, sensor_id text NOT NULL, hour int NOT NULL, multiplier double precision NOT NULL,
  PRIMARY KEY (profile, sensor_id, hour)
);

CREATE TABLE IF NOT EXISTS forecast_runs (
  id            serial PRIMARY KEY,
  as_of         timestamptz NOT NULL,
  model_version text NOT NULL,
  created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS forecast_runs_as_of ON forecast_runs (as_of);

CREATE TABLE IF NOT EXISTS forecasts (
  run_id    int NOT NULL REFERENCES forecast_runs(id) ON DELETE CASCADE,
  sensor_id text NOT NULL,
  ts        timestamptz NOT NULL,
  p50 double precision, p10 double precision, p90 double precision,
  reasons   jsonb,
  PRIMARY KEY (run_id, sensor_id, ts)
);

CREATE TABLE IF NOT EXISTS stores (
  id text PRIMARY KEY, name text NOT NULL, sensor_id text NOT NULL,
  lat double precision, lon double precision, config jsonb NOT NULL
);

CREATE TABLE IF NOT EXISTS alerts (
  id         serial PRIMARY KEY,
  kind       text NOT NULL,           -- prezero | battery | offline | anomaly
  sensor_id  text,
  ts         timestamptz NOT NULL,
  title      text NOT NULL,
  detail     text,
  payload    jsonb,
  status     text NOT NULL DEFAULT 'open',  -- open | ack | labelled
  dedupe_key text UNIQUE
);

CREATE TABLE IF NOT EXISTS campaigns (
  store_id text PRIMARY KEY, config jsonb NOT NULL, active boolean NOT NULL DEFAULT false
);

CREATE TABLE IF NOT EXISTS offers (
  id           serial PRIMARY KEY,
  store_id     text NOT NULL,
  customer_ref text NOT NULL,
  usual_slot   timestamptz NOT NULL,
  offer_slot   timestamptz NOT NULL,
  discount     int NOT NULL,
  coupon_code  text UNIQUE NOT NULL,
  status       text NOT NULL DEFAULT 'issued',  -- issued | redeemed
  created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS app_state (key text PRIMARY KEY, value text NOT NULL);

CREATE TABLE IF NOT EXISTS model_metrics (
  model_version text NOT NULL, metric text NOT NULL, scope text NOT NULL, value double precision,
  PRIMARY KEY (model_version, metric, scope)
);

-- Door-counter history per store (hourly). The demo data is synthetic: scripts/make_store_entries.py.
CREATE TABLE IF NOT EXISTS store_entries (
  store_id text NOT NULL,
  ts       timestamptz NOT NULL,
  entries  int NOT NULL,
  is_open  boolean NOT NULL,
  PRIMARY KEY (store_id, ts)
);

-- Store customer forecast (open hours only), written with every forecast run.
CREATE TABLE IF NOT EXISTS store_forecasts (
  run_id   int NOT NULL REFERENCES forecast_runs(id) ON DELETE CASCADE,
  store_id text NOT NULL,
  ts       timestamptz NOT NULL,
  p50 double precision, p10 double precision, p90 double precision,
  PRIMARY KEY (run_id, store_id, ts)
);

-- Live door-counter counts per store door and minute, written by the Kafka consumer (internal/ingest).
-- Hourly store_entries are recomputed from these; people inside now = entries - exits since midnight.
CREATE TABLE IF NOT EXISTS store_door_counts (
  store_id text NOT NULL,
  door     text NOT NULL,
  ts       timestamptz NOT NULL,   -- start of the minute
  entries  int NOT NULL,
  exits    int NOT NULL,
  PRIMARY KEY (store_id, door, ts)
);
CREATE INDEX IF NOT EXISTS store_door_counts_store_ts ON store_door_counts (store_id, ts);

-- What the forecaster learned from past events (forecaster/events.py). One row per event series: occurrences of
-- the same event in different years share a series key ("Heilbronner Lichterfest 2026" -> heilbronner-lichterfest).
-- Street multipliers per sensor x hour go to event_profiles (profile 'learned:<series>'), shopper multipliers here.
CREATE TABLE IF NOT EXISTS event_store_profiles (
  profile    text NOT NULL,
  store_id   text NOT NULL,
  hour       int  NOT NULL,
  multiplier double precision NOT NULL,
  PRIMARY KEY (profile, store_id, hour)
);
CREATE TABLE IF NOT EXISTS event_learned (
  series       text PRIMARY KEY,
  name         text NOT NULL,          -- newest past occurrence
  category     text NOT NULL,
  occurrences  int  NOT NULL,
  last_start   timestamptz NOT NULL,
  sensor_ids   text[] NOT NULL,
  street_peak  double precision,       -- highest sensor x hour multiplier
  street_mean  double precision,       -- mean multiplier over the event's sensor hours
  store_mean   double precision,       -- mean shopper multiplier over the event's open store hours (NULL: no store nearby)
  detail       jsonb NOT NULL,         -- {"sensors": {id: {hour: mult}}, "stores": {id: {hour: mult}}}
  learned_at   timestamptz NOT NULL DEFAULT now()
);
-- next planned occurrence of each learned series (filled by the forecaster after projecting recurring events)
ALTER TABLE event_learned ADD COLUMN IF NOT EXISTS next_name text;
ALTER TABLE event_learned ADD COLUMN IF NOT EXISTS next_start timestamptz;
