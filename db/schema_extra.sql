-- Tables for the endpoints /api/forecast, /api/context and the "likely reasons" of /api/events/spikes.
-- They are filled by the Python jobs (ETL for weather, holidays, events; forecast worker).
-- Run once:  psql -U app -d energy -v ON_ERROR_STOP=1 < db/schema_extra.sql

CREATE TABLE IF NOT EXISTS weather_hourly (
    time              timestamptz PRIMARY KEY,   -- start of the hour, UTC
    temperature_c     double precision,
    precipitation_mm  double precision,
    wind_speed_ms     double precision
);

CREATE TABLE IF NOT EXISTS holidays (
    date    date NOT NULL,
    name    text NOT NULL,
    region  text,                                 -- e.g. 'BW', NULL = nationwide
    PRIMARY KEY (date, name)
);

CREATE TABLE IF NOT EXISTS events (
    id         bigserial PRIMARY KEY,
    name       text NOT NULL,
    starts_at  timestamptz NOT NULL,
    ends_at    timestamptz,
    venue      text,
    latitude   double precision,
    longitude  double precision,
    source     text,
    UNIQUE (name, starts_at)
);
CREATE INDEX IF NOT EXISTS events_starts_idx ON events (starts_at);

-- One row per sensor and future hour. The forecast worker upserts, so the newest forecast wins.
CREATE TABLE IF NOT EXISTS forecasts (
    sensor_id   text NOT NULL REFERENCES sensors(sensor_id),
    time        timestamptz NOT NULL,
    yhat        double precision NOT NULL,        -- predicted value
    yhat_lower  double precision,                 -- lower edge of the band
    yhat_upper  double precision,                 -- upper edge of the band
    model       text,
    created_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (sensor_id, time)
);