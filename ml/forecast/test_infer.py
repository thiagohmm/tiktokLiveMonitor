"""Testes do relógio da inferência, sem banco e sem ONNX."""

import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
sys.path.insert(0, str(Path(__file__).resolve().parent))

import infer  # noqa: E402


class InferClockTest(unittest.TestCase):
    def test_last_completed_minute_is_the_previous_one(self):
        # 12:00:30 UTC -> o minuto fechado anterior é 11:59:00
        now = 1_700_000_000 + 30
        minute = infer.last_completed_minute(now)
        self.assertEqual(minute % 60, 0)
        self.assertLess(minute, now - 30)
        self.assertGreaterEqual(now - minute, 60)


if __name__ == "__main__":
    unittest.main()
