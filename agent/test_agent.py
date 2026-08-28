import asyncio
import os
import socket
import tempfile
import unittest

from fastapi.testclient import TestClient

from . import api
from . import correlate
from . import feedback as feedback_mod
from . import moderate
from . import router
from . import rules
from . import summary
from . import vectors
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


class VectorStoreTest(unittest.TestCase):
    def _store(self):
        self._tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self._tmp.cleanup)
        store = vectors.VectorStore(os.path.join(self._tmp.name, "vectors.db"))
        self.addCleanup(store.close)
        return store

    def test_upsert_and_search_ordering(self):
        store = self._store()
        store.upsert("feedback", "voce e um idiota", "ODIO", [1.0, 0.0])
        store.upsert("feedback", "boa noite pessoal", "OK", [0.0, 1.0])
        results = store.search([1.0, 0.0], k=2, sources=("feedback",))
        self.assertEqual(2, len(results))
        self.assertEqual("voce e um idiota", results[0]["comment"])
        self.assertAlmostEqual(1.0, results[0]["score"], places=6)

    def test_upsert_dedup_case_insensitive(self):
        store = self._store()
        first = store.upsert("feedback", "Voce e um idiota", "ODIO", [1.0, 0.0])
        second = store.upsert("feedback", "voce e um idiota", "ODIO", [1.0, 0.0])
        self.assertGreater(first, 0)
        self.assertEqual(0, second)
        self.assertEqual(1, store.count("feedback"))

    def test_search_excludes_classify_by_sources(self):
        store = self._store()
        store.upsert("classify", "spam classify", "SPAM", [1.0, 0.0])
        store.upsert("feedback", "spam feedback", "SPAM", [0.9, 0.1])
        results = store.search([1.0, 0.0], k=5, sources=("feedback", "anomaly"))
        self.assertEqual(1, len(results))
        self.assertEqual("spam feedback", results[0]["comment"])


class RulesTest(unittest.TestCase):
    def test_classify_by_rules_parity(self):
        cases = [
            ("boa noite pessoal", False, "OK"),
            ("qual a sua musica favorita?", False, "PERGUNTA"),
            ("jesus salva, aceita a cristo", True, "PROSELITISMO"),
            ("clica no link da bio https://bit.ly/abc", True, "SPAM"),
            ("voce e um idiota", True, "ODIO"),
        ]
        for comment, want_flag, want_cat in cases:
            folded = rules.fold_text(comment)
            res = rules.classify_by_rules(comment, folded)
            self.assertEqual(want_flag, res["flagged"], comment)
            self.assertEqual(want_cat, res["category"], comment)

    def test_engine_allowlist_releases_false_positive(self):
        self._tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self._tmp.cleanup)
        store = feedback_mod.FeedbackStore(os.path.join(self._tmp.name, "feedback.db"))
        self.addCleanup(store.close)
        store.add("voce e um idiota", "ODIO", "NAO")
        engine = rules.RulesEngine(store)
        self.assertFalse(engine.classify("Voce e um idiota")["flagged"])


class _FakeEmbedder:
    def __init__(self, vec=None):
        self._vec = vec or [1.0, 0.0]
        self.calls = []

    async def embed(self, texts):
        self.calls.append(list(texts))
        return [list(self._vec) for _ in texts]


class _FakeStore:
    def __init__(self, examples=None):
        self._examples = examples or []
        self.upserts = []

    def search(self, vec, k=8, sources=("feedback", "anomaly")):
        return list(self._examples[:k])

    def upsert(self, source, comment, category, embedding):
        self.upserts.append((source, comment, category))


