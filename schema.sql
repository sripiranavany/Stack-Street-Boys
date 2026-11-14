
-- PostgreSQL schema (generated from analysis.ipynb)
CREATE EXTENSION IF NOT EXISTS timescaledb;

CREATE TABLE sensors (
    sensor_id   text PRIMARY KEY,                      -- e.g. '863f75b0' (NOT unique per name!)
    name        text NOT NULL,
    longitude   double precision NOT NULL,
    latitude    double precision NOT NULL,
    first_seen  timestamptz,
    last_seen   timestamptz,
    is_test     boolean NOT NULL DEFAULT false         -- maker-fair, niklas-01, d3304b39 ... (few rows)
);

CREATE TYPE metric_type AS ENUM ('densityData','batteryData');

CREATE TABLE readings (
    time          timestamptz      NOT NULL,           -- UTC, original sub-second stamp
    sensor_id     text             NOT NULL REFERENCES sensors(sensor_id),
    type          metric_type      NOT NULL,
    value         double precision,                    -- NULL = unknown; never 0 as filler. 0 = real zero.
    slot_15m      timestamptz GENERATED ALWAYS AS (date_bin('15 minutes', time, TIMESTAMPTZ '2000-01-01 00:00:00+00')) STORED,
    quality_flag  smallint NOT NULL DEFAULT 0,         -- bit flags: 1=negative, 2=spike, 4=stuck, 8=out_of_range
    PRIMARY KEY (sensor_id, type, time),
    CONSTRAINT battery_range CHECK (type <> 'batteryData' OR value IS NULL OR value BETWEEN 0 AND 100)
);
CREATE INDEX readings_time_idx  ON readings (time);
CREATE INDEX readings_slot_idx  ON readings (type, slot_15m);
SELECT create_hypertable('readings','time', chunk_time_interval => interval '30 days');

-- Regular 15-minute grid: a missing slot appears as NULL (sensor offline), a real zero stays 0.
CREATE VIEW readings_15m AS
SELECT s.sensor_id, s.name, g.slot, m.type,
       avg(r.value) FILTER (WHERE r.quality_flag = 0) AS value,   -- NULL when no (clean) reading
       count(r.value)                                  AS n_readings
FROM sensors s
CROSS JOIN LATERAL generate_series(date_bin('15 minutes', s.first_seen, TIMESTAMPTZ '2000-01-01'),
                                   date_bin('15 minutes', s.last_seen,  TIMESTAMPTZ '2000-01-01'),
                                   interval '15 minutes') AS g(slot)
CROSS JOIN (VALUES ('densityData'::metric_type),('batteryData')) AS m(type)
LEFT JOIN readings r ON r.sensor_id = s.sensor_id AND r.type = m.type AND r.slot_15m = g.slot
GROUP BY s.sensor_id, s.name, g.slot, m.type;

-- Gap report: consecutive readings > 20 min apart
CREATE VIEW sensor_gaps AS
SELECT sensor_id, prev_time AS gap_start, time AS gap_end, time - prev_time AS gap_length
FROM (SELECT sensor_id, time, lag(time) OVER (PARTITION BY sensor_id ORDER BY time) AS prev_time
      FROM readings WHERE type='densityData') x
WHERE time - prev_time > interval '20 minutes';
