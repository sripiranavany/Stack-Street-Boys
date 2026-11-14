"""Events: learn their effect from history and apply it to future events.

- learn():   for every past event, compare measured street density and store door entries with the usual level for
             that sensor/store, weekday and hour (median of the surrounding 8 weeks, event days left out). Occurrences
             of the same event in different years form a *series* ("Heilbronner Lichterfest 2026" and "... 2027" ->
             "heilbronner-lichterfest"); a series' multipliers are the mean over its occurrences.
- project(): recurring events get their next occurrence added automatically (same weekday, 52 weeks later), marked
             source='projected', so the forecast already knows about next year's Lichterfest.
- apply():   multiplier for each future event hour, in this order:
               1. an explicit profile on the event (e.g. the hand-labelled 2026 Lichterfest)
               2. the learned profile of the event's series
               3. the mean learned effect of past events in the same category (e.g. other festivals)
               4. the size default (S/M/L)
             plus, for stores, the learned shopper multiplier: crowds outside do not always shop.
- event_hours(): sensor x hour cells covered by past events, so the base model is trained on normal hours only.

All timestamps are naive Europe/Berlin local hours, like ebbmodel.
"""
from __future__ import annotations

import json
import logging
import re

import numpy as np
import pandas as pd

log = logging.getLogger("forecaster")

SIZE_MULT = {"S": 1.3, "M": 2.0, "L": 4.0}
WINDOW = pd.Timedelta(weeks=8)          # reference period on each side of an event for the "usual" level
YEAR_STEP = pd.Timedelta(weeks=52)      # recurring events: same weekday a year later
PROJECT_AHEAD = pd.Timedelta(days=400)
MATCH_TOLERANCE = pd.Timedelta(days=21)  # an occurrence within 3 weeks of the projected date counts as "already planned"
CLIP = (0.3, 8.0)
GENERIC_CATEGORIES = {"", "event"}      # too broad to borrow an effect from
# Words that say nothing about which event it is: "Heilbronner Weindorf" must not match "Heilbronner Lichterfest".
GENERIC_TOKENS = {"event", "events", "fest", "festival", "market", "markt", "day", "days", "night", "nacht", "tag",
                  "the", "and", "of", "der", "die", "das", "und", "am", "im", "in", "heilbronn", "heilbronner", "expected"}
SHRINK = 1.0  # one past occurrence-day counts as much as this many "no effect" days: noisy single events stay modest
_YEAR = re.compile(r"\b(19|20)\d{2}\b")


def series_key(name: str) -> str:
    s = _YEAR.sub(" ", (name or "").lower()).replace("(expected)", " ")
    return re.sub(r"[^a-z0-9]+", "-", s).strip("-")


def _tokens(key: str) -> set[str]:
    return {t for t in key.split("-") if t}


def same_series(a: str, b: str) -> bool:
    """True when one name's words are all contained in the other's and include a distinctive word
    ("lichterfest" ~ "heilbronner-lichterfest", but not "heilbronner-weindorf" ~ "heilbronner-lichterfest")."""
    ta, tb = _tokens(a), _tokens(b)
    small, big = (ta, tb) if len(ta) <= len(tb) else (tb, ta)
    return bool(small) and small <= big and bool(small - GENERIC_TOKENS)


def canonical(keys) -> dict[str, str]:
    """Map every series key to the most specific matching key (more words first)."""
    canon, out = [], {}
    for k in sorted(set(keys), key=lambda k: (-len(_tokens(k)), k)):
        hit = next((c for c in canon if same_series(k, c)), None)
        if hit is None:
            canon.append(k)
            hit = k
        out[k] = hit
    return out


def _q(con, sql, params=None) -> pd.DataFrame:
    cur = con.execute(sql, params or {})
    return pd.DataFrame(cur.fetchall(), columns=[d.name for d in cur.description])


def load(con) -> pd.DataFrame:
    ev = _q(con, """SELECT id, name, category, start_ts AT TIME ZONE 'Europe/Berlin' AS start,
                           end_ts AT TIME ZONE 'Europe/Berlin' AS "end", sensor_ids, size, profile, recurring, source
                    FROM events ORDER BY start_ts""")
    if ev.empty:
        return ev.assign(series=pd.Series(dtype=str))
    ev["start"], ev["end"] = pd.to_datetime(ev.start), pd.to_datetime(ev["end"])
    ev["series"] = ev.name.map(series_key)
    ev["series"] = ev.series.map(canonical(ev.series))
    return ev


