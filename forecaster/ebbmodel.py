"""Ebb forecasting model: features, training, prediction, explanations, evaluation.

All timestamps inside this module are naive Europe/Berlin local hours.
"""
from __future__ import annotations

import logging
import math
from dataclasses import dataclass, field
from datetime import datetime, timedelta

import numpy as np
import pandas as pd
from sklearn.ensemble import HistGradientBoostingRegressor

import events as ev_mod

TZ = "Europe/Berlin"
TRAIN_START = pd.Timestamp("2026-06-12")
EXCLUDED_DAYS = (pd.Timestamp("2026-06-17"), pd.Timestamp("2026-06-21"))  # outage + Lichterfest
BASE_FEATS = ["sid", "hod", "dow", "temp", "precip", "cloud", "sun", "wind", "holiday", "school"]
LAG_FEATS = BASE_FEATS + ["lag7", "dmean7"]
WX = ["temp", "precip", "cloud", "sun", "wind"]
SIZE_MULT = {"S": 1.3, "M": 2.0, "L": 4.0}
# Store customers: door-counter entries per open hour, from street density + calendar + weather + last week's count
STORE_FEATS = ["store", "hod", "dow", "temp", "precip", "cloud", "sun", "wind", "holiday", "school", "eve", "density", "lag7"]
QUANTS = ((0.1, "p10"), (0.5, "p50"), (0.9, "p90"))
log = logging.getLogger("forecaster")


# ---------------------------------------------------------------- data access
def q(con, sql, params=None) -> pd.DataFrame:
    cur = con.execute(sql, params or {})
    cols = [d.name for d in cur.description]
    return pd.DataFrame(cur.fetchall(), columns=cols)


def active_sensors(con) -> list[str]:
    return q(con, "SELECT id FROM sensors WHERE active ORDER BY id").id.tolist()


def load_hourly(con, until: pd.Timestamp | None = None) -> pd.DataFrame:
    sql = """SELECT r.sensor_id, date_trunc('hour', r.ts AT TIME ZONE 'Europe/Berlin') AS hour, avg(r.density) AS value
             FROM readings r JOIN sensors s ON s.id = r.sensor_id
             WHERE s.active AND NOT r.is_estimated {cond}
             GROUP BY 1, 2"""
    if until is not None:
        df = q(con, sql.format(cond="AND r.ts AT TIME ZONE 'Europe/Berlin' <= %(u)s"), {"u": until.to_pydatetime()})
    else:
        df = q(con, sql.format(cond=""))
    df["hour"] = pd.to_datetime(df.hour)
    df["value"] = df.value.astype(float)
    return df


def load_weather(con, kind="actual") -> pd.DataFrame:
    df = q(con, """SELECT ts AT TIME ZONE 'Europe/Berlin' AS hour, lead_days, temp, precip, cloud, sun, wind
                   FROM weather WHERE kind = %(k)s""", {"k": kind})
    df["hour"] = pd.to_datetime(df.hour)
    for c in WX:
        df[c] = df[c].astype(float)
    return df


def load_holidays(con) -> tuple[set, set]:
    df = q(con, "SELECT start_date, end_date, kind FROM holidays")
    pub, school = set(), set()
    for r in df.itertuples():
        for d in pd.date_range(r.start_date, r.end_date):
            (pub if r.kind == "public" else school).add(d.normalize())
    return pub, school


def load_events(con) -> tuple[pd.DataFrame, pd.DataFrame]:
    ev = q(con, """SELECT id, name, start_ts AT TIME ZONE 'Europe/Berlin' AS start, end_ts AT TIME ZONE 'Europe/Berlin' AS "end",
                          sensor_ids, size, profile FROM events""")
    prof = q(con, "SELECT profile, sensor_id, hour, multiplier FROM event_profiles")
    return ev, prof


