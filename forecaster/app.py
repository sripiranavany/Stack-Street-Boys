"""Ebb forecaster service (FastAPI).

POST /predict   {as_of}                     -> stores a forecast run (7 days, all sensors), returns run id
POST /whatif    {sensor_id, as_of, scenario, event_size?, event_start?, event_end?} -> baseline + scenario (not stored)
POST /train     {cutoff?}                   -> retrain production model (used by the live-mode CronJob)
GET  /model                                 -> model info + evaluation metrics
GET  /healthz, /readyz
"""
import json, logging, os, threading
from datetime import datetime

import pandas as pd
import psycopg
from fastapi import FastAPI, HTTPException
from pydantic import BaseModel

import ebbmodel as em

logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(message)s")
log = logging.getLogger("forecaster")
def _dsn() -> str:
    """Same settings as the Go service (ConfigMap app-config + Secret db-credentials), or DATABASE_URL."""
    if os.environ.get("DATABASE_URL"):
        return os.environ["DATABASE_URL"]
    from urllib.parse import quote
    return "postgresql://{u}:{p}@{h}:{port}/{db}".format(
        u=os.environ.get("POSTGRES_USER", "app"), p=quote(os.environ.get("POSTGRES_PASSWORD", ""), safe=""),
        h=os.environ.get("DB_HOST", "localhost"), port=os.environ.get("DB_PORT", "5432"), db=os.environ.get("POSTGRES_DB", "energy"))


DSN = _dsn()

app = FastAPI(title="Ebb forecaster")
STATE = {"model": None, "eval": None, "error": None}
LOCK = threading.Lock()


def conn():
    # Ebb's tables live in the "ebb" schema; City Pulse tables stay in "public".
    return psycopg.connect(DSN, autocommit=True, options="-c search_path=ebb,public")


def publish_to_city_pulse(con, df, model_version):
    """Mirror the latest forecast into City Pulse's public.forecasts table, so its forecast views show it."""
    try:
        with con.cursor() as cur:
            cur.executemany(
                """INSERT INTO public.forecasts (sensor_id, time, yhat, yhat_lower, yhat_upper, model, created_at)
                   VALUES (%s, %s, %s, %s, %s, %s, now())
                   ON CONFLICT (sensor_id, time) DO UPDATE SET yhat = EXCLUDED.yhat, yhat_lower = EXCLUDED.yhat_lower,
                     yhat_upper = EXCLUDED.yhat_upper, model = EXCLUDED.model, created_at = now()""",
                [(r.sensor_id, r.hour.tz_localize(em.TZ, ambiguous=True, nonexistent="shift_forward").to_pydatetime(),
                  float(r.p50), float(r.p10), float(r.p90), model_version) for r in df.itertuples()])
    except psycopg.Error as e:  # City Pulse tables missing (e.g. local run): Ebb works on its own
        log.warning("could not publish to public.forecasts: %s", e)


def local(ts: str | datetime) -> pd.Timestamp:
    t = pd.Timestamp(ts)
    if t.tzinfo is None:
        return t
    return t.tz_convert(em.TZ).tz_localize(None)


def cutoff_from_db(con) -> pd.Timestamp:
    row = con.execute("SELECT value FROM app_state WHERE key='train_cutoff'").fetchone()
    mode = con.execute("SELECT value FROM app_state WHERE key='mode'").fetchone()
    if mode and mode[0] == "live":
        return pd.Timestamp.now(tz=em.TZ).tz_localize(None).floor("h")
    return local(row[0]) if row else pd.Timestamp("2026-10-31")


def train(cutoff: pd.Timestamp | None = None):
    with LOCK, conn() as con:
        cutoff = cutoff or cutoff_from_db(con)
        log.info("training production model, cutoff %s", cutoff)
        STATE["model"] = em.fit(con, cutoff, f"prod-{cutoff:%Y-%m-%dT%H}")
        log.info("model ready: %s", STATE["model"].version)


def evaluate():
    with conn() as con:
        res, _, _ = em.evaluate(con)
        STATE["eval"] = res
        rows = [(res["model_version"], k, "all", float(v)) for k, v in res.items() if isinstance(v, (int, float))]
        rows += [(res["model_version"], "wape_hourly_ebb", s, float(v)) for s, v in res["per_sensor"].items()]
        with con.cursor() as cur:
            cur.executemany("""INSERT INTO model_metrics VALUES (%s,%s,%s,%s)
                               ON CONFLICT (model_version, metric, scope) DO UPDATE SET value = EXCLUDED.value""", rows)
        log.info("evaluation stored: hourly %.1f%% daily %.1f%%", res["wape_hourly_ebb"], res["wape_daily_ebb"])