def _cells(e) -> pd.DataFrame:
    hours = pd.date_range(e.start.floor("h"), e.end - pd.Timedelta(seconds=1), freq="h")
    return pd.DataFrame([(s, h) for s in e.sensor_ids for h in hours], columns=["sensor_id", "hour"])


def event_hours(ev: pd.DataFrame, until: pd.Timestamp | None = None) -> pd.DataFrame:
    """sensor x hour cells covered by (past) events."""
    if until is not None:
        ev = ev[ev.start < until]
    parts = [_cells(e) for e in ev.itertuples()]
    return pd.concat(parts, ignore_index=True).drop_duplicates() if parts else pd.DataFrame(columns=["sensor_id", "hour"])


def drop_event_hours(df: pd.DataFrame, cells: pd.DataFrame, sensor_col: str = "sensor_id") -> pd.DataFrame:
    if cells.empty:
        return df
    m = df.merge(cells.rename(columns={"sensor_id": sensor_col}).assign(_ev=1), on=[sensor_col, "hour"], how="left")
    return df[m._ev.isna().values]


def _ratios(values: pd.DataFrame, key: str, cells_days: pd.DataFrame, e, smooth: float) -> pd.DataFrame:
    """values: key, hour, value. Ratio of each event hour to the usual weekday/hour level around the event."""
    v = values.copy()
    v["date"], v["dow"], v["hod"] = v.hour.dt.normalize(), v.hour.dt.dayofweek, v.hour.dt.hour
    near = v[(v.hour >= e.start - WINDOW) & (v.hour < e.end + WINDOW)]
    normal = near.merge(cells_days.assign(_ev=1), on=[key, "date"], how="left")
    normal = normal[normal._ev.isna()]
    typ = normal.groupby([key, "dow", "hod"]).value.median().rename("typ").reset_index()
    during = v[(v.hour >= e.start.floor("h")) & (v.hour < e.end)]
    r = during.merge(typ, on=[key, "dow", "hod"])
    r["ratio"] = ((r.value + smooth) / (r.typ + smooth)).clip(*CLIP)
    return r[[key, "hod", "ratio"]]


def _profile(r: pd.DataFrame, key: str) -> pd.DataFrame:
    """Mean ratio per key x hour, shrunk towards 1 by the number of measured days: m = 1 + (mean - 1) * n / (n + SHRINK)."""
    g = r.groupby([key, "hod"]).ratio.agg(["mean", "count"]).reset_index()
    g["ratio"] = (1 + (g["mean"] - 1) * g["count"] / (g["count"] + SHRINK)).round(3)
    return g[[key, "hod", "ratio"]]