# ---------------------------------------------------------------- features
def add_calendar(df: pd.DataFrame, pub: set, school: set) -> pd.DataFrame:
    d = df.hour.dt.normalize()
    df["hod"] = df.hour.dt.hour
    df["dow"] = df.hour.dt.dayofweek
    df["holiday"] = d.isin(pub).astype(int)
    df["school"] = d.isin(school).astype(int)
    return df


def add_lags(df: pd.DataFrame, hist: pd.DataFrame) -> pd.DataFrame:
    lag = hist[["sensor_id", "hour", "value"]].copy()
    lag["hour"] = lag.hour + pd.Timedelta(days=7)
    df = df.merge(lag.rename(columns={"value": "lag7"}), on=["sensor_id", "hour"], how="left")
    dm = hist.assign(date=hist.hour.dt.normalize()).groupby(["sensor_id", "date"]).value.mean().rename("dmean7").reset_index()
    dm["date"] = dm.date + pd.Timedelta(days=7)
    df = df.assign(date=df.hour.dt.normalize()).merge(dm, on=["sensor_id", "date"], how="left").drop(columns="date")
    return df


def gbm(loss="squared_error", quantile=None):
    kw = dict(max_iter=400, learning_rate=0.05, categorical_features=[0], random_state=0)
    if loss == "quantile":
        return HistGradientBoostingRegressor(loss="quantile", quantile=quantile, **kw)
    return HistGradientBoostingRegressor(**kw)


@dataclass
class EbbModel:
    version: str
    cutoff: pd.Timestamp
    sensors: list[str]
    models: dict = field(default_factory=dict)     # (feats_name, q) -> fitted model
    climate: pd.DataFrame | None = None            # month x hod typical weather
    stores: list[str] = field(default_factory=list)  # stores with a trained customer model ("store", q) in models

    @property
    def sid(self):
        return {s: i for i, s in enumerate(self.sensors)}


def training_frame(con, cutoff: pd.Timestamp, sensors: list[str], hist=None, wx=None, hol=None):
    hist = load_hourly(con) if hist is None else hist
    wx = load_weather(con, "actual") if wx is None else wx
    pub, school = load_holidays(con) if hol is None else hol
    df = hist[hist.sensor_id.isin(sensors)].copy()
    df = df.merge(wx.drop(columns="lead_days"), on="hour", how="left")
    df = add_calendar(df, pub, school)
    df = add_lags(df, hist)
    df["sid"] = df.sensor_id.map({s: i for i, s in enumerate(sensors)})
    return df


def fit(con, cutoff: pd.Timestamp, version: str, frame: pd.DataFrame | None = None) -> EbbModel:
    sensors = active_sensors(con)
    df = training_frame(con, cutoff, sensors) if frame is None else frame
    excl = (df.hour >= EXCLUDED_DAYS[0]) & (df.hour < EXCLUDED_DAYS[1] + pd.Timedelta(days=1))
    tr = df[(df.hour >= TRAIN_START) & (df.hour < cutoff) & ~excl].dropna(subset=WX)
    # past events are applied as learned multipliers on top, so the base model only sees normal hours
    tr = ev_mod.drop_event_hours(tr, ev_mod.event_hours(ev_mod.load(con), cutoff))
    m = EbbModel(version=version, cutoff=cutoff, sensors=sensors)
    y = np.log1p(tr.value)
    for name, feats, rows in (("lag", LAG_FEATS, tr.dropna(subset=["lag7", "dmean7"])), ("nolag", BASE_FEATS, tr)):
        yy = np.log1p(rows.value)
        m.models[(name, 0.5)] = gbm().fit(rows[feats], yy)
        m.models[(name, 0.1)] = gbm("quantile", 0.1).fit(rows[feats], yy)
        m.models[(name, 0.9)] = gbm("quantile", 0.9).fit(rows[feats], yy)
    wx = load_weather(con, "actual")
    wx = wx[wx.hour < cutoff]
    m.climate = wx.assign(month=wx.hour.dt.month, hod=wx.hour.dt.hour).groupby(["month", "hod"])[WX].mean()
    m.climate["precip"] = 0.0  # "typical" = dry
    try:
        fit_stores(con, m, cutoff)
    except Exception as e:  # no door-counter data (or tables not migrated yet): sensor forecasts still work
        log.warning("store customer model not trained: %s", e)
    return m


