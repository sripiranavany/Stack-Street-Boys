"""Fetch enrichment data (weather actuals, archived weather forecasts, BW holidays) into data/context/.

All sources are free, keyless, public. Output CSVs are small and committed to the repo.
Usage: python scripts/fetch_context.py
"""
import csv, io, json, os, urllib.request

LAT, LON = 49.142, 9.217
OUT = os.path.join(os.path.dirname(__file__), "..", "forecaster", "context")
VARS = ["temperature_2m", "precipitation", "cloud_cover", "sunshine_duration", "wind_speed_10m"]
SHORT = {"temperature_2m": "temp", "precipitation": "precip", "cloud_cover": "cloud",
         "sunshine_duration": "sun", "wind_speed_10m": "wind"}


def get(url):
    with urllib.request.urlopen(urllib.request.Request(url, headers={"User-Agent": "ebb-hackathon"}), timeout=60) as r:
        return r.read().decode("utf-8")


def om_csv(text):
    lines = text.splitlines()
    i = next(k for k, l in enumerate(lines) if l.startswith("time,"))
    return list(csv.DictReader(io.StringIO("\n".join(lines[i:]))))


def weather_actual(start, end):
    url = (f"https://archive-api.open-meteo.com/v1/archive?latitude={LAT}&longitude={LON}"
           f"&start_date={start}&end_date={end}&hourly={','.join(VARS)}&timezone=Europe%2FBerlin&format=csv")
    rows = om_csv(get(url))
    out = []
    for r in rows:
        vals = {SHORT[v]: r[next(k for k in r if k.startswith(v + " "))] for v in VARS}
        out.append({"ts": r["time"].replace("T", " "), **vals})
    return out


def weather_forecast(start, end):
    """Forecasts as issued N days before (N=1..7), so replay never sees the future."""
    hv = ",".join(f"{v}_previous_day{d}" for v in VARS for d in range(1, 8))
    url = (f"https://previous-runs-api.open-meteo.com/v1/forecast?latitude={LAT}&longitude={LON}"
           f"&start_date={start}&end_date={end}&hourly={hv}&timezone=Europe%2FBerlin&format=csv")
    rows = om_csv(get(url))
    out = []
    for r in rows:
        for d in range(1, 8):
            vals = {}
            for v in VARS:
                key = next(k for k in r if k.startswith(f"{v}_previous_day{d} "))
                vals[SHORT[v]] = r[key]
            if all(x != "" for x in vals.values()):
                out.append({"ts": r["time"].replace("T", " "), "lead_days": d, **vals})
    return out


def holidays(start, end):
    out = []
    for kind, ep in (("public", "PublicHolidays"), ("school", "SchoolHolidays")):
        url = (f"https://openholidaysapi.org/{ep}?countryIsoCode=DE&subdivisionCode=DE-BW"
               f"&languageIsoCode=EN&validFrom={start}&validTo={end}")
        for h in json.loads(get(url)):
            out.append({"start_date": h["startDate"], "end_date": h["endDate"], "kind": kind,
                        "name": h["name"][0]["text"]})
    return out


def write(name, rows):
    os.makedirs(OUT, exist_ok=True)
    with open(os.path.join(OUT, name), "w", newline="", encoding="utf-8") as f:
        w = csv.DictWriter(f, fieldnames=list(rows[0].keys()))
        w.writeheader(); w.writerows(rows)
    print(f"{name}: {len(rows)} rows")


if __name__ == "__main__":
    write("weather_actual.csv", weather_actual("2025-10-01", "2026-11-12"))
    write("weather_forecast.csv", weather_forecast("2026-10-20", "2026-11-14"))
    write("holidays.csv", holidays("2025-01-01", "2027-12-31"))
