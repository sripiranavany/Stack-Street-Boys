"""Seed Ebb's decision layer (schema "ebb") from the City Pulse data that is already in the database.

- Readings and sensors come from City Pulse's public.readings / public.sensors (no raw CSV needed).
- Weather, archived weather forecasts and Baden-Wuerttemberg holidays come from ./context (public, small).
- City Pulse's empty public.weather_hourly, public.holidays and public.events are filled too.
- Store door-counter history comes from ./context/store_entries.csv (synthetic, see scripts/make_store_entries.py).

Runs as a one-off Kubernetes Job (same image as the forecaster) or locally:
  POSTGRES_PASSWORD=... DB_HOST=localhost python seed.py
The Go service creates the ebb tables on start-up; this script waits for them.
"""
import csv, json, math, os, time
from datetime import date, timedelta

import pandas as pd
import psycopg

from app import DSN

CTX = os.path.join(os.path.dirname(os.path.abspath(__file__)), "context")
LABELS = {  # sensor_id: (label, active in Ebb)
    "f8c19730": ("Lohtorstraße", True), "00091a5f": ("Götzenturm", True), "e52c55f3": ("Hagenbuchersee North", True),
    "0f8519cf": ("Hagenbuchersee South", True), "8d7eef22": ("Marahaus", True), "d4cce74b": ("Neckarufer", True),
    "ba71377f": ("Soleo", True), "f4928534": ("Stadtgalerie", True), "863f75b0": ("Lohtorstraße (old device)", False),
}
STORE_CFG = {"open_days": [0, 1, 2, 3, 4, 5], "open_from": 7, "open_to": 22, "dwell_seconds": 30, "capture_rate": 0.03,
             "customers_per_cashier_h": 40, "min_staff": 1, "bakery_buy_rate": 0.3, "items_per_buyer": 2,
             "batch_size": 40, "bake_lead_min": 30}
CAMPAIGN_CFG = {"cap_pct_of_peak": 0.8, "acceptance": 0.25, "tiers": [10, 15, 20], "flex_hours": 4,
                "customers_per_week": 3000, "weekly_budget_eur": 600, "avg_discounted_spend_eur": 6.0}
STATE = {"mode": "replay", "running": "true", "speed": "60", "sim_now": "2026-10-31T00:00:00+01:00",
         "replay_start": "2026-10-31T00:00:00+01:00", "replay_end": "2026-11-08T00:00:00+01:00",
         "train_cutoff": "2026-10-31T00:00:00+01:00"}


def rows(name):
    return list(csv.DictReader(open(os.path.join(CTX, name), encoding="utf-8")))


def exists(cur, table):
    return cur.execute("SELECT to_regclass(%s) IS NOT NULL", (table,)).fetchone()[0]


