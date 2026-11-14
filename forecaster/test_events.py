"""Unit tests for events.py (no database): python -m pytest test_events.py, or python test_events.py."""
import pandas as pd

import events as ev


def test_series_key_and_matching():
    assert ev.series_key("Heilbronner Lichterfest 2026") == "heilbronner-lichterfest"
    assert ev.series_key("Heilbronner Lichterfest 2027 (expected)") == "heilbronner-lichterfest"
    assert ev.series_key("Käthchen Weihnachtsmarkt 2025") == "kaethchen-weihnachtsmarkt"
    assert ev.same_series("lichterfest", "heilbronner-lichterfest")
    assert not ev.same_series("heilbronner-weindorf", "heilbronner-lichterfest")
    assert not ev.same_series("heilbronner", "heilbronner-lichterfest")  # only a generic word in common
    assert not ev.same_series("street-market", "farmers-market")
    m = ev.canonical(["lichterfest", "heilbronner-lichterfest", "weindorf"])
    assert m == {"lichterfest": "heilbronner-lichterfest", "heilbronner-lichterfest": "heilbronner-lichterfest",
                 "weindorf": "weindorf"}


def test_next_occurrence_keeps_weekday():
    start, end = pd.Timestamp("2026-06-18 17:00"), pd.Timestamp("2026-06-21 00:00")
    s, e = ev.next_occurrence(start, end, pd.Timestamp("2026-11-14"))
    assert s == pd.Timestamp("2027-06-17 17:00") and s.dayofweek == start.dayofweek and e - s == end - start


def test_profile_shrinks_single_observations():
    r = pd.DataFrame({"sensor_id": ["a", "a", "a", "b"], "hod": [18, 18, 18, 18], "ratio": [3.0, 3.0, 3.0, 3.0]})
    p = ev._profile(r, "sensor_id").set_index("sensor_id").ratio
    assert p["a"] == 2.5   # 3 days: 1 + 2 * 3/4
    assert p["b"] == 2.0   # 1 day:  1 + 2 * 1/2


def test_drop_event_hours():
    df = pd.DataFrame({"sensor_id": ["a", "a", "b"], "hour": pd.to_datetime(["2026-06-18 18:00", "2026-06-18 19:00", "2026-06-18 18:00"])})
    cells = pd.DataFrame({"sensor_id": ["a"], "hour": pd.to_datetime(["2026-06-18 18:00"])})
    out = ev.drop_event_hours(df, cells)
    assert len(out) == 2 and not ((out.sensor_id == "a") & (out.hour == pd.Timestamp("2026-06-18 18:00"))).any()


def test_editions_group_market_days():
    days = pd.date_range("2025-11-25 11:00", periods=28, freq="D")
    g = pd.DataFrame({"start": list(days) + [pd.Timestamp("2026-11-24 11:00")]})
    assert ev.editions(g) == 2
    assert len(ev.last_edition(g)) == 1
    assert len(ev.last_edition(g.iloc[:28])) == 28


if __name__ == "__main__":
    for name, fn in list(globals().items()):
        if name.startswith("test_"):
            fn()
    print("ok")
