"""Generate a SYNTHETIC hourly door-counter dataset for the two demo stores.

No real store data exists for this project. This script simulates what a door counter at each
FreshMart store would have recorded, derived from the real street-sensor readings next to it:

  entries = regulars(hour, weekday)  +  passers-by x capture rate(hour, weekday, weather, events)

- passers-by/h = sensor density x 3600 / dwell seconds   (same formula as backend/internal/domain/store.go)
- the capture rate averages about 3% (the app's default) but is NOT constant, so a model trained on
  this file has something real to learn beyond "density times a constant"
- closed hours, Sundays and public holidays are 0
- hours where the street sensor was offline are filled from that sensor's typical weekday/hour level,
  because a door counter keeps counting when the street sensor is down

Usage: python scripts/make_store_entries.py
Output: forecaster/context/store_entries.csv  (ts is the Europe/Berlin hour start, with UTC offset)
"""
import os

import numpy as np
import pandas as pd

ROOT = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..")
RAW = os.path.join(ROOT, "data-1794095549194.csv")
CTX = os.path.join(ROOT, "forecaster", "context")
OUT = os.path.join(CTX, "store_entries.csv")
TZ = "Europe/Berlin"
SEED = 2026

# Same stores, sensors and opening hours as forecaster/seed.py
STORES = {"freshmart-neckarufer": "d4cce74b", "freshmart-stadtgalerie": "f4928534"}
OPEN_DAYS, OPEN_FROM, OPEN_TO = {0, 1, 2, 3, 4, 5}, 7, 22
DWELL_SECONDS = 30
BASE_CAPTURE = 0.03
REGULARS_PER_HOUR = 12          # shoppers who come regardless of street footfall
EVENT_EVENINGS = ("2026-06-18", "2026-06-19", "2026-06-20")  # Lichterfest: crowds outside, few shoppers

# Share of passers-by who enter, relative to the base rate, by hour of day
HOUR_FACTOR = {7: 1.25, 8: 1.25, 9: 1.05, 10: 0.95, 11: 1.0, 12: 1.15, 13: 1.1, 14: 0.9, 15: 0.9,
               16: 1.05, 17: 1.25, 18: 1.25, 19: 1.05, 20: 0.8, 21: 0.65}
DOW_FACTOR = {0: 0.95, 1: 0.95, 2: 1.0, 3: 1.0, 4: 1.1, 5: 1.15}


def hourly_density(sensor_id, hours):
    """Mean density per local hour; offline hours filled from the typical weekday/hour level."""
    df = pd.read_csv(RAW)
    df = df[(df.type == "densityData") & (df.sensor_id == sensor_id)]
    ts = pd.to_datetime(df.time, format="mixed", utc=True).dt.floor("h").dt.tz_convert(TZ)
    measured = df.value.groupby(ts).mean().reindex(hours)
    typical = measured.groupby([hours.day_of_week, hours.hour]).transform("median")
    by_hour = measured.groupby(hours.hour).transform("median")
    filled = measured.fillna(typical).fillna(by_hour)
    return filled, measured.notna()


def main():
    rng = np.random.default_rng(SEED)
    hours = pd.date_range("2026-06-13 00:00", "2026-11-07 23:00", freq="h", tz=TZ)
    days = hours.normalize()

    wx = pd.read_csv(os.path.join(CTX, "weather_actual.csv"))
    wx["ts"] = pd.to_datetime(wx.ts).dt.tz_localize(TZ, ambiguous="NaT", nonexistent="NaT")
    wx = wx.dropna(subset=["ts"]).drop_duplicates("ts").set_index("ts").reindex(hours).ffill()

    hol = pd.read_csv(os.path.join(CTX, "holidays.csv"))
    public = set()
    for r in hol[hol.kind == "public"].itertuples():
        public.update(d.date() for d in pd.date_range(r.start_date, r.end_date))
    is_holiday = np.array([d.date() in public for d in days])
    eve_of_closed = np.array([(d + pd.Timedelta(days=1)).date() in public for d in days])  # stock-up day

    is_open = np.isin(hours.day_of_week, list(OPEN_DAYS)) & (hours.hour >= OPEN_FROM) & (hours.hour < OPEN_TO) & ~is_holiday
    hour_f = np.array([HOUR_FACTOR.get(h, 0.0) for h in hours.hour])
    dow_f = np.array([DOW_FACTOR.get(d, 0.0) for d in hours.day_of_week])
    rain_f = np.where(wx.precip.values >= 0.3, 1.12, 1.0)          # people duck inside when it rains
    hot_f = np.where(wx.temp.values >= 26, 1.15, 1.0)              # drinks and ice cream
    eve_f = np.where(eve_of_closed, 1.25, 1.0)
    event_f = np.where(np.isin(days.strftime("%Y-%m-%d"), EVENT_EVENINGS) & (hours.hour >= 17), 0.45, 1.0)

    out = []
    for store_id, sensor_id in STORES.items():
        density, measured = hourly_density(sensor_id, hours)
        day_codes, day_index = pd.factorize(days)
        day_noise = rng.lognormal(0, 0.08, len(day_index))[day_codes]   # good days and bad days
        capture = BASE_CAPTURE * hour_f * dow_f * rain_f * hot_f * eve_f * event_f
        passers = density.values * 3600 / DWELL_SECONDS
        regulars = REGULARS_PER_HOUR * hour_f * dow_f * eve_f
        expected = np.where(is_open, (regulars + passers * capture) * day_noise, 0.0)
        out.append(pd.DataFrame({
            "ts": hours.strftime("%Y-%m-%dT%H:%M:%S%z").str.replace(r"(\d\d)(\d\d)$", r"\1:\2", regex=True),
            "store_id": store_id,
            "sensor_id": sensor_id,
            "is_open": is_open.astype(int),
            "entries": rng.poisson(expected),
            "sensor_online": measured.values.astype(int),
        }))
    df = pd.concat(out, ignore_index=True)
    df.to_csv(OUT, index=False)
    print(f"wrote {len(df):,} rows to {os.path.relpath(OUT, ROOT)}")


if __name__ == "__main__":
    main()