class RagModeratorTest(unittest.TestCase):
    def test_parse_token(self):
        self.assertEqual("ODIO", moderate.RagModerator._parse("ODIO"))
        self.assertEqual("PERGUNTA", moderate.RagModerator._parse("  pergunta "))
        self.assertEqual("OK", moderate.RagModerator._parse("blabla"))

    def test_classify_flags_via_llm(self):
        rules_engine = rules.RulesEngine(None)
        store = _FakeStore([{"comment": "vai embora", "category": "ODIO", "score": 0.9}])
        model = _FakeModel(reply="ODIO")
        mod = moderate.RagModerator(
            embedder=_FakeEmbedder([1.0, 0.0]), store=store, model=model, rules=rules_engine
        )
        result = asyncio.run(mod.classify("vai embora seu lixo"))
        self.assertTrue(result["flagged"])
        self.assertEqual("ODIO", result["category"])

    def test_classify_rule_fallback_without_embedder(self):
        rules_engine = rules.RulesEngine(None)
        mod = moderate.RagModerator(embedder=None, store=None, model=None, rules=rules_engine)
        result = asyncio.run(mod.classify("voce e um idiota"))
        self.assertTrue(result["flagged"])
        self.assertEqual("ODIO", result["category"])


class ModerateEndpointTest(unittest.TestCase):
    def test_moderate_flags_rule_match(self):
        mod = moderate.RagModerator(embedder=None, store=None, model=None, rules=rules.RulesEngine(None))
        api.app.state.moderator = mod
        client = TestClient(api.app)
        resp = client.post("/moderate", json={"comment": "voce e um idiota"})
        self.assertEqual(200, resp.status_code)
        self.assertTrue(resp.json()["flagged"])

    def test_moderate_empty_comment(self):
        client = TestClient(api.app)
        resp = client.post("/moderate", json={"comment": ""})
        self.assertEqual(400, resp.status_code)


class GiftQuestionCorrelatorTest(unittest.TestCase):
    def _gift(self):
        return {"giftName": "Rosa", "uniqueId": "alice", "nickname": "Alice"}

    def test_no_candidates_returns_none(self):
        svc = correlate.GiftQuestionCorrelator(model=_FakeModel())
        self.assertIsNone(asyncio.run(svc.correlate(self._gift(), [])))

    def test_single_candidate_skips_llm(self):
        model = _FakeModel()
        svc = correlate.GiftQuestionCorrelator(model=model)
        cands = [{"uniqueId": "alice", "nickname": "Alice", "comment": "oi galera", "timestamp": 1}]
        result = asyncio.run(svc.correlate(self._gift(), cands))
        self.assertEqual(0, len(model.calls))
        self.assertEqual("single-candidate", result["method"])
        self.assertEqual("medium", result["confidence"])

    def test_single_candidate_question_high_confidence(self):
        svc = correlate.GiftQuestionCorrelator(model=_FakeModel())
        cands = [{"uniqueId": "alice", "nickname": "Alice", "comment": "qual a sua música?", "timestamp": 1}]
        result = asyncio.run(svc.correlate(self._gift(), cands))
        self.assertEqual("high", result["confidence"])

    def test_llm_picks_best_question(self):
        model = _FakeModel(reply="MATCH:2")
        svc = correlate.GiftQuestionCorrelator(model=model)
        cands = [
            {"uniqueId": "alice", "nickname": "Alice", "comment": "oi galera", "timestamp": 1},
            {"uniqueId": "alice", "nickname": "Alice", "comment": "qual a sua música favorita?", "timestamp": 2},
            {"uniqueId": "alice", "nickname": "Alice", "comment": "kkkkkk", "timestamp": 3},
        ]
        result = asyncio.run(svc.correlate(self._gift(), cands))
        self.assertEqual(1, len(model.calls))
        self.assertEqual("llm", result["method"])
        self.assertEqual("qual a sua música favorita?", result["comment"])
        self.assertEqual("high", result["confidence"])

    def test_llm_none_falls_back_to_heuristic(self):
        model = _FakeModel(reply="NONE")
        svc = correlate.GiftQuestionCorrelator(model=model)
        cands = [
            {"uniqueId": "alice", "nickname": "Alice", "comment": "oi galera", "timestamp": 1},
            {"uniqueId": "alice", "nickname": "Alice", "comment": "qual a sua religiao?", "timestamp": 2},
        ]
        result = asyncio.run(svc.correlate(self._gift(), cands))
        self.assertEqual("heuristic-fallback", result["method"])
        self.assertEqual("qual a sua religiao?", result["comment"])
        self.assertEqual("medium", result["confidence"])

    def test_llm_unavailable_falls_back_to_heuristic(self):
        class _Boom:
            async def chat(self, messages, max_tokens=512, temperature=0.1):
                raise RuntimeError("llm down")

        svc = correlate.GiftQuestionCorrelator(model=_Boom())
        cands = [
            {"uniqueId": "alice", "nickname": "Alice", "comment": "oi", "timestamp": 1},
            {"uniqueId": "alice", "nickname": "Alice", "comment": "tem como cantar?", "timestamp": 2},
        ]
        result = asyncio.run(svc.correlate(self._gift(), cands))
        self.assertEqual("heuristic-fallback", result["method"])
        self.assertEqual("tem como cantar?", result["comment"])

    def test_no_model_uses_heuristic(self):
        svc = correlate.GiftQuestionCorrelator(model=None)
        cands = [
            {"uniqueId": "alice", "nickname": "Alice", "comment": "oi galera", "timestamp": 1},
            {"uniqueId": "alice", "nickname": "Alice", "comment": "quando termina?", "timestamp": 2},
        ]
        result = asyncio.run(svc.correlate(self._gift(), cands))
        self.assertEqual("heuristic-fallback", result["method"])
        self.assertEqual("quando termina?", result["comment"])

    def test_buffer_candidates_from_agent_buffer(self):
        buf = MessageBuffer()
        buf._add_message({"uniqueId": "alice", "nickname": "Alice", "comment": "oi", "timestamp": 1})
        buf._add_message({"uniqueId": "bob", "nickname": "Bob", "comment": "qual o nome?", "timestamp": 2})
        buf._add_message({"uniqueId": "alice", "nickname": "Alice", "comment": "qual a sua música?", "timestamp": 3})
        svc = correlate.GiftQuestionCorrelator(model=None, buffer=buf)
        cands = svc.buffer_candidates(self._gift())
        self.assertEqual(2, len(cands))
        self.assertTrue(all(m["uniqueId"] == "alice" for m in cands))
        # Sem candidatos do monitor, o correlator usa o próprio buffer.
        result = asyncio.run(svc.correlate(self._gift()))
        self.assertEqual("qual a sua música?", result["comment"])