def _predict_rows(m: EbbModel, df: pd.DataFrame) -> pd.DataFrame:
    out = pd.DataFrame(index=df.index, columns=["p10", "p50", "p90"], dtype=float)
    has_lag = df[["lag7", "dmean7"]].notna().all(axis=1)
    for name, feats, mask in (("lag", LAG_FEATS, has_lag), ("nolag", BASE_FEATS, ~has_lag)):
        if mask.any():
            X = df.loc[mask, feats]
            for qn, col in ((0.1, "p10"), (0.5, "p50"), (0.9, "p90")):
                out.loc[mask, col] = np.expm1(m.models[(name, qn)].predict(X))
    out = out.clip(lower=0)
    srt = np.sort(out[["p10", "p50", "p90"]].values, axis=1)
    out[["p10", "p50", "p90"]] = srt
    return out


def _p50(m: EbbModel, df: pd.DataFrame) -> np.ndarray:
    has_lag = df[["lag7", "dmean7"]].notna().all(axis=1)
    res = np.zeros(len(df))
    for name, feats, mask in (("lag", LAG_FEATS, has_lag), ("nolag", BASE_FEATS, ~has_lag)):
        if mask.any():
            res[mask.values] = np.expm1(m.models[(name, 0.5)].predict(df.loc[mask, feats]))
    return np.clip(res, 0, None)


# ---------------------------------------------------------------- prediction
def horizon_frame(con, m: EbbModel, as_of: pd.Timestamp, hours: int = 168, weather_override: dict | None = None):
    """Feature frame for the next `hours` hours after as_of, using only information available at as_of."""
    start = as_of.floor("h") + pd.Timedelta(hours=1)
    targets = pd.date_range(start, periods=hours, freq="h")
    df = pd.DataFrame([(s, t) for s in m.sensors for t in targets], columns=["sensor_id", "hour"])
    hist = load_hourly(con, until=as_of)
    df = add_lags(df, hist)
    # weather as forecast at as_of: lead = days between issue and target
    fc = load_weather(con, "forecast")
    act = load_weather(con, "actual")
    lead = np.clip(np.ceil((df.hour - as_of).dt.total_seconds() / 86400).astype(int), 1, 7)
    df["lead_days"] = lead
    df = df.merge(fc, on=["hour", "lead_days"], how="left")
    miss = df.temp.isna()
    if miss.any():  # fall back to actual weather (live mode before forecasts exist), then climatology
        a = act.drop(columns="lead_days").set_index("hour")
        for c in WX:
            df.loc[miss, c] = df.loc[miss, "hour"].map(a[c])
    miss = df.temp.isna()
    if miss.any():
        clim = m.climate
        for c in WX:
            df.loc[miss, c] = [clim.loc[(h.month, h.hour), c] for h in df.loc[miss, "hour"]]
    if weather_override:
        for c, v in weather_override.items():
            df[c] = v
    pub, school = load_holidays(con)
    df = add_calendar(df, pub, school)
    df["sid"] = df.sensor_id.map(m.sid)
    return df


def refresh_events(con, as_of: pd.Timestamp):
    """Learn event effects from history up to as_of and project recurring events (see events.py)."""
    stores = load_stores(con)
    try:
        entries = load_entries(con, until=as_of)
    except Exception:  # no door-counter table yet
        entries = pd.DataFrame(columns=["store_id", "hour", "entries"])
    return ev_mod.refresh(con, as_of, load_hourly(con, until=as_of), entries, stores[["store_id", "sensor_id"]])