def main():
    con = psycopg.connect(DSN, autocommit=True, options="-c search_path=ebb,public")
    cur = con.cursor()
    for i in range(60):
        if exists(cur, "ebb.app_state"):
            break
        print("waiting for the Go service to create the ebb schema ...")
        time.sleep(5)
    else:
        raise SystemExit("ebb schema not found")

    print("readings from public.readings ...")
    cur.execute("TRUNCATE ebb.readings")
    cur.execute("""
        INSERT INTO ebb.readings (sensor_id, ts, density, battery)
        SELECT sensor_id, to_timestamp(round(extract(epoch FROM time) / 60) * 60) AS ts,
               max(value) FILTER (WHERE type = 'densityData'), max(value) FILTER (WHERE type = 'batteryData')
        FROM public.readings WHERE quality_flag = 0 AND sensor_id = ANY(%s)
        GROUP BY 1, 2 HAVING max(value) FILTER (WHERE type = 'densityData') IS NOT NULL
        ON CONFLICT DO NOTHING""", (list(LABELS),))
    print("  ", cur.execute("SELECT count(*) FROM ebb.readings").fetchone()[0], "rows")

    coords = {r[0]: (r[1], r[2], r[3]) for r in cur.execute("SELECT sensor_id, latitude, longitude, name FROM public.sensors")}
    active = [s for s, v in LABELS.items() if v[1]]
    cur.execute("TRUNCATE ebb.sensors")
    for sid, (label, act) in LABELS.items():
        lat, lon, name = coords[sid]
        nb = min((o for o in active if o != sid), key=lambda o: math.dist(coords[o][:2], (lat, lon)))
        cur.execute("INSERT INTO ebb.sensors VALUES (%s,%s,%s,%s,%s,%s,%s)", (sid, name, label, lat, lon, act, nb))

    print("weather and holidays ...")
    cur.execute("TRUNCATE ebb.weather; TRUNCATE ebb.holidays")
    wa = rows("weather_actual.csv")
    cur.executemany("""INSERT INTO ebb.weather VALUES ((%s::timestamp AT TIME ZONE 'Europe/Berlin'),'actual',0,%s,%s,%s,%s,%s)
                       ON CONFLICT DO NOTHING""", [(r["ts"], r["temp"], r["precip"], r["cloud"], r["sun"], r["wind"]) for r in wa])
    cur.executemany("""INSERT INTO ebb.weather VALUES ((%s::timestamp AT TIME ZONE 'Europe/Berlin'),'forecast',%s,%s,%s,%s,%s,%s)
                       ON CONFLICT DO NOTHING""", [(r["ts"], r["lead_days"], r["temp"], r["precip"], r["cloud"], r["sun"], r["wind"])
                                                    for r in rows("weather_forecast.csv")])
    hol = rows("holidays.csv")
    cur.executemany("INSERT INTO ebb.holidays VALUES (%s,%s,%s,%s)", [(r["start_date"], r["end_date"], r["kind"], r["name"]) for r in hol])

    # City Pulse's context tables (were empty): weather, holidays, events
    if exists(cur, "public.weather_hourly"):
        cur.executemany("""INSERT INTO public.weather_hourly (time, temperature_c, precipitation_mm, wind_speed_ms)
                           VALUES ((%s::timestamp AT TIME ZONE 'Europe/Berlin'), %s, %s, %s) ON CONFLICT (time) DO NOTHING""",
                        [(r["ts"], r["temp"], r["precip"], round(float(r["wind"]) / 3.6, 2)) for r in wa])
    if exists(cur, "public.holidays"):
        for r in hol:
            d0, d1 = date.fromisoformat(r["start_date"]), date.fromisoformat(r["end_date"])
            name = r["name"] if r["kind"] == "public" else r["name"] + " (school holidays)"
            for k in range((d1 - d0).days + 1):
                cur.execute("INSERT INTO public.holidays (date, name, region) VALUES (%s,%s,'BW') ON CONFLICT DO NOTHING",
                            (d0 + timedelta(days=k), name))

    print("stores, campaigns, Lichterfest ...")
    cur.execute("TRUNCATE ebb.stores; TRUNCATE ebb.campaigns")
    # capacity: most people the store should hold at once; visit_minutes turns door entries/h into people inside
    for sid, name, sensor, capacity in (("freshmart-neckarufer", "FreshMart Neckarufer", "d4cce74b", 120),
                                        ("freshmart-stadtgalerie", "FreshMart Stadtgalerie", "f4928534", 160)):
        lat, lon, _ = coords[sensor]
        cfg = dict(STORE_CFG, capacity=capacity, visit_minutes=15)
        cur.execute("INSERT INTO ebb.stores VALUES (%s,%s,%s,%s,%s,%s)", (sid, name, sensor, lat, lon, json.dumps(cfg)))
        cur.execute("INSERT INTO ebb.campaigns VALUES (%s,%s,true)", (sid, json.dumps(CAMPAIGN_CFG)))
    if not exists(cur, "ebb.store_entries"):
        raise SystemExit("ebb.store_entries not found: restart the Go service so it creates the new tables, then seed again")
    cur.execute("TRUNCATE ebb.store_entries")
    cur.executemany("INSERT INTO ebb.store_entries VALUES (%s,%s,%s,%s)",
                    [(r["store_id"], r["ts"], r["entries"], r["is_open"] == "1") for r in rows("store_entries.csv")])
    print("  ", cur.execute("SELECT count(*) FROM ebb.store_entries").fetchone()[0], "door-counter rows")
    lichterfest(con, cur, active)
    if exists(cur, "public.events"):
        cur.execute("""INSERT INTO public.events (name, starts_at, ends_at, venue, latitude, longitude, source)
                       VALUES ('Heilbronner Lichterfest 2026', '2026-06-18 17:00+02', '2026-06-21 00:00+02',
                               'Neckar riverside (Bollwerksturm to Götzenturm)', 49.1430, 9.2160, 'heilbronn.de')
                       ON CONFLICT DO NOTHING""")

    cur.execute("TRUNCATE ebb.app_state")
    cur.executemany("INSERT INTO ebb.app_state VALUES (%s,%s)", list(STATE.items()))
    cur.execute("TRUNCATE ebb.forecast_runs CASCADE; TRUNCATE ebb.alerts; TRUNCATE ebb.offers")
    print("done. Restart the forecaster so it trains on the seeded data.")


def lichterfest(con, cur, active):
    """Measured event profile: actual / typical density per sensor x hour on 18-20 Jun 2026."""
    cur.execute("SELECT sensor_id, ts, density FROM ebb.readings WHERE sensor_id = ANY(%s)", (active,))
    df = pd.DataFrame(cur.fetchall(), columns=["sensor_id", "ts", "density"])
    df["ts"] = pd.to_datetime(df.ts, utc=True).dt.tz_convert("Europe/Berlin")
    df["date"] = df.ts.dt.date.astype(str); df["dow"] = df.ts.dt.dayofweek; df["hour"] = df.ts.dt.hour
    h = df.groupby(["sensor_id", "date", "dow", "hour"]).density.mean().reset_index()
    normal = h[(h.date >= "2026-06-22") & (h.date <= "2026-10-30")]
    typ = normal.groupby(["sensor_id", "dow", "hour"]).density.median().rename("typ").reset_index()
    ev = h[h.date.isin(["2026-06-18", "2026-06-19", "2026-06-20"])].merge(typ, on=["sensor_id", "dow", "hour"])
    ev["ratio"] = (ev.density + 1) / (ev.typ + 1)
    prof = ev.groupby(["sensor_id", "hour"]).ratio.mean().clip(1.0, 8.0).reset_index()
    prof.loc[~prof.hour.isin([17, 18, 19, 20, 21, 22, 23]), "ratio"] = 1.0
    cur.execute("DELETE FROM ebb.event_profiles WHERE profile='lichterfest'; TRUNCATE ebb.events RESTART IDENTITY")
    cur.executemany("INSERT INTO ebb.event_profiles VALUES ('lichterfest',%s,%s,%s)",
                    [(r.sensor_id, int(r.hour), float(round(r.ratio, 3))) for r in prof.itertuples()])
    affected = sorted(prof[prof.ratio > 1.5].sensor_id.unique().tolist())
    cur.execute("""INSERT INTO ebb.events (name, category, start_ts, end_ts, sensor_ids, profile, recurring, source)
                   VALUES ('Heilbronner Lichterfest 2026','festival','2026-06-18 17:00+02','2026-06-21 00:00+02',%s,'lichterfest',true,'labelled')""",
                (affected,))


if __name__ == "__main__":
    main()
