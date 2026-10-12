"""Testes da janela e do critério de exportação. Não exigem PyTorch."""

import unittest

import numpy as np

import series


class SeriesTest(unittest.TestCase):
    def test_should_export_only_when_strictly_better(self):
        self.assertTrue(series.should_export(1.0, 2.0))
        self.assertFalse(series.should_export(2.0, 1.0))
        self.assertFalse(series.should_export(1.0, 1.0))
        self.assertFalse(series.should_export(float("nan"), 1.0))

    def test_align_window_fills_gaps_with_zero(self):
        end = 1_700_000_040
        points = {
            end: np.array([1, 2, 3, 4, 5], dtype=np.float32),
            end - 120: np.array([9, 0, 0, 0, 0], dtype=np.float32),
        }
        window = series.align_window(points, end, window=4)
        self.assertEqual(window.shape, (4, 5))
        self.assertEqual(int(window[-1, 0]), 1)
        self.assertEqual(int(window[-3, 0]), 9)
        self.assertEqual(int(window[-2].sum()), 0)

    def test_windows_cover_five_and_ten_minute_sums(self):
        series_rows = np.ones((45, 5), dtype=np.float64)
        windows, targets = series.windows_from_series(series_rows)
        self.assertEqual(windows.shape[1:], (30, 5))
        self.assertEqual(targets.shape[1], 10)
        self.assertAlmostEqual(float(targets[0, 0]), 5.0)
        self.assertAlmostEqual(float(targets[0, 5]), 10.0)

    def test_persistence_repeats_recent_rate(self):
        window = np.zeros((30, 5), dtype=np.float64)
        window[-5:] = 2
        forecast = series.persistence_forecast(window)
        self.assertAlmostEqual(float(forecast[0]), 10.0)
        self.assertAlmostEqual(float(forecast[5]), 20.0)

    def test_log_persistence_is_the_residual_base(self):
        window = np.zeros((30, 5), dtype=np.float64)
        window[-5:] = 4
        base = series.persistence_forecast(window)
        logged = series.log_persistence_forecast(window)
        self.assertTrue(np.allclose(logged, np.log1p(base)))

    def test_grid_puts_zero_in_the_missing_minute(self):
        start = 1_700_000_000
        buckets = np.array([start, start + 120])
        values = np.array([[1, 0, 0, 0, 0], [3, 0, 0, 0, 0]], dtype=np.float64)
        grid = series.grid_from_buckets(buckets, values)
        self.assertEqual(grid.shape, (3, 5))
        self.assertEqual(float(grid[0, 0]), 1.0)
        self.assertEqual(float(grid[1].sum()), 0.0)
        self.assertEqual(float(grid[2, 0]), 3.0)

    def test_cold_start_window_pads_the_left(self):
        rows = np.arange(1, 41, dtype=np.float64)[:, None] * np.ones((1, 5))
        windows, targets = series.windows_from_series(rows)
        self.assertEqual(windows.shape[0], 40 - 10 - series.MIN_OBSERVED_MINUTES + 1)
        self.assertEqual(float(windows[0, :-2].sum()), 0.0)
        self.assertEqual(float(windows[0, -2, 0]), 1.0)
        self.assertEqual(float(windows[0, -1, 0]), 2.0)
        self.assertAlmostEqual(float(targets[0, 0]), float(np.arange(3, 8).sum()))

    def test_log_mae_weights_metrics_equally(self):
        target = np.zeros((1, series.OUTPUTS), dtype=np.float64)
        predicted = np.zeros((1, series.OUTPUTS), dtype=np.float64)
        predicted[0, 0] = np.expm1(1.0)
        # Um horizonte de uma métrica: 1 / (2 horizontes) / 5 métricas.
        self.assertAlmostEqual(series.log_mae(predicted, target), 0.1)


if __name__ == "__main__":
    unittest.main()