class CorrelateGiftEndpointTest(unittest.TestCase):
    def test_ok_with_candidates(self):
        api.app.state.correlator = correlate.GiftQuestionCorrelator(_FakeModel(reply="MATCH:2"))
        client = TestClient(api.app)
        payload = {
            "gift": {"giftName": "Rosa", "uniqueId": "alice", "nickname": "Alice"},
            "isTarget": True,
            "candidates": [
                {"uniqueId": "alice", "nickname": "Alice", "comment": "oi", "timestamp": 1},
                {"uniqueId": "alice", "nickname": "Alice", "comment": "qual a sua música?", "timestamp": 2},
            ],
        }
        resp = client.post("/correlate-gift", json=payload)
        self.assertEqual(200, resp.status_code)
        body = resp.json()
        self.assertEqual("qual a sua música?", body["match"]["comment"])
        self.assertEqual("llm", body["method"])
        self.assertEqual("high", body["confidence"])

    def test_disabled_when_not_target_gift(self):
        api.app.state.correlator = correlate.GiftQuestionCorrelator(_FakeModel())
        client = TestClient(api.app)
        resp = client.post("/correlate-gift", json={
            "gift": {"giftName": "Rosa", "uniqueId": "alice", "nickname": "Alice"},
            "isTarget": False,
            "candidates": [{"uniqueId": "alice", "nickname": "Alice", "comment": "oi", "timestamp": 1}],
        })
        self.assertEqual(200, resp.status_code)
        self.assertIsNone(resp.json()["match"])

    def test_missing_gift_name(self):
        client = TestClient(api.app)
        resp = client.post("/correlate-gift", json={"gift": {"uniqueId": "alice"}})
        self.assertEqual(400, resp.status_code)

    def test_no_candidates_and_no_buffer(self):
        api.app.state.correlator = correlate.GiftQuestionCorrelator(_FakeModel())
        client = TestClient(api.app)
        resp = client.post("/correlate-gift", json={
            "gift": {"giftName": "Rosa", "uniqueId": "alice", "nickname": "Alice"},
            "candidates": [],
        })
        self.assertEqual(200, resp.status_code)
        self.assertIsNone(resp.json()["match"])


if __name__ == "__main__":
    unittest.main()