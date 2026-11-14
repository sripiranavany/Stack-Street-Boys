CREATE EXTENSION IF NOT EXISTS timescaledb;

CREATE TABLE IF NOT EXISTS sensors (
    sensor_id   text PRIMARY KEY,
    name        text NOT NULL,
    longitude   double precision NOT NULL,
    latitude    double precision NOT NULL,
    first_seen  timestamptz,
    last_seen   timestamptz,
    is_test     boolean NOT NULL DEFAULT false
);

DO $$ BEGIN
    CREATE TYPE metric_type AS ENUM ('densityData', 'batteryData');
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

CREATE TABLE IF NOT EXISTS readings (
    time          timestamptz      NOT NULL,
    sensor_id     text             NOT NULL REFERENCES sensors(sensor_id),
    type          metric_type      NOT NULL,
    value         double precision,
    slot_15m      timestamptz GENERATED ALWAYS AS
                    (date_bin('15 minutes', time, TIMESTAMPTZ '2000-01-01 00:00:00+00')) STORED,
    quality_flag  smallint NOT NULL DEFAULT 0,
    PRIMARY KEY (sensor_id, type, time),
    CONSTRAINT battery_range CHECK (type <> 'batteryData' OR value IS NULL OR value BETWEEN 0 AND 100)
);

SELECT create_hypertable('readings', 'time', if_not_exists => TRUE);

CREATE INDEX IF NOT EXISTS readings_slot_idx ON readings (type, slot_15m);

CREATE OR REPLACE VIEW readings_15m AS
SELECT s.sensor_id, s.name, g.slot, m.type,
       avg(r.value) FILTER (WHERE r.quality_flag = 0) AS value,
       count(r.value)                                  AS n_readings
FROM sensors s
CROSS JOIN LATERAL generate_series(date_bin('15 minutes', s.first_seen, TIMESTAMPTZ '2000-01-01'),
                                   date_bin('15 minutes', s.last_seen,  TIMESTAMPTZ '2000-01-01'),
                                   interval '15 minutes') AS g(slot)
CROSS JOIN (VALUES ('densityData'::metric_type), ('batteryData'::metric_type)) AS m(type)
LEFT JOIN readings r ON r.sensor_id = s.sensor_id AND r.type = m.type AND r.slot_15m = g.slot
GROUP BY s.sensor_id, s.name, g.slot, m.type;

CREATE OR REPLACE VIEW sensor_gaps AS
SELECT sensor_id, prev_time AS gap_start, time AS gap_end, time - prev_time AS gap_length
FROM (SELECT sensor_id, time,
             lag(time) OVER (PARTITION BY sensor_id ORDER BY time) AS prev_time
      FROM readings WHERE type = 'densityData') x
WHERE time - prev_time > interval '20 minutes';