def learn(con, until: pd.Timestamp, hist: pd.DataFrame, entries: pd.DataFrame, stores: pd.DataFrame) -> int:
    """Learn multipliers from events that ended before `until`. hist: sensor_id, hour, value (street density);
    entries: store_id, hour, entries (open hours); stores: store_id, sensor_id. Returns the number of series."""
    ev = load(con)
    past = ev[ev["end"] <= until]
    if past.empty:
        return 0
    # days touched by any event, per sensor / store (left out of the "usual" level)
    cells = event_hours(ev)
    days = cells.assign(date=cells.hour.dt.normalize())[["sensor_id", "date"]].drop_duplicates()
    sdays = days.merge(stores[["store_id", "sensor_id"]], on="sensor_id")[["store_id", "date"]].drop_duplicates()
    hist = hist[hist.hour < until]
    ent = entries[entries.hour < until].rename(columns={"entries": "value"}) if not entries.empty else entries
    sensor_of = dict(zip(stores.store_id, stores.sensor_id))

    rows, street_prof, store_prof = [], [], []
    for series, g in past.groupby("series"):
        sr, st = [], []
        for e in g.itertuples():
            h = hist[hist.sensor_id.isin(e.sensor_ids)]
            if not h.empty:
                sr.append(_ratios(h, "sensor_id", days, e, smooth=1.0))
            mine = [s for s, sen in sensor_of.items() if sen in e.sensor_ids]
            if mine and not ent.empty:
                x = ent[ent.store_id.isin(mine)]
                if not x.empty:
                    st.append(_ratios(x, "store_id", sdays, e, smooth=5.0))
        sr = pd.concat(sr) if sr else pd.DataFrame(columns=["sensor_id", "hod", "ratio"])
        if len(sr) < 3:
            log.info("event %s: not enough measured hours to learn from", series)
            continue
        sp = _profile(sr, "sensor_id")
        stp = _profile(pd.concat(st), "store_id") if st else pd.DataFrame(columns=["store_id", "hod", "ratio"])
        last = g.sort_values("start").iloc[-1]
        detail = {"sensors": {s: {int(r.hod): float(r.ratio) for r in d.itertuples()} for s, d in sp.groupby("sensor_id")},
                  "stores": {s: {int(r.hod): float(r.ratio) for r in d.itertuples()} for s, d in stp.groupby("store_id")}}
        rows.append((series, last["name"], last.category or "event", len(g),
                     last.start.tz_localize("Europe/Berlin", ambiguous=True, nonexistent="shift_forward").to_pydatetime(),
                     sorted({s for x in g.sensor_ids for s in x}), float(sp.ratio.max()), float(sp.ratio.mean()),
                     float(stp.ratio.mean()) if len(stp) else None, json.dumps(detail)))
        street_prof += [(f"learned:{series}", r.sensor_id, int(r.hod), float(r.ratio)) for r in sp.itertuples()]
        store_prof += [(f"learned:{series}", r.store_id, int(r.hod), float(r.ratio)) for r in stp.itertuples()]
        log.info("event %s: learned from %d occurrence(s), street x%.2f on average (peak x%.2f)%s", series, len(g),
                 sp.ratio.mean(), sp.ratio.max(), f", shoppers x{stp.ratio.mean():.2f}" if len(stp) else "")

    with con.cursor() as cur:
        cur.execute("DELETE FROM event_profiles WHERE profile LIKE 'learned:%%'")
        cur.execute("DELETE FROM event_store_profiles WHERE profile LIKE 'learned:%%'")
        cur.execute("TRUNCATE event_learned")
        cur.executemany("INSERT INTO event_profiles (profile, sensor_id, hour, multiplier) VALUES (%s,%s,%s,%s)", street_prof)
        cur.executemany("INSERT INTO event_store_profiles (profile, store_id, hour, multiplier) VALUES (%s,%s,%s,%s)", store_prof)
        cur.executemany("""INSERT INTO event_learned (series, name, category, occurrences, last_start, sensor_ids,
                                                      street_peak, street_mean, store_mean, detail)
                           VALUES (%s,%s,%s,%s,%s,%s,%s,%s,%s,%s)""", rows)
    return len(rows)


def next_occurrence(start: pd.Timestamp, end: pd.Timestamp, now: pd.Timestamp):
    """First yearly repeat (same weekday) that starts after now."""
    while start <= now:
        start, end = start + YEAR_STEP, end + YEAR_STEP
    return start, end


def project(con, now: pd.Timestamp) -> list[str]:
    """Add the next occurrence of every recurring event series that has none planned yet. Returns the new names."""
    ev = load(con)
    added = []
    for series, g in ev[ev.recurring].groupby("series"):
        everything = ev[ev.series == series]
        last = g.sort_values("start").iloc[-1]   # repeat the recurring occurrences only
        if last.start > now:
            continue  # next one already known
        start, end = next_occurrence(last.start, last["end"], now)
        if start - now > PROJECT_AHEAD or ((everything.start - start).abs() <= MATCH_TOLERANCE).any():
            continue
        name = _YEAR.sub(str(start.year), last["name"]) if _YEAR.search(last["name"]) else f"{last['name']} {start.year}"
        loc = lambda t: t.tz_localize("Europe/Berlin", ambiguous=True, nonexistent="shift_forward").to_pydatetime()
        con.execute("""INSERT INTO events (name, category, start_ts, end_ts, sensor_ids, size, recurring, source)
                       VALUES (%s, %s, %s, %s, %s, %s, true, 'projected')""",
                    (name, last.category, loc(start), loc(end), list(last.sensor_ids), last["size"]))
        added.append(name)
        log.info("recurring event %s: next occurrence projected for %s", series, start.date())
    return added


def refresh(con, until: pd.Timestamp, hist, entries, stores):
    """Learn from past events and project recurring ones. Never fails the forecast."""
    try:
        n = learn(con, until, hist, entries, stores)
        added = project(con, until)
        _next_occurrences(con, until)
        return n, added
    except Exception:
        log.exception("event learning failed; forecasts use the size defaults")
        return 0, []


