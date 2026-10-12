"""Janelas de atividade e a comparação com a taxa recente.

Os arrays não carregam organização, sessão nem usuário: só contagens.
"""

from __future__ import annotations

import numpy as np

WINDOW = 30
METRICS = 5
HORIZONS = (5, 10)
OUTPUTS = len(HORIZONS) * METRICS
RECENT_MINUTES = 5
# A inferência local publica a partir de 2 minutos e completa o resto com zero.
MIN_OBSERVED_MINUTES = 2
METRIC_NAMES = ("messages", "likes", "shares", "gift_units", "gift_diamonds")


def should_export(model_mae: float, baseline_mae: float) -> bool:
    """O arquivo de produção só muda quando o modelo erra menos que a taxa recente."""
    if not np.isfinite(model_mae) or not np.isfinite(baseline_mae):
        return False
    return float(model_mae) < float(baseline_mae)


def persistence_forecast(window: np.ndarray, horizons: tuple[int, ...] = HORIZONS) -> np.ndarray:
    """Repete a média dos últimos minutos ao longo de cada horizonte."""
    recent = np.asarray(window, dtype=np.float64)[-RECENT_MINUTES:]
    rate = recent.mean(axis=0)
    return np.concatenate([rate * horizon for horizon in horizons])


def log_persistence_forecast(window: np.ndarray, horizons: tuple[int, ...] = HORIZONS) -> np.ndarray:
    """A base residual: log1p da taxa recente, na mesma ordem da saída da rede."""
    base = np.clip(persistence_forecast(window, horizons), 0, None)
    return np.log1p(base)


def align_window(points: dict[int, np.ndarray], end_minute: int, window: int = WINDOW) -> np.ndarray:
    """Monta (window, 5) terminando em end_minute (epoch, minuto fechado). Buracos viram zero."""
    out = np.zeros((window, METRICS), dtype=np.float32)
    start = end_minute - (window - 1) * 60
    for index, minute in enumerate(range(start, end_minute + 60, 60)):
        row = points.get(minute)
        if row is not None:
            out[index] = np.asarray(row, dtype=np.float32)
    return out


def grid_from_buckets(buckets: np.ndarray, values: np.ndarray) -> np.ndarray:
    """Coloca contagens numa grade de minutos. O minuto sem linha vira zero."""
    buckets = np.asarray(buckets, dtype=np.int64)
    values = np.asarray(values, dtype=np.float64)
    if len(buckets) == 0:
        return np.zeros((0, METRICS), dtype=np.float64)
    order = np.argsort(buckets, kind="stable")
    buckets = buckets[order]
    values = values[order]
    start = int(buckets[0])
    steps = (int(buckets[-1]) - start) // 60 + 1
    out = np.zeros((steps, METRICS), dtype=np.float64)
    for bucket, row in zip(buckets, values):
        out[(int(bucket) - start) // 60] = row
    return out


def windows_from_series(series: np.ndarray) -> tuple[np.ndarray, np.ndarray]:
    """Recorta uma live (T, 5) em janelas de 30 minutos e nas somas de 5 e 10.

    Minutos iniciais, antes da janela encher, entram com zero à esquerda,
    como a inferência faz no começo da live.
    """
    series = np.asarray(series, dtype=np.float64)
    inputs = []
    targets = []
    last = len(series) - HORIZONS[-1]
    for start in range(MIN_OBSERVED_MINUTES, last + 1):
        observed = series[max(0, start - WINDOW):start]
        if len(observed) < WINDOW:
            pad = np.zeros((WINDOW - len(observed), METRICS), dtype=np.float64)
            observed = np.concatenate([pad, observed], axis=0)
        inputs.append(observed)
        chunks = [series[start:start + horizon].sum(axis=0) for horizon in HORIZONS]
        targets.append(np.concatenate(chunks))
    if not inputs:
        return (
            np.zeros((0, WINDOW, METRICS), dtype=np.float32),
            np.zeros((0, OUTPUTS), dtype=np.float32),
        )
    return np.stack(inputs).astype(np.float32), np.stack(targets).astype(np.float32)


def mae(predicted: np.ndarray, target: np.ndarray) -> float:
    predicted = np.asarray(predicted, dtype=np.float64)
    target = np.asarray(target, dtype=np.float64)
    if predicted.size == 0 or predicted.shape != target.shape:
        return float("inf")
    return float(np.mean(np.abs(predicted - target)))


def log_mae(predicted: np.ndarray, target: np.ndarray) -> float:
    """Erro em log1p. Cada métrica pesa igual, nos dois horizontes."""
    predicted = np.asarray(predicted, dtype=np.float64)
    target = np.asarray(target, dtype=np.float64)
    if predicted.size == 0 or predicted.shape != target.shape or predicted.shape[-1] != OUTPUTS:
        return float("inf")
    diff = np.abs(
        np.log1p(np.clip(predicted, 0, None)) - np.log1p(np.clip(target, 0, None))
    )
    per_metric = diff.reshape(diff.shape[0], len(HORIZONS), METRICS).mean(axis=(0, 1))
    score = float(per_metric.mean())
    if not np.isfinite(score):
        return float("inf")
    return score


def log_mae_by_live(predicted: np.ndarray, target: np.ndarray, groups: np.ndarray) -> float:
    """Média do log_mae de cada live, para uma live longa não decidir sozinha."""
    predicted = np.asarray(predicted)
    target = np.asarray(target)
    groups = np.asarray(groups)
    if len(groups) != len(predicted):
        return float("inf")
    scores = [
        log_mae(predicted[groups == live], target[groups == live])
        for live in np.unique(groups)
    ]
    if not scores or not np.isfinite(scores).all():
        return float("inf")
    return float(np.mean(scores))


def synthetic_lives(n_lives: int = 8, length: int = 90, seed: int = 7) -> tuple[np.ndarray, np.ndarray, np.ndarray]:
    """Lives em rampa: a taxa dos últimos minutos subestima os próximos."""
    rng = np.random.default_rng(seed)
    inputs = []
    targets = []
    groups = []
    clock = np.arange(length, dtype=np.float64)
    # A mesma rampa em todas as lives: a média dos últimos minutos fica
    # abaixo da soma futura, e o holdout não muda de escala.
    for live in range(n_lives):
        noise = rng.normal(0, 0.05, size=length)
        messages = np.clip(0.8 * clock + noise + live * 0.01, 0, None)
        likes = messages * 3
        shares = np.full(length, 0.2)
        units = messages * 0.15
        diamonds = units * 8
        series = np.stack([messages, likes, shares, units, diamonds], axis=1)
        window, target = windows_from_series(series)
        if len(window) == 0:
            continue
        inputs.append(window)
        targets.append(target)
        groups.append(np.full(len(window), live, dtype=np.int64))
    return np.concatenate(inputs), np.concatenate(targets), np.concatenate(groups)
