#!/usr/bin/env python3
"""Treina a rede de engajamento e só grava o ONNX se ela superar a taxa recente.

Uso:
  python train_engagement.py --synthetic --out /caminho/engagement.onnx
  python train_engagement.py --data windows.npz --out /caminho/engagement.onnx

O npz (de export_windows.py) contém só janelas numéricas, sem organização nem usuário.
"""

from __future__ import annotations

import argparse
import sys
from pathlib import Path

import numpy as np

import series


def load_arrays(path: Path | None, synthetic: bool) -> tuple[np.ndarray, np.ndarray, np.ndarray]:
    if synthetic or path is None:
        return series.synthetic_lives()
    data = np.load(path)
    return data["X"], data["Y"], data["groups"]


def split_by_live(groups: np.ndarray) -> tuple[np.ndarray, np.ndarray]:
    """Segura as últimas lives para a avaliação, sem misturar minutos da mesma live."""
    lives = np.unique(groups)
    if len(lives) < 2:
        raise SystemExit("é preciso pelo menos duas lives para separar treino e avaliação")
    holdout = max(1, len(lives) // 5)
    test_lives = set(lives[-holdout:].tolist())
    test = np.isin(groups, list(test_lives))
    return ~test, test


def train_and_maybe_export(x: np.ndarray, y: np.ndarray, groups: np.ndarray, out: Path, epochs: int) -> int:
    import copy

    import torch
    from torch import nn

    train_idx, test_idx = split_by_live(groups)
    if int(test_idx.sum()) == 0 or int(train_idx.sum()) == 0:
        print("modelo não exportado: divisão de lives vazia")
        return 0

    torch.manual_seed(7)

    class Net(nn.Module):
        def __init__(self) -> None:
            super().__init__()
            width = series.WINDOW * series.METRICS
            self.mlp = nn.Sequential(
                nn.Linear(width, 64),
                nn.ReLU(),
                nn.Linear(64, 32),
                nn.ReLU(),
                nn.Linear(32, series.OUTPUTS),
            )
            # Correção zero no início: a saída nasce igual à taxa recente.
            nn.init.zeros_(self.mlp[-1].weight)
            nn.init.zeros_(self.mlp[-1].bias)

        def forward(self, window: torch.Tensor) -> torch.Tensor:
            counts = torch.clamp(window, min=0)
            recent = counts[:, -series.RECENT_MINUTES:, :]
            rate = recent.mean(dim=1)
            base = torch.cat([rate * horizon for horizon in series.HORIZONS], dim=1)
            flat = torch.log1p(counts).reshape(counts.shape[0], -1)
            return torch.log1p(torch.clamp(base, min=0)) + self.mlp(flat)

    model = Net()
    optimizer = torch.optim.Adam(model.parameters(), lr=1e-2, weight_decay=1e-4)
    loss_fn = nn.MSELoss()
    tx = torch.tensor(x[train_idx])
    ty = torch.log1p(torch.clamp(torch.tensor(y[train_idx]), min=0))
    vx = torch.tensor(x[test_idx])
    vy = y[test_idx]
    vgroups = groups[test_idx]
    best_score = float("inf")
    best_state = copy.deepcopy(model.state_dict())
    stall = 0
    patience = 20
    for _ in range(epochs):
        model.train()
        order = torch.randperm(tx.shape[0])
        for start in range(0, tx.shape[0], 64):
            batch = order[start:start + 64]
            optimizer.zero_grad()
            loss = loss_fn(model(tx[batch]), ty[batch])
            loss.backward()
            nn.utils.clip_grad_norm_(model.parameters(), 1.0)
            optimizer.step()
        model.eval()
        with torch.no_grad():
            holdout = torch.expm1(model(vx)).clamp(min=0).numpy()
        score = series.log_mae_by_live(holdout, vy, vgroups)
        if score < best_score:
            best_score = score
            best_state = copy.deepcopy(model.state_dict())
            stall = 0
        else:
            stall += 1
            if stall >= patience:
                break

    model.load_state_dict(best_state)
    model.eval()
    with torch.no_grad():
        predicted = torch.expm1(model(vx)).clamp(min=0).numpy()
    baseline = np.stack([series.persistence_forecast(window) for window in x[test_idx]])
    model_mae = series.log_mae_by_live(predicted, vy, vgroups)
    baseline_mae = series.log_mae_by_live(baseline, vy, vgroups)
    print(f"log_mae modelo={model_mae:.4f} log_mae taxa recente={baseline_mae:.4f}")
    if not series.should_export(model_mae, baseline_mae):
        print("modelo não exportado: não superou a taxa recente")
        return 0

    out.parent.mkdir(parents=True, exist_ok=True)
    tmp = out.with_suffix(".onnx.part")
    torch.onnx.export(
        model,
        torch.zeros(1, series.WINDOW, series.METRICS),
        str(tmp),
        input_names=["window"],
        output_names=["forecast"],
        dynamo=False,
        opset_version=17,
    )
    tmp.replace(out)
    print(f"modelo gravado em {out}")
    return 0


def main(argv: list[str]) -> int:
    parser = argparse.ArgumentParser(description="Treina a previsão de engajamento e exporta ONNX.")
    parser.add_argument("--data", type=Path, help="npz anonimizado produzido por export_windows.py")
    parser.add_argument("--synthetic", action="store_true", help="treina numa série sintética em rampa")
    parser.add_argument("--out", type=Path, required=True, help="caminho do engagement.onnx")
    parser.add_argument("--epochs", type=int, default=200)
    args = parser.parse_args(argv)
    if args.data is None and not args.synthetic:
        parser.error("informe --data ou --synthetic")
    x, y, groups = load_arrays(args.data, args.synthetic)
    return train_and_maybe_export(x, y, groups, args.out, args.epochs)


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