def _next_occurrences(con, now: pd.Timestamp):
    """Store the next planned occurrence (after now) of every learned series, for the dashboard."""
    ev = load(con)
    fut = ev[ev.start > now].sort_values("start").drop_duplicates("series")
    loc = lambda t: t.tz_localize("Europe/Berlin", ambiguous=True, nonexistent="shift_forward").to_pydatetime()
    with con.cursor() as cur:
        cur.execute("UPDATE event_learned SET next_name = NULL, next_start = NULL")
        cur.executemany("UPDATE event_learned SET next_name = %s, next_start = %s WHERE series = %s",
                        [(r["name"], loc(r.start), r.series) for _, r in fut.iterrows()])


def _learned(con):
    street = _q(con, "SELECT profile, sensor_id, hour, multiplier FROM event_profiles")
    store = _q(con, "SELECT profile, store_id, hour, multiplier FROM event_store_profiles")
    summary = _q(con, "SELECT series, name, category, street_mean, store_mean FROM event_learned")
    return street, store, summary


def apply(con, df: pd.DataFrame, extra_events: list[dict] | None = None) -> pd.DataFrame:
    """Adds event_mult, event_name, event_profile, event_basis and event_based_on to a forecast frame
    (columns sensor_id, hour)."""
    ev = load(con)
    try:
        street, _, summary = _learned(con)
    except Exception:  # tables not migrated yet
        street, summary = pd.DataFrame(columns=["profile", "sensor_id", "hour", "multiplier"]), pd.DataFrame()
    learned_keys = [p[len("learned:"):] for p in set(street.profile)] if len(street) else []

    def match(name: str) -> str:
        k = series_key(name)
        return next((c for c in learned_keys if same_series(k, c)), k)

    rows = ev.to_dict("records") + [dict(e, series=match(e.get("name", "")), category=e.get("category", "event"))
                                    for e in (extra_events or [])]
    pm = {(r.profile, r.sensor_id, int(r.hour)): float(r.multiplier) for r in street.itertuples()}
    hour_mean = street.groupby(["profile", "hour"]).multiplier.mean().to_dict() if len(street) else {}
    names = set(street.profile) if len(street) else set()
    by_series = {r.series: r for r in summary.itertuples()} if len(summary) else {}
    cat_mean = (summary[~summary.category.isin(GENERIC_CATEGORIES)].groupby("category").street_mean.mean().to_dict()
                if len(summary) else {})

    df["event_mult"], df["event_name"], df["event_profile"] = 1.0, None, None
    df["event_basis"], df["event_based_on"] = None, None
    for e in rows:
        start, end = pd.Timestamp(e["start"]), pd.Timestamp(e["end"])
        mask = (df.hour >= start.floor("h")) & (df.hour < end) & df.sensor_id.isin(e["sensor_ids"])
        if not mask.any():
            continue
        learned = f"learned:{e['series']}"
        profile, basis, based_on = None, None, None
        if e.get("profile") and e["profile"] in names:
            profile, basis = e["profile"], "profile"
        elif learned in names:
            s = by_series.get(e["series"])
            profile, basis, based_on = learned, "learned", s.name if s is not None else None
        if profile:
            mult = [pm.get((profile, s, h.hour), hour_mean.get((profile, h.hour), 1.0))
                    for s, h in zip(df.loc[mask, "sensor_id"], df.loc[mask, "hour"])]
        elif (e.get("category") or "") in cat_mean:
            mult, basis, based_on = cat_mean[e["category"]], "category", f"past {e['category']} events"
        else:
            mult, basis = SIZE_MULT.get(e.get("size") or "M", 2.0), "size"
        df.loc[mask, "event_mult"] = df.loc[mask, "event_mult"] * np.asarray(mult, dtype=float)
        df.loc[mask, "event_name"] = e["name"]
        df.loc[mask, "event_profile"] = profile
        df.loc[mask, "event_basis"] = basis
        df.loc[mask, "event_based_on"] = based_on
    return df


def store_multipliers(con) -> dict:
    """(profile, store_id, hour) -> learned shopper multiplier."""
    try:
        _, store, _ = _learned(con)
    except Exception:
        return {}
    return {(r.profile, r.store_id, int(r.hour)): float(r.multiplier) for r in store.itertuples()}