def apply_events(con, df: pd.DataFrame, extra_events: list[dict] | None = None) -> pd.DataFrame:
    """Event multipliers: explicit profile, else learned from past occurrences, else category, else size (events.py)."""
    return ev_mod.apply(con, df, extra_events)


def _apply_events_by_size(con, df: pd.DataFrame, extra_events: list[dict] | None = None) -> pd.DataFrame:
    """Previous behaviour (explicit profile or fixed size multiplier), kept for comparison."""
    ev, prof = load_events(con)
    rows = ev.to_dict("records") + (extra_events or [])
    df["event_mult"] = 1.0
    df["event_name"] = None
    pm = {(r.profile, r.sensor_id, int(r.hour)): float(r.multiplier) for r in prof.itertuples()}
    for e in rows:
        start, end = pd.Timestamp(e["start"]), pd.Timestamp(e["end"])
        mask = (df.hour >= start) & (df.hour < end) & df.sensor_id.isin(e["sensor_ids"])
        if not mask.any():
            continue
        if e.get("profile"):
            mult = [pm.get((e["profile"], s, h.hour), 1.0) for s, h in zip(df.loc[mask, "sensor_id"], df.loc[mask, "hour"])]
        else:
            mult = SIZE_MULT.get(e.get("size") or "M", 2.0)
        df.loc[mask, "event_mult"] = df.loc[mask, "event_mult"] * mult
        df.loc[mask, "event_name"] = e["name"]
    return df


def explain(m: EbbModel, df: pd.DataFrame, base_p50: np.ndarray) -> list[dict]:
    """Counterfactual effects with the same model: weather vs typical-dry, holiday, school holiday."""
    eff = {}
    cf = df.copy()
    month = cf.hour.dt.month.values; hod = cf.hour.dt.hour.values
    clim = m.climate
    for c in WX:
        cf[c] = [clim.loc[(mo, h), c] if (mo, h) in clim.index else df[c].iloc[i] for i, (mo, h) in enumerate(zip(month, hod))]
    eff["weather"] = base_p50 / np.maximum(_p50(m, cf), 0.05) - 1
    for flag in ("holiday", "school"):
        e = np.zeros(len(df))
        on = df[flag].values == 1
        if on.any():
            cf2 = df.copy(); cf2[flag] = 0
            e = base_p50 / np.maximum(_p50(m, cf2), 0.05) - 1
            e[~on] = 0
        eff[flag] = e
    out = []
    for i in range(len(df)):
        r = {k: round(float(v[i]), 3) for k, v in eff.items()}
        r["weather_desc"] = f"{df.temp.iloc[i]:.0f}°C, {df.precip.iloc[i]:.1f} mm rain, {df.cloud.iloc[i]:.0f}% cloud"
        r["temp"] = round(float(df.temp.iloc[i]), 1)
        r["precip"] = round(float(df.precip.iloc[i]), 2)
        r["cloud"] = round(float(df.cloud.iloc[i]), 0)
        if df.event_mult.iloc[i] != 1.0:
            r["event"] = {"name": df.event_name.iloc[i], "mult": round(float(df.event_mult.iloc[i]), 2)}
            if "event_basis" in df and df.event_basis.iloc[i]:
                r["event"]["basis"] = df.event_basis.iloc[i]          # profile | learned | category | size
                if df.event_based_on.iloc[i]:
                    r["event"]["based_on"] = df.event_based_on.iloc[i]
        out.append(r)
    return out


def forecast(con, m: EbbModel, as_of: pd.Timestamp, hours=168, weather_override=None, extra_events=None, with_reasons=True):
    df = horizon_frame(con, m, as_of, hours, weather_override)
    pred = _predict_rows(m, df)
    df = apply_events(con, df, extra_events)
    for c in ("p10", "p50", "p90"):
        df[c] = pred[c].values * df.event_mult.values
    if with_reasons:
        df["reasons"] = explain(m, df, pred.p50.values.astype(float))
    return df