def publish_latest():
    """City Pulse view: a real 7-day forecast after the newest reading, from a model trained on ALL data.
    (Separate from the replay runs, which only use data up to the replay clock.)"""
    with conn() as con:
        newest = con.execute("SELECT max(ts) FROM readings WHERE NOT is_estimated").fetchone()[0]
        if newest is None:
            return
        cutoff = local(newest).ceil("h")
        m = em.fit(con, cutoff, f"latest-{cutoff:%Y-%m-%dT%H}")
        df = em.forecast(con, m, cutoff, 168, with_reasons=False)
        publish_to_city_pulse(con, df, m.version)
        log.info("published 7-day forecast after %s to public.forecasts (%d rows)", cutoff, len(df))


def startup_job():
    # Retry until the database is seeded (in Kubernetes the seed Job may finish after this pod starts).
    import time
    for attempt in range(1, 121):
        try:
            train()
            STATE["error"] = None
            break
        except Exception as e:  # keep serving health; readiness stays false until trained
            STATE["error"] = repr(e)
            log.warning("training attempt %d failed (%s); retrying in 30 s", attempt, e)
            time.sleep(30)
    else:
        return
    for job in (publish_latest, evaluate):
        try:
            job()
        except Exception:
            log.exception("%s failed", job.__name__)


@app.on_event("startup")
def on_startup():
    threading.Thread(target=startup_job, daemon=True).start()


@app.get("/healthz")
def healthz():
    return {"ok": True}


@app.get("/readyz")
def readyz():
    if STATE["model"] is None:
        raise HTTPException(503, STATE["error"] or "model training")
    return {"ok": True, "model": STATE["model"].version}


class PredictReq(BaseModel):
    as_of: str
    hours: int = 168


@app.post("/predict")
def predict(req: PredictReq):
    m = STATE["model"]
    if m is None:
        raise HTTPException(503, "model not ready")
    as_of_local = local(req.as_of)
    with conn() as con:
        df = em.forecast(con, m, as_of_local, req.hours)
        aware = pd.Timestamp(req.as_of)
        aware = aware if aware.tzinfo else aware.tz_localize(em.TZ)
        run_id = em.store_run(con, m, aware.to_pydatetime(), df)
    return {"run_id": run_id, "rows": len(df), "model": m.version}


class WhatIfReq(BaseModel):
    sensor_id: str
    as_of: str
    scenario: str = "none"          # none | sunny | rain | cold | hot
    event_size: str | None = None   # S | M | L
    event_start: str | None = None
    event_end: str | None = None
    hours: int = 168


SCENARIOS = {
    "sunny": {"precip": 0.0, "cloud": 5.0, "sun": 3600.0},
    "rain": {"precip": 2.0, "cloud": 100.0, "sun": 0.0},
    "cold": {"temp": 0.0},
    "hot": {"temp": 28.0, "precip": 0.0, "cloud": 10.0, "sun": 3600.0},
}


@app.post("/whatif")
def whatif(req: WhatIfReq):
    m = STATE["model"]
    if m is None:
        raise HTTPException(503, "model not ready")
    as_of = local(req.as_of)
    extra = None
    if req.event_size and req.event_start and req.event_end:
        extra = [{"name": "What-if event", "start": local(req.event_start), "end": local(req.event_end),
                  "sensor_ids": [req.sensor_id], "size": req.event_size, "profile": None}]
    with conn() as con:
        base = em.forecast(con, m, as_of, req.hours, with_reasons=False)
        scen = em.forecast(con, m, as_of, req.hours, SCENARIOS.get(req.scenario), extra, with_reasons=False)
    b = base[base.sensor_id == req.sensor_id]
    s = scen[scen.sensor_id == req.sensor_id]
    return {"points": [{"ts": h.tz_localize(em.TZ, ambiguous=True, nonexistent="shift_forward").isoformat(),
                        "baseline": round(float(x), 2), "scenario": round(float(y), 2)}
                       for h, x, y in zip(b.hour, b.p50, s.p50)]}


class TrainReq(BaseModel):
    cutoff: str | None = None


@app.post("/train")
def retrain(req: TrainReq):
    train(local(req.cutoff) if req.cutoff else None)
    return {"model": STATE["model"].version}


@app.get("/model")
def model_info():
    m = STATE["model"]
    return {"model": m.version if m else None, "cutoff": str(m.cutoff) if m else None,
            "features": em.LAG_FEATS, "algorithm": "HistGradientBoostingRegressor (p10/p50/p90)",
            "evaluation": STATE["eval"], "error": STATE["error"]}
