"""Forecast hourly densityData values from a sensor CSV using Python 3.11+.

Run with --help for the sensor list, forecast, and backtest commands.
"""

import argparse
import csv
import math
from collections import defaultdict
from dataclasses import dataclass
from datetime import date, datetime, timedelta, timezone
from statistics import median
from zoneinfo import ZoneInfo


LOCAL = ZoneInfo("Europe/Berlin")
HISTORY = timedelta(weeks=8)
TEST_PERIOD = timedelta(weeks=4)
MIN_MATCHING_DAYS = 4
MIN_READINGS_PER_HOUR = 2


@dataclass(frozen=True)
class Sensor:
    name: str
    latitude: float
    longitude: float


@dataclass(frozen=True)
class Hour:
    day: date
    hour: int
    value: float


def load_csv(path):
    sensors = {}
    latest_day = None
    buckets = defaultdict(dict)
    with open(path, newline="", encoding="utf-8-sig") as source:
        reader = csv.DictReader(source)
        required = {"time", "sensor_id", "name", "latitude", "longitude", "type", "value"}
        if not required.issubset(reader.fieldnames or []):
            raise ValueError("Missing CSV columns: " + ", ".join(sorted(required - set(reader.fieldnames or []))))
        for row in reader:
            if row["type"] != "densityData":
                continue
            try:
                timestamp = datetime.fromisoformat(row["time"])
                if timestamp.utcoffset() is None:
                    raise ValueError("timestamp needs a UTC offset")
                timestamp = timestamp.astimezone(timezone.utc)
                value = float(row["value"])
                if not math.isfinite(value) or value < 0:
                    raise ValueError("density must be finite and nonnegative")
                sensor_id = row["sensor_id"].strip()
                if not sensor_id:
                    raise ValueError("sensor_id is blank")
                sensor = Sensor(row["name"], float(row["latitude"]), float(row["longitude"]))
                if sensors.setdefault(sensor_id, sensor) != sensor:
                    raise ValueError(f"metadata changed for sensor {sensor_id}")
                hour = timestamp.replace(minute=0, second=0, microsecond=0)
                readings = buckets[sensor_id, hour]
                if readings.setdefault(timestamp, value) != value:
                    raise ValueError("conflicting readings at the same sensor and timestamp")
                day = timestamp.astimezone(LOCAL).date()
                latest_day = day if latest_day is None else max(latest_day, day)
            except (AttributeError, TypeError, ValueError) as error:
                raise ValueError(f"CSV row {reader.line_num}: {error}") from error

    if latest_day is None:
        raise ValueError("No densityData readings found")
    local_hours = defaultdict(list)
    for (sensor_id, hour), readings in buckets.items():
        if len(readings) >= MIN_READINGS_PER_HOUR:
            local = hour.astimezone(LOCAL)
            local_hours[sensor_id, local.date(), local.hour].append(median(readings.values()))
    hours_by_sensor = {sensor_id: [] for sensor_id in sensors}
    for (sensor_id, day, hour), values in sorted(local_hours.items()):
        hours_by_sensor[sensor_id].append(Hour(day, hour, median(values)))
    return sensors, hours_by_sensor, latest_day


def fit(hours, cutoff):
    by_slot = defaultdict(list)
    start = cutoff - HISTORY
    for reading in hours:
        if start <= reading.day < cutoff:
            by_slot[reading.day.weekday(), reading.hour].append(reading.value)
    return by_slot


