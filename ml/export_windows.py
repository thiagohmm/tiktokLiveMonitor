#!/usr/bin/env python3
"""Exporta janelas anonimizadas de live_minute_metrics para o treino.

O arquivo de saída guarda contagens e um índice de live (0, 1, 2…), sem
org_id, live_id nem usuário. Roda fora do processo que captura a live.
"""

from __future__ import annotations

import argparse
import os
import sys
from pathlib import Path

import numpy as np

import series


def load_lives(database_url: str) -> list[np.ndarray]:
    import psycopg

    lives: list[np.ndarray] = []
    current_id = None
    buckets: list[int] = []
    rows: list[list[float]] = []

    def flush() -> None:
        if len(rows) == 0:
            return
        lives.append(series.grid_from_buckets(np.asarray(buckets), np.asarray(rows, dtype=np.float64)))

    with psycopg.connect(database_url) as conn:
        with conn.cursor() as cur:
            cur.execute(
                """
                SELECT live_id,
                       EXTRACT(EPOCH FROM bucket)::bigint,
                       messages, likes, shares, gift_units, gift_diamonds
                FROM live_minute_metrics
                ORDER BY live_id, bucket
                """
            )
            for live_id, bucket, messages, likes, shares, units, diamonds in cur:
                if current_id != live_id:
                    flush()
                    current_id = live_id
                    buckets = []
                    rows = []
                buckets.append(int(bucket))
                rows.append([messages, likes, shares, units, diamonds])
    flush()
    return lives


def build_dataset(lives: list[np.ndarray]) -> tuple[np.ndarray, np.ndarray, np.ndarray]:
    inputs = []
    targets = []
    groups = []
    group = 0
    for live in lives:
        window, target = series.windows_from_series(live)
        if len(window) == 0:
            continue
        inputs.append(window)
        targets.append(target)
        groups.append(np.full(len(window), group, dtype=np.int64))
        group += 1
    if not inputs:
        raise SystemExit("nenhuma live produziu janela: faltam minutos observados e os 10 minutos seguintes")
    return np.concatenate(inputs), np.concatenate(targets), np.concatenate(groups)


def main(argv: list[str]) -> int:
    parser = argparse.ArgumentParser(description="Exporta janelas anonimizadas para o treino.")
    parser.add_argument("--out", type=Path, required=True)
    parser.add_argument("--database-url", default=os.environ.get("DATABASE_URL", ""))
    args = parser.parse_args(argv)
    if not args.database_url:
        raise SystemExit("DATABASE_URL é obrigatório")
    x, y, groups = build_dataset(load_lives(args.database_url))
    args.out.parent.mkdir(parents=True, exist_ok=True)
    np.savez_compressed(args.out, X=x, Y=y, groups=groups)
    print(f"{len(groups)} janelas de {len(np.unique(groups))} lives em {args.out}")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
