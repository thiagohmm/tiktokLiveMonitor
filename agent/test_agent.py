import asyncio
import os
import socket
import tempfile
import unittest

from fastapi.testclient import TestClient

from . import api
from . import feedback as feedback_mod
from . import router
from . import summary
from .buffer import MessageBuffer
from .context import ContextBuilder
from .history import AskAIService, ConversationStore
from .llm_worker import LlamaWorker


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


class FeedbackStoreTest(unittest.TestCase):
    def _store(self):
        self._tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self._tmp.cleanup)
        store = feedback_mod.FeedbackStore(os.path.join(self._tmp.name, "feedback.db"))
        self.addCleanup(store.close)
        return store

    def test_add_and_recent(self):
        store = self._store()
        store.add("comentário ok", "OK", "NAO")
        recent = store.recent()
        self.assertEqual(1, len(recent))
        self.assertEqual("comentário ok", recent[0]["comment"])
        self.assertEqual("NAO", recent[0]["expected"])

    def test_dedup_same_comment_and_expected(self):
        store = self._store()
        store.add("spam", "SPAM", "NAO")
        self.assertEqual(0, store.add("spam", "SPAM", "NAO"))
        self.assertEqual(1, len(store.recent()))

    def test_validation_errors(self):
        store = self._store()
        with self.assertRaises(feedback_mod.FeedbackError):
            store.add("", "OK", "NAO")
        with self.assertRaises(feedback_mod.FeedbackError):
            store.add("x", "INVALIDO", "NAO")
        with self.assertRaises(feedback_mod.FeedbackError):
            store.add("x", "OK", "INVALIDO")


class ConversationStoreTest(unittest.TestCase):
    def test_messages_per_session(self):
        store = ConversationStore()
        store.append("s1", "user", "oi")
        store.append("s2", "user", "tchau")
        self.assertEqual(1, len(store.messages("s1")))
        self.assertEqual(1, len(store.messages("s2")))
        self.assertEqual(0, len(store.messages("s3")))

    def test_max_messages_evicts_oldest(self):
        store = ConversationStore(max_messages=2)
        for i in range(3):
            store.append("s1", "user", f"m{i}")
        msgs = store.messages("s1")
        self.assertEqual(2, len(msgs))
        self.assertEqual("m1", msgs[0]["content"])


class LlamaWorkerTest(unittest.TestCase):
    def test_resolve_bin_fallback(self):
        worker = LlamaWorker(bin_dir="/nonexistent", models_dir="/nonexistent")
        self.assertTrue(worker.resolve_bin().endswith("llama-server"))

    def test_register_remote_replaces_local(self):
        worker = LlamaWorker()
        worker.register_remote("10.0.0.5", 9090)
        self.assertFalse(worker.is_local)
        self.assertEqual("10.0.0.5", worker.host)
        self.assertEqual(9090, worker.port)
        self.assertTrue(worker.ready)

    def test_check_health_dead_worker(self):
        s = socket.socket()
        s.bind(("127.0.0.1", 0))
        port = s.getsockname()[1]
        s.close()
        worker = LlamaWorker(port=port, bind_host="127.0.0.1")
        self.assertFalse(asyncio.run(worker.check_health()))


class _FakeModel:
    def __init__(self, reply="resposta fake"):
        self.reply = reply
        self.calls = []

    async def chat(self, messages, max_tokens=512, temperature=0.1):
        self.calls.append(messages)
        return self.reply


class _ScriptedModel:
    def __init__(self, replies):
        self.replies = list(replies)
        self.calls = []

    async def chat(self, messages, max_tokens=512, temperature=0.1):
        self.calls.append(messages)
        if len(self.replies) > 1:
            return self.replies.pop(0)
        return self.replies[0]


class _FakeMonitor:
    async def gifts(self, user=None):
        return []

    async def history(self):
        return []


