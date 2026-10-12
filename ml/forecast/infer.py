#!/usr/bin/env python3
"""Roda o ONNX nas sessões abertas e grava live_forecasts.

Se o modelo não estiver no volume, ou se o banco falhar, este processo só
registra o erro e espera. A captura da live, a fila de presentes e a fila PIX
seguem no backend.
"""

from __future__ import annotations

import os
import signal
import sys
import time
from pathlib import Path

import numpy as np

import series

POLL_SECONDS = int(os.environ.get("FORECAST_POLL_SECONDS", "60"))
MIN_MINUTES = int(os.environ.get("FORECAST_MIN_MINUTES", "15"))
MODEL_PATH = Path(os.environ.get("FORECAST_MODEL_PATH", "/models/engagement.onnx"))
DATABASE_URL = os.environ.get("DATABASE_URL", "")

stop = False


def request_stop(_signum, _frame) -> None:
    global stop
    stop = True


def last_completed_minute(now: float | None = None) -> int:
    current = int((now if now is not None else time.time()) // 60 * 60)
    return current - 60


def predict(runtime, window: np.ndarray) -> tuple[np.ndarray, np.ndarray]:
    raw = runtime.run(None, {"window": window[np.newaxis, :].astype(np.float32)})[0][0]
    values = np.rint(np.clip(np.expm1(raw), 0, None)).astype(np.int64)
    return values[: series.METRICS], values[series.METRICS:]


def model_version(path: Path) -> str:
    stat = path.stat()
    return f"{path.name}:{int(stat.st_mtime)}:{stat.st_size}"


def load_points(cur, org_id: str, live_id: str, end_minute: int) -> dict[int, np.ndarray]:
    cur.execute(
        """
        SELECT EXTRACT(EPOCH FROM bucket)::bigint,
               messages, likes, shares, gift_units, gift_diamonds
        FROM live_minute_metrics
        WHERE org_id = %s AND live_id = %s AND bucket <= to_timestamp(%s)
        ORDER BY bucket
        """,
        (org_id, live_id, end_minute),
    )
    points: dict[int, np.ndarray] = {}
    for bucket, messages, likes, shares, units, diamonds in cur:
        minute = int(bucket) // 60 * 60
        points[minute] = np.array([messages, likes, shares, units, diamonds], dtype=np.float32)
    return points


def write_forecast(cur, org_id: str, live_id: str, version: str, five: np.ndarray, ten: np.ndarray) -> None:
    for horizon, values in ((5, five), (10, ten)):
        cur.execute(
            """
            INSERT INTO live_forecasts
                (org_id, live_id, horizon_min, messages, likes, shares, gift_units, gift_diamonds, model_version, computed_at)
            VALUES (%s, %s, %s, %s, %s, %s, %s, %s, %s, CURRENT_TIMESTAMP)
            ON CONFLICT (live_id, horizon_min) DO UPDATE SET
                messages = EXCLUDED.messages,
                likes = EXCLUDED.likes,
                shares = EXCLUDED.shares,
                gift_units = EXCLUDED.gift_units,
                gift_diamonds = EXCLUDED.gift_diamonds,
                model_version = EXCLUDED.model_version,
                computed_at = CURRENT_TIMESTAMP
            WHERE live_forecasts.org_id = EXCLUDED.org_id
            """,
            (org_id, live_id, horizon, *[int(v) for v in values], version),
        )


def run_once(conn, runtime, version: str) -> None:
    end_minute = last_completed_minute()
    with conn.cursor() as cur:
        cur.execute(
            """
            SELECT id, org_id FROM live_sessions
            WHERE ended_at IS NULL AND org_id <> ''
            """
        )
        sessions = cur.fetchall()
    for live_id, org_id in sessions:
        if stop:
            return
        wrote = False
        try:
            with conn.transaction():
                with conn.cursor() as cur:
                    points = load_points(cur, org_id, live_id, end_minute)
                    if len(points) >= MIN_MINUTES:
                        window = series.align_window(points, end_minute)
                        five, ten = predict(runtime, window)
                        write_forecast(cur, org_id, live_id, version, five, ten)
                        wrote = True
            if wrote:
                print(f"[forecast] live {live_id}: horizontes gravados")
        except Exception as exc:
            print(f"[forecast] live {live_id}: {exc}", file=sys.stderr)


def open_runtime(path: Path):
    import onnxruntime as ort

    return ort.InferenceSession(str(path), providers=["CPUExecutionProvider"]), model_version(path)


def main() -> int:
    if not DATABASE_URL:
        print("[forecast] DATABASE_URL ausente; aguardando encerramento", file=sys.stderr)
    signal.signal(signal.SIGTERM, request_stop)
    signal.signal(signal.SIGINT, request_stop)
    runtime = None
    version = ""
    loaded_stamp = ""
    conn = None
    while not stop:
        try:
            if not MODEL_PATH.is_file():
                print(f"[forecast] modelo ausente em {MODEL_PATH}", file=sys.stderr)
            else:
                stamp = model_version(MODEL_PATH)
                if runtime is None or stamp != loaded_stamp:
                    runtime, version = open_runtime(MODEL_PATH)
                    loaded_stamp = stamp
                    print(f"[forecast] modelo {version}")
                if conn is None and DATABASE_URL:
                    import psycopg

                    conn = psycopg.connect(DATABASE_URL)
                    # O SELECT inicial abre uma transação. O bloco interno vira
                    # só um savepoint e não a encerra, então o backend nunca
                    # via as linhas. Com autocommit, cada live commita sozinha.
                    conn.autocommit = True
                if conn is not None and runtime is not None:
                    run_once(conn, runtime, version)
        except Exception as exc:
            print(f"[forecast] ciclo ignorado: {exc}", file=sys.stderr)
            if conn is not None:
                conn.close()
                conn = None
        for _ in range(max(POLL_SECONDS, 1)):
            if stop:
                break
            time.sleep(1)
    if conn is not None:
        conn.close()
    return 0


if __name__ == "__main__":
    sys.exit(main())