def store_run(con, m: EbbModel, as_of_aware: datetime, df: pd.DataFrame) -> int:
    import json
    run_id = con.execute("INSERT INTO forecast_runs (as_of, model_version) VALUES (%s, %s) RETURNING id",
                         (as_of_aware, m.version)).fetchone()[0]
    with con.cursor() as cur, cur.copy("COPY forecasts (run_id, sensor_id, ts, p50, p10, p90, reasons) FROM STDIN") as cp:
        for r in df.itertuples():
            ts = r.hour.tz_localize(TZ, ambiguous=True, nonexistent="shift_forward")
            cp.write_row((run_id, r.sensor_id, ts.to_pydatetime(), float(r.p50), float(r.p10), float(r.p90),
                          json.dumps(r.reasons)))
    return run_id


# ---------------------------------------------------------------- store customers
def load_stores(con) -> pd.DataFrame:
    return q(con, "SELECT id AS store_id, sensor_id, config FROM stores ORDER BY id")


def load_entries(con, until: pd.Timestamp | None = None) -> pd.DataFrame:
    """Door-counter entries for open hours, as naive Europe/Berlin hours."""
    sql = """SELECT store_id, ts AT TIME ZONE 'Europe/Berlin' AS hour, entries
             FROM store_entries WHERE is_open {cond}"""
    if until is not None:
        df = q(con, sql.format(cond="AND ts AT TIME ZONE 'Europe/Berlin' < %(u)s"), {"u": until.to_pydatetime()})
    else:
        df = q(con, sql.format(cond=""))
    df["hour"] = pd.to_datetime(df.hour)
    df["entries"] = df.entries.astype(float)
    return df


def store_features(df: pd.DataFrame, entries: pd.DataFrame, pub: set, school: set, store_ids: list[str]) -> pd.DataFrame:
    """df has store_id, hour, density and the weather columns; adds calendar, eve-of-holiday and last week's count."""
    df = add_calendar(df, pub, school)
    df["eve"] = (df.hour.dt.normalize() + pd.Timedelta(days=1)).isin(pub).astype(int)
    lag = entries[["store_id", "hour", "entries"]].rename(columns={"entries": "lag7"})
    lag["hour"] = lag.hour + pd.Timedelta(days=7)
    df = df.merge(lag, on=["store_id", "hour"], how="left")
    df["store"] = df.store_id.map({s: i for i, s in enumerate(store_ids)})
    return df


def store_training_frame(con, stores: pd.DataFrame, hist: pd.DataFrame | None = None, density: pd.DataFrame | None = None):
    """One row per store x counted open hour. density defaults to the measured street density (NaN while the sensor is offline)."""
    entries = load_entries(con)
    if density is None:
        hist = load_hourly(con) if hist is None else hist
        density = hist.rename(columns={"value": "density"})
    df = entries.merge(stores[["store_id", "sensor_id"]], on="store_id")
    df = df.merge(density[["sensor_id", "hour", "density"]], on=["sensor_id", "hour"], how="left")
    df = df.merge(load_weather(con, "actual").drop(columns="lead_days"), on="hour", how="left")
    pub, school = load_holidays(con)
    return store_features(df, entries, pub, school, stores.store_id.tolist())


def fit_stores(con, m: EbbModel, cutoff: pd.Timestamp):
    stores = load_stores(con)
    if stores.empty:
        return
    df = store_training_frame(con, stores)
    tr = df[(df.hour >= TRAIN_START) & (df.hour < cutoff)].dropna(subset=WX)
    tr = ev_mod.drop_event_hours(tr, ev_mod.event_hours(ev_mod.load(con), cutoff))  # events: learned multipliers instead
    if len(tr) < 500:
        log.warning("store customer model not trained: only %d counted hours before %s", len(tr), cutoff)
        return
    y = np.log1p(tr.entries)
    for qn, _ in QUANTS:
        m.models[("store", qn)] = (gbm() if qn == 0.5 else gbm("quantile", qn)).fit(tr[STORE_FEATS], y)
    m.stores = stores.store_id.tolist()
    log.info("store customer model trained on %d counted hours (%s)", len(tr), ", ".join(m.stores))