class _FakeWorker:
    def __init__(self, ready, error=None):
        self._ready = ready
        self._error = error

    async def ensure_ready(self):
        return self._ready, self._error


class AskAIEndpointTest(unittest.TestCase):
    def test_without_history(self):
        model = _FakeModel()
        api.app.state.ask_ai = AskAIService(model=model, monitor=_FakeMonitor())
        client = TestClient(api.app)
        resp = client.post("/ask-ai", json={"question": "pergunta 1", "session": "nova"})
        self.assertEqual(200, resp.status_code)
        self.assertEqual("pergunta 1", resp.json()["question"])
        self.assertEqual(1, len(model.calls))
        roles = [m["role"] for m in model.calls[0]]
        self.assertNotIn("assistant", roles)

    def test_with_history(self):
        model = _FakeModel()
        api.app.state.ask_ai = AskAIService(model=model, monitor=_FakeMonitor())
        client = TestClient(api.app)
        first = client.post("/ask-ai", json={"question": "pergunta 1", "session": "s"})
        self.assertEqual(200, first.status_code)
        second = client.post("/ask-ai", json={"question": "pergunta 2", "session": "s"})
        self.assertEqual(200, second.status_code)
        self.assertEqual(2, len(model.calls))
        contents = [m["content"] for m in model.calls[1]]
        self.assertIn("pergunta 1", contents)
        roles = [m["role"] for m in model.calls[1]]
        self.assertEqual("user", roles[-1])


class ProbeLLMEndpointTest(unittest.TestCase):
    def test_worker_alive(self):
        api.app.state.worker = _FakeWorker(True)
        client = TestClient(api.app)
        resp = client.get("/probe-llm")
        self.assertEqual(200, resp.status_code)
        self.assertTrue(resp.json()["llmActive"])

    def test_worker_dead(self):
        api.app.state.worker = _FakeWorker(False)
        client = TestClient(api.app)
        resp = client.get("/probe-llm")
        self.assertEqual(200, resp.status_code)
        self.assertFalse(resp.json()["llmActive"])


class AskEndpointTest(unittest.TestCase):
    def test_ask_routes_through_copilot(self):
        model = _ScriptedModel(["NONE", "resposta do copiloto"])
        copilot = router.Copilot(model=model, buffer=None)
        api.app.state.copilot = copilot
        client = TestClient(api.app)
        resp = client.post("/ask", json={"question": "oi"})
        self.assertEqual(200, resp.status_code)
        self.assertEqual("resposta do copiloto", resp.json()["answer"])
        self.assertEqual(2, len(model.calls))


class SummarizeEndpointTest(unittest.TestCase):
    def test_summarize(self):
        api.app.state.summarizer = summary.LiveSummarizer(_FakeModel(reply="resumo fake"), MessageBuffer())
        client = TestClient(api.app)
        resp = client.get("/summarize")
        self.assertEqual(200, resp.status_code)
        self.assertEqual("resumo fake", resp.json()["summary"])


class FeedbackEndpointTest(unittest.TestCase):
    def _store(self):
        self._tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self._tmp.cleanup)
        store = feedback_mod.FeedbackStore(os.path.join(self._tmp.name, "feedback.db"))
        self.addCleanup(store.close)
        return store

    def test_persist_and_query(self):
        store = self._store()
        api.app.state.feedback = store
        client = TestClient(api.app)
        resp = client.post("/feedback", json={"comment": "spam", "category": "SPAM", "expected": "NAO"})
        self.assertEqual(200, resp.status_code)
        self.assertTrue(resp.json()["success"])
        recent = store.recent()
        self.assertEqual(1, len(recent))
        self.assertEqual("spam", recent[0]["comment"])

    def test_invalid_category(self):
        api.app.state.feedback = self._store()
        client = TestClient(api.app)
        resp = client.post("/feedback", json={"comment": "x", "category": "INVALID", "expected": "NAO"})
        self.assertEqual(400, resp.status_code)


if __name__ == "__main__":
    unittest.main()