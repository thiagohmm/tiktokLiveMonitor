import unittest

from .buffer import MessageBuffer
from .context import ContextBuilder
from . import router


class MessageBufferTest(unittest.TestCase):
    def test_repetition_detection_flags_on_third_occurrence(self):
        buf = MessageBuffer()
        for i in range(3):
            buf._add_message({"uniqueId": "u1", "nickname": "Joao", "comment": "spam", "timestamp": 1000 + i})
        reps = buf.top_repetitions()
        self.assertEqual(1, len(reps))
        self.assertEqual(3, reps[0]["count"])

    def test_repetition_resets_outside_window(self):
        buf = MessageBuffer()
        buf._add_message({"uniqueId": "u1", "nickname": "Joao", "comment": "spam", "timestamp": 1000})
        buf._add_message({"uniqueId": "u1", "nickname": "Joao", "comment": "spam", "timestamp": 1000 + 60_000 + 1})
        buf._add_message({"uniqueId": "u1", "nickname": "Joao", "comment": "spam", "timestamp": 1000 + 120_000 + 2})
        self.assertEqual(0, len(buf.top_repetitions()))

    def test_different_users_do_not_cross_count(self):
        buf = MessageBuffer()
        for i in range(3):
            buf._add_message({"uniqueId": f"u{i}", "nickname": f"N{i}", "comment": "oi", "timestamp": 1000 + i})
        self.assertEqual(0, len(buf.top_repetitions()))

    def test_gift_aggregation(self):
        buf = MessageBuffer()
        buf._add_gift({"uniqueId": "u1", "nickname": "Joao", "giftName": "Rosa", "repeatCount": 2})
        buf._add_gift({"uniqueId": "u1", "nickname": "Joao", "giftName": "Rosa", "repeatCount": 3})
        gifts = buf.top_gifts()
        self.assertEqual(1, len(gifts))
        self.assertEqual(5, gifts[0]["count"])

    def test_context_text_includes_repetitions(self):
        buf = MessageBuffer()
        for i in range(3):
            buf._add_message({"uniqueId": "u1", "nickname": "Joao", "comment": "spam", "timestamp": 1000 + i})
        text = ContextBuilder(buf).build()
        self.assertIn("Repetições", text)
        self.assertIn("spam", text)


class RouterTest(unittest.TestCase):
    def test_deterministic_ranking(self):
        tool, _arg = router._deterministic_route("qual o ranking da live?")
        self.assertEqual("ranking", tool)

    def test_deterministic_profile_extracts_name(self):
        tool, arg = router._deterministic_route("me mostra o perfil de joao")
        self.assertEqual("profile", tool)
        self.assertEqual("joao", arg)

    def test_parse_llm_route(self):
        self.assertEqual(("ranking", None), router._parse_llm_route("TOOL:ranking"))
        self.assertEqual(("profile", "joao"), router._parse_llm_route("TOOL:profile:joao"))
        self.assertEqual((None, None), router._parse_llm_route("NONE"))
        self.assertEqual((None, None), router._parse_llm_route("blabla"))


if __name__ == "__main__":
    unittest.main()