def _predict_stores(m: EbbModel, df: pd.DataFrame) -> pd.DataFrame:
    out = pd.DataFrame({col: np.expm1(m.models[("store", qn)].predict(df[STORE_FEATS])) for qn, col in QUANTS}, index=df.index)
    out = out.clip(lower=0)
    out[["p10", "p50", "p90"]] = np.sort(out[["p10", "p50", "p90"]].values, axis=1)
    return out


def forecast_stores(con, m: EbbModel, as_of: pd.Timestamp, fc: pd.DataFrame) -> pd.DataFrame:
    """Customers per open hour for every store, from the street forecast `fc` (output of forecast())."""
    cols = ["store_id", "hour", "p10", "p50", "p90"]
    if not m.stores:
        return pd.DataFrame(columns=cols)
    stores = load_stores(con)
    stores = stores[stores.store_id.isin(m.stores)]
    pub, school = load_holidays(con)
    parts = []
    for st in stores.itertuples():
        use = ["hour"] + WX + ["p50"] + [c for c in ("event_mult", "event_profile") if c in fc]
        d = fc.loc[fc.sensor_id == st.sensor_id, use].rename(columns={"p50": "density"})
        cfg = st.config
        open_ = (d.hour.dt.dayofweek.isin(cfg.get("open_days", [])) & (d.hour.dt.hour >= cfg.get("open_from", 0))
                 & (d.hour.dt.hour < cfg.get("open_to", 24)) & ~d.hour.dt.normalize().isin(pub))
        parts.append(d[open_].assign(store_id=st.store_id))
    df = pd.concat(parts, ignore_index=True) if parts else pd.DataFrame()
    if df.empty:
        return pd.DataFrame(columns=cols)
    df = store_features(df, load_entries(con, until=as_of), pub, school, m.stores)
    pred = _predict_stores(m, df)
    # Event hours with a learned shopper effect: predict from the street level WITHOUT the event, then apply what
    # the door counters showed in past occurrences (e.g. Lichterfest: crowds outside, hardly more shoppers).
    smult = ev_mod.store_multipliers(con)
    if smult and "event_profile" in df:
        mult = np.array([smult.get((p, s, h.hour), np.nan) if p else np.nan
                         for p, s, h in zip(df.event_profile, df.store_id, df.hour)])
        has = ~np.isnan(mult)
        if has.any():
            base = df.loc[has].copy()
            base["density"] = base.density / base.event_mult.astype(float)
            bp = _predict_stores(m, base)
            pred.loc[has, ["p10", "p50", "p90"]] = bp[["p10", "p50", "p90"]].values * mult[has][:, None]
    return pd.concat([df[["store_id", "hour"]], pred], axis=1)[cols]


def store_store_run(con, run_id: int, df: pd.DataFrame):
    with con.cursor() as cur, cur.copy("COPY store_forecasts (run_id, store_id, ts, p50, p10, p90) FROM STDIN") as cp:
        for r in df.itertuples():
            ts = r.hour.tz_localize(TZ, ambiguous=True, nonexistent="shift_forward")
            cp.write_row((run_id, r.store_id, ts.to_pydatetime(), float(r.p50), float(r.p10), float(r.p90)))