def backtest(sensors, hours_by_sensor, end):
    start = end - TEST_PERIOD
    print(f"Test period: {start} through {end - timedelta(days=1)} (Europe/Berlin)")
    print(f"{'Sensor':<31} {'Scored hours':>12} {'Observed':>9} {'Forecast MAE':>14} {'Baseline MAE':>14}")
    total = observed = 0
    model_error = baseline_error = 0.0
    for sensor_id, sensor in sorted(sensors.items(), key=lambda entry: (entry[1].name, entry[0])):
        hours = hours_by_sensor[sensor_id]
        test = [reading for reading in hours if start <= reading.day < end]
        if not test:
            continue
        by_slot = fit(hours, start)
        training = [value for values in by_slot.values() for value in values]
        baseline = median(training) if training else None
        errors = []
        for reading in test:
            samples = by_slot.get((reading.day.weekday(), reading.hour), [])
            if len(samples) >= MIN_MATCHING_DAYS:
                errors.append((abs(reading.value - median(samples)), abs(reading.value - baseline)))
        count = len(errors)
        forecast_sum = sum(error[0] for error in errors)
        baseline_sum = sum(error[1] for error in errors)
        label = f"{sensor.name} [{sensor_id}]"
        forecast_mae = f"{forecast_sum / count:.2f}" if count else "—"
        baseline_mae = f"{baseline_sum / count:.2f}" if count else "—"
        print(f"{label:<31} {count:>12,} {len(test):>9,} {forecast_mae:>14} {baseline_mae:>14}")
        total += count
        observed += len(test)
        model_error += forecast_sum
        baseline_error += baseline_sum
    if total:
        print(f"{'All sensors':<31} {total:>12,} {observed:>9,} {model_error / total:>14.2f} {baseline_error / total:>14.2f}")
    else:
        print("No held-out hours had enough matching training dates.")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("csv", help="Path to the sensor CSV")
    commands = parser.add_subparsers(dest="command", required=True)
    commands.add_parser("sensors", help="List sensor IDs and coordinates")
    commands.add_parser("backtest", help="Score the model on the final 28 complete local dates")
    forecast = commands.add_parser("forecast", help="Forecast the median density reading for a local hour")
    forecast.add_argument("sensor_id", help="Use the sensors command to find the ID")
    forecast.add_argument("time", help="Europe/Berlin local time on the hour, e.g. '2026-11-14 15:00'")
    args = parser.parse_args()

    try:
        sensors, hours_by_sensor, latest_day = load_csv(args.csv)
        if args.command == "sensors":
            for sensor_id, sensor in sorted(sensors.items(), key=lambda entry: (entry[1].name, entry[0])):
                print(f"{sensor_id}  {sensor.name:<22}  {sensor.latitude:.6f}, {sensor.longitude:.6f}")
        elif args.command == "backtest":
            backtest(sensors, hours_by_sensor, latest_day)
        else:
            if args.sensor_id not in sensors:
                raise ValueError("Unknown sensor ID. Run the sensors command to list valid IDs.")
            target = datetime.strptime(args.time, "%Y-%m-%d %H:%M")
            if target.minute:
                raise ValueError("Enter the start of an hour, such as 15:00")
            if target.replace(tzinfo=LOCAL).astimezone(timezone.utc).astimezone(LOCAL).replace(tzinfo=None) != target:
                raise ValueError("That local hour does not exist due to the daylight saving transition")
            # Use whole local days, excluding the final potentially partial day in the CSV.
            cutoff = min(target.date(), latest_day)
            by_slot = fit(hours_by_sensor[args.sensor_id], cutoff)
            samples = by_slot.get((target.weekday(), target.hour), [])
            if len(samples) < MIN_MATCHING_DAYS:
                raise ValueError(f"Forecast unavailable for {args.sensor_id}: {len(samples)} matching dates. Need {MIN_MATCHING_DAYS}.")
            label = sensors[args.sensor_id].name
            print(f"{label} [{args.sensor_id}] | {target:%Y-%m-%d %H:%M} Europe/Berlin")
            print(f"History through {cutoff - timedelta(days=1)}")
            print(f"Forecast: {median(samples):.1f} densityData units ({len(samples)} matching dates)")
    except (OSError, ValueError) as error:
        parser.exit(2, f"{error}\n")


if __name__ == "__main__":
    main()
