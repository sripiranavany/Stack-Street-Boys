# Database (TimescaleDB / PostgreSQL)

Sensor data for the Future City project: people-density and battery readings from 12 sensors in Heilbronn.

| File | Purpose |
|---|---|
| [../analysis.ipynb](../analysis.ipynb) | Exploration of the raw CSV; generates everything below |
| [../schema.sql](../schema.sql) | Tables, hypertable, views |
| [../sensors_load.csv](../sensors_load.csv) | 12 rows for `sensors` |
| [../readings_load.csv](../readings_load.csv) | 192,464 rows for `readings` |
| `../data-1794095549194.csv` | Raw export (long format, source of truth) |

## What the data looks like

- **Range:** 2025-10-16 to 2026-11-07, timestamps in UTC with microseconds. Local time is Europe/Berlin, so convert only for display or per-day grouping.
- **Sensors:** 12 `sensor_id`s but 11 names, because `density-01` is used by two sensors. Always key on `sensor_id`.
  Three sensors (`niklas-01`, `maker-fair`, `hn-bollwerksturm-01`) have fewer than 200 rows and are flagged `is_test`.
- **Metrics (`type`):** `densityData` (people count) and `batteryData` (percent, 0-100). They arrive as a pair a fraction of a second apart.
- **Interval:** nominally 15 minutes. Real spacing drifts to about 15.3 minutes, so timestamps are not clock-aligned.

## Missing data vs zero

A missing row (sensor offline) is **not** the same as `value = 0` (nobody around).

- Gaps are stored as **no row**. Nothing is ever filled with 0.
- `0` is a real reading (5.2% of density rows).
- `readings_15m` builds a regular 15-minute grid, so an offline slot shows `value = NULL, n_readings = 0` and a real zero shows `value = 0, n_readings >= 1`.
- Gaps are real and large: about 9,500 gaps over 20 minutes, around 40,000 missing slots. Coverage of the main sensors is 56-89%.
  Several sensors went offline together (for example 2026-06-17, about 5 days), so that is a system outage, not a clock-drift problem.

## Schema

- `sensors(sensor_id PK, name, longitude, latitude, first_seen, last_seen, is_test)`
- `readings(time, sensor_id, type, value, slot_15m, quality_flag)`
  - PK `(sensor_id, type, time)`, hypertable on `time` (30-day chunks).
  - `value` is nullable. `slot_15m` is generated with `date_bin('15 minutes', time, ...)`.
  - `quality_flag` is a bit field: `1` negative, `2` spike, `4` stuck, `8` out of range. It is `0` by default; raw values are never altered.
  - Battery must be within 0-100.
- `readings_15m` (view): sensor x slot x metric grid, LEFT JOINed to readings. It averages `quality_flag = 0` rows, so it is `NULL` when there is no clean reading.
- `sensor_gaps` (view): consecutive density readings more than 20 minutes apart.

## Load

Requires PostgreSQL 14+ (`date_bin`) with the `timescaledb` extension. Load `sensors` first because `readings` references it.

```bash
export KC=~/.kube/stackit.yaml NS=<namespace>

# copy files into the pod
for f in schema.sql sensors_load.csv readings_load.csv; do
  kubectl cp $f $NS/timescaledb-0:/tmp/$f --kubeconfig $KC
done

kubectl exec -it timescaledb-0 -n $NS --kubeconfig $KC -- sh -c '
  psql -U app -d energy -f /tmp/schema.sql &&
  psql -U app -d energy -c "\copy sensors(sensor_id,name,longitude,latitude,first_seen,last_seen,is_test) FROM /tmp/sensors_load.csv CSV HEADER" &&
  psql -U app -d energy -c "\copy readings(time,sensor_id,type,value) FROM /tmp/readings_load.csv CSV HEADER"'
```

Sanity check: `sensors` should have 12 rows and `readings` 192,464.

## Useful queries

```sql
-- only slots that actually have data
SELECT * FROM readings_15m WHERE sensor_id='00091a5f' AND n_readings > 0 ORDER BY slot LIMIT 10;

-- missing slots vs real zeros per sensor
SELECT sensor_id,
       count(*) FILTER (WHERE n_readings = 0)                 AS missing_slots,
       count(*) FILTER (WHERE n_readings > 0 AND value = 0)   AS real_zeros
FROM readings_15m WHERE type='densityData' GROUP BY 1;

-- longest outages
SELECT * FROM sensor_gaps ORDER BY gap_length DESC LIMIT 10;
```

`readings_15m` generates the whole grid per sensor, so filter by `sensor_id` and `slot` on large queries.

## Outliers (detected, not removed)

- No negative values; battery is always 0-100.
- One clear density spike: 292.6 on 2026-08-14 at `hn-hagenbuchersee-01` (local median about 57).
- The values of 200-260 on 2026-06-18 to 20 are a genuine crowd event across several sensors, not faults.
- No stuck non-zero density runs beyond about 20 low-count readings overnight.
- Battery is flat at 0-4% for days on several sensors, which looks like drained batteries.

Apply flags to `quality_flag` rather than deleting rows.

## Regenerating

Run [../analysis.ipynb](../analysis.ipynb) with the `.fch` kernel. The last cells rewrite `schema.sql`, `sensors_load.csv` and `readings_load.csv`.