def evaluate_stores(con, m: EbbModel, te: pd.DataFrame, cutoff: pd.Timestamp, test_end: pd.Timestamp) -> dict:
    """Held-out store hours, fed with the street model's own predicted density (as in production).
    Baselines: the fixed capture-rate formula on the same density, and the typical count for that weekday and hour."""
    if not m.stores:
        return {}
    stores = load_stores(con)
    density = te[["sensor_id", "hour", "p50"]].rename(columns={"p50": "density"})
    df = store_training_frame(con, stores, density=density)
    typ = df[(df.hour >= TRAIN_START) & (df.hour < cutoff)].groupby(["store_id", "dow", "hod"]).entries.median().rename("typical")
    t = df[(df.hour >= cutoff) & (df.hour < test_end)].dropna(subset=WX + ["density"]).join(typ, on=["store_id", "dow", "hod"])
    t = t.dropna(subset=["typical"])
    if t.empty:
        return {}
    cfg = {r.store_id: r.config for r in stores.itertuples()}
    t["model"] = _predict_stores(m, t).p50.values
    t["formula"] = [d * 3600 / max(cfg[s].get("dwell_seconds", 30), 1) * cfg[s].get("capture_rate", 0.03)
                    for s, d in zip(t.store_id, t.density)]
    return {"wape_hourly_store_model": wape(t.entries, t.model), "wape_hourly_store_fixed_rate": wape(t.entries, t.formula),
            "wape_hourly_store_typical_week": wape(t.entries, t.typical), "n_store_test_hours": int(len(t))}


# ---------------------------------------------------------------- evaluation
def wape(a, p):
    a, p = np.asarray(a, float), np.asarray(p, float)
    return float(np.abs(p - a).sum() / max(a.sum(), 1e-9) * 100)


def evaluate(con, cutoff=pd.Timestamp("2026-10-01"), test_end=pd.Timestamp("2026-11-08")) -> dict:
    """Train on data before cutoff, test on [cutoff, test_end) with actual weather (documented in MODEL_CARD)."""
    sensors = active_sensors(con)
    frame = training_frame(con, cutoff, sensors)
    m = fit(con, cutoff, f"eval-{cutoff.date()}", frame)
    te = frame[(frame.hour >= cutoff) & (frame.hour < test_end)].dropna(subset=WX + ["lag7", "dmean7"]).copy()
    pred = _predict_rows(m, te)
    te["p50"], te["p10"], te["p90"] = pred.p50.values, pred.p10.values, pred.p90.values
    trn = frame[(frame.hour >= TRAIN_START) & (frame.hour < cutoff)]
    typ = trn[trn.holiday == 0].groupby(["sensor_id", "dow", "hod"]).value.median().rename("typical")
    te = te.join(typ, on=["sensor_id", "dow", "hod"])
    te["lastweek"] = te.lag7
    day = te[(te.hod >= 8) & (te.hod <= 22)].dropna(subset=["typical"])
    res = {"model_version": m.version, "test_from": str(cutoff.date()), "test_to": str((test_end - pd.Timedelta(days=1)).date())}
    for col, name in (("p50", "ebb"), ("typical", "typical_week"), ("lastweek", "last_week")):
        res[f"wape_hourly_{name}"] = wape(day.value, day[col])
        d = te.dropna(subset=[col]).groupby(["sensor_id", te.hour.dt.date]).agg(a=("value", "sum"), p=(col, "sum"), n=("value", "size"))
        d = d[d.n >= 20]
        res[f"wape_daily_{name}"] = wape(d.a, d.p)
    blk = day.assign(b=day.hod // 3).groupby(["sensor_id", day.hour.dt.date, "b"]).agg(a=("value", "sum"), p=("p50", "sum"))
    res["wape_block3_ebb"] = wape(blk.a, blk.p)
    res["bias_ebb"] = float(day.p50.mean() / day.value.mean())
    res["coverage_p10_p90"] = float(((day.value >= day.p10) & (day.value <= day.p90)).mean() * 100)
    res["per_sensor"] = {s: wape(g.value, g.p50) for s, g in day.groupby("sensor_id")}
    res["n_test_hours"] = int(len(day))
    try:
        res.update(evaluate_stores(con, m, te, cutoff, test_end))
    except Exception as e:
        log.warning("store model evaluation skipped: %s", e)
    return res, te, m
