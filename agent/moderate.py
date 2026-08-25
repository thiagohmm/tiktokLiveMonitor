"""Moderação RAG: regras (fallback) + recuperação semântica + LLM.

Implementa ``EventSink`` para consumir o stream SSE do monitor e, a cada
``new-chat-message``, classificar via regras e/ou RAG+LLM, reportando flags de
volta ao monitor por ``MonitorClient.flag``. Dependências injetadas (DIP).
"""

import asyncio
import logging
import time

from . import config
from .buffer import EventSink
from .rules import CATEGORIES, RulesEngine, category_label, fold_text

log = logging.getLogger("agent.moderate")

FLAGGED = frozenset(("PROSELITISMO", "ODIO", "SPAM", "GOLPE", "OUTRO"))

_DEDUP_WINDOW_S = 60.0

_SYSTEM_TEMPLATE = (
    "Você é o moderador de uma live do TikTok. Classifique o comentário em EXATAMENTE "
    "uma das categorias: OK, PERGUNTA, PROSELITISMO, ODIO, SPAM, GOLPE ou OUTRO.\n"
    "Use os exemplos rotulados abaixo como referência.\n\n"
    "EXEMPLOS:\n{examples}\n\n"
    "Responda apenas com a categoria, sem pontuação ou explicação."
)


class RagModerator:
    """Orquestra regras → RAG → LLM e reporta flags ao monitor."""

    def __init__(self, embedder, store, model, rules: RulesEngine, monitor=None):
        self._embedder = embedder
        self._store = store
        self._model = model
        self._rules = rules
        self._monitor = monitor
        self._enabled = True
        self._ai_enabled = True
        self._queue: asyncio.Queue = asyncio.Queue(maxsize=max(config.RAG_CONCURRENCY * 4, 8))
        self._sem = asyncio.Semaphore(max(config.RAG_CONCURRENCY, 1))
        self._recent: dict[tuple[str, str], float] = {}
        self._worker_task: asyncio.Task | None = None

    # --- Ciclo de vida ---

    def start(self) -> None:
        if self._worker_task is None:
            self._worker_task = asyncio.create_task(self._run())

    async def stop(self) -> None:
        if self._worker_task is not None:
            self._worker_task.cancel()
            try:
                await self._worker_task
            except asyncio.CancelledError:
                pass
            self._worker_task = None

    def set_settings(self, settings: dict) -> None:
        if not settings:
            return
        if "moderationEnabled" in settings:
            self._enabled = bool(settings["moderationEnabled"])
        if "aiModerationEnabled" in settings:
            self._ai_enabled = bool(settings["aiModerationEnabled"])

    # --- EventSink ---

    def ingest(self, event_type: str, data: dict) -> None:
        if event_type == "settings-update":
            self.set_settings(data)
            return
        if event_type != "new-chat-message":
            return
        comment = str(data.get("comment") or "").strip()
        if not comment:
            return
        unique_id = str(data.get("uniqueId") or "")
        nickname = str(data.get("nickname") or "")
        key = (unique_id.lower(), fold_text(comment))
        now = time.monotonic()
        if self._recent.get(key, 0.0) > now - _DEDUP_WINDOW_S:
            return
        self._recent[key] = now
        try:
            self._queue.put_nowait((comment, unique_id, nickname))
        except asyncio.QueueFull:
            log.warning("fila de moderação cheia; descartando mensagem")

    # --- Worker ---

    async def _run(self) -> None:
        while True:
            comment, unique_id, nickname = await self._queue.get()
            try:
                async with self._sem:
                    await self._process(comment, unique_id, nickname)
            except asyncio.CancelledError:
                raise
            except Exception as exc:  # noqa: BLE001
                log.warning("falha ao moderar: %s", exc)
            finally:
                self._queue.task_done()

    async def _process(self, comment: str, unique_id: str, nickname: str) -> None:
        if not self._enabled:
            return
        result, _ = await self._analyze(comment)
        if result["flagged"]:
            await self._emit(comment, unique_id, nickname, result["category"], result["reason"], "rag")

    async def classify(self, comment: str) -> dict:
        """Classifica sem reportar flag (usado pelo endpoint /moderate)."""
        result, _ = await self._analyze(comment)
        return result

    async def _analyze(self, comment: str) -> tuple[dict, list[float] | None]:
        # 1. Regras SEMPRE rodam primeiro (fallback determinístico).
        result = self._rules.classify(comment)

        # 2. Embedding (uma vez) para indexação 'chat' e para o RAG.
        vec = None
        if self._embedder is not None:
            try:
                vecs = await self._embedder.embed([comment])
                vec = vecs[0] if vecs else None
            except Exception as exc:  # noqa: BLE001
                log.warning("embedding indisponível: %s", exc)

        if vec is not None:
            if config.RAG_CHAT_INDEX_ENABLED:
                self._safe_index("chat", comment, result["category"], vec)
            if result["flagged"]:
                self._safe_index("classify", comment, result["category"], vec)
                return result, vec

        if result["flagged"]:
            return result, vec

        if vec is None or self._store is None or not self._ai_enabled:
            return result, vec

        # 3. RAG + LLM.
        examples = self._store.search(vec, k=config.RAG_TOP_K, sources=("feedback", "anomaly"))
        category = await self._classify_llm(comment, examples)
        out = {
            "flagged": category in FLAGGED,
            "category": category,
            "reason": category_label(category),
        }
        if out["flagged"]:
            self._safe_index("classify", comment, category, vec)
        return out, vec

    async def _classify_llm(self, comment: str, examples: list[dict]) -> str:
        lines = [f'- "{ex["comment"]}" -> {ex["category"]}' for ex in examples]
        system = _SYSTEM_TEMPLATE.format(examples="\n".join(lines) or "(nenhum exemplo)")
        raw = await self._model.chat(
            [{"role": "system", "content": system}, {"role": "user", "content": comment}],
            max_tokens=8,
            temperature=0.0,
        )
        return self._parse(raw)

    @staticmethod
    def _parse(raw: str) -> str:
        token = (raw or "").strip().upper()
        for cat in CATEGORIES:
            if cat == token:
                return cat
        for cat in CATEGORIES:
            if token.startswith(cat):
                return cat
        return "OK"

    async def _emit(self, comment, unique_id, nickname, category, reason, source) -> None:
        if self._monitor is None:
            return
        try:
            await self._monitor.flag({
                "comment": comment,
                "uniqueId": unique_id,
                "nickname": nickname,
                "category": category,
                "reason": reason,
                "source": source,
            })
        except Exception as exc:  # noqa: BLE001
            log.warning("falha ao enviar flag ao monitor: %s", exc)

    def _safe_index(self, source: str, comment: str, category: str, vec) -> None:
        if self._store is None:
            return
        try:
            self._store.upsert(source, comment, category, vec)
        except Exception as exc:  # noqa: BLE001
            log.warning("falha ao indexar %r: %s", source, exc)
