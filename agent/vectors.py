"""Índice vetorial em SQLite (BLOB float32) com cosseno em Python puro.

Espelha o padrão de ``feedback.FeedbackStore`` (sqlite3 + WAL + busy_timeout +
lock) e mantém a busca simples: para o volume de feedback/mensagens
(centenas/milhares de linhas) a varredura exaustiva em Python é instantânea,
sem dependências nativas.
"""

import logging
import math
import os
import sqlite3
import struct
import threading

log = logging.getLogger("agent.vectors")

SOURCES = ("feedback", "anomaly", "chat", "classify")

_SCHEMA = """
CREATE TABLE IF NOT EXISTS moderation_vectors (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    source TEXT NOT NULL,
    comment TEXT NOT NULL,
    category TEXT NOT NULL,
    embedding BLOB NOT NULL,
    dim INTEGER NOT NULL,
    created_at TEXT DEFAULT CURRENT_TIMESTAMP
)
"""

_INDEX_SOURCE = (
    "CREATE INDEX IF NOT EXISTS idx_moderation_vectors_source "
    "ON moderation_vectors(source)"
)

# Mapeamento de ``expected`` (tabela false_positives) para categoria normalizada.
_EXPECTED_TO_CATEGORY = {
    "NAO": "OK",
    "SIM_PERGUNTA": "PERGUNTA",
    "SIM_PROSELITISMO": "PROSELITISMO",
    "SIM_ODIO": "ODIO",
    "SIM_SPAM": "SPAM",
    "SIM_GOLPE": "GOLPE",
    "SIM_OUTRO": "OUTRO",
}


def expected_to_category(expected: str) -> str:
    """Converte o ``expected`` do feedback em categoria normalizada."""
    return _EXPECTED_TO_CATEGORY.get(expected, "OK")


def fold(text: str) -> str:
    """Normaliza (lower + trim + ç→c + sem marcas combinantes), como no Go."""
    s = (text or "").lower().strip()
    s = s.replace("ç", "c")
    return "".join(ch for ch in s if not (0x0300 <= ord(ch) <= 0x036F))


def _normalize(vec):
    norm = math.sqrt(sum(x * x for x in vec))
    if norm == 0:
        return [0.0] * len(vec)
    return [x / norm for x in vec]


class VectorStore:
    """Acesso thread-safe à tabela de vetores no SQLite."""

    def __init__(self, path: str):
        directory = os.path.dirname(os.path.abspath(path))
        os.makedirs(directory, exist_ok=True)
        self._conn = sqlite3.connect(path, check_same_thread=False)
        self._conn.execute("PRAGMA journal_mode=WAL")
        self._conn.execute("PRAGMA busy_timeout=5000")
        self._lock = threading.Lock()
        with self._lock:
            self._conn.execute(_SCHEMA)
            self._conn.execute(_INDEX_SOURCE)
            self._conn.commit()
        log.info("vector store aberto em %s", path)

    @staticmethod
    def _pack(vec) -> bytes:
        return struct.pack("<%df" % len(vec), *vec)

    @staticmethod
    def _unpack(blob) -> list[float]:
        n = len(blob) // 4
        return list(struct.unpack("<%df" % n, blob))

    def upsert(self, source: str, comment: str, category: str, embedding) -> int:
        """Insere se ainda não existir para (source, comment normalizado)."""
        if source not in SOURCES:
            raise ValueError(f"invalid source {source!r}")
        comment = fold(comment)
        if not comment:
            return 0
        category = (category or "OK").strip()
        vec = _normalize([float(x) for x in embedding])
        blob = self._pack(vec)

        with self._lock:
            row = self._conn.execute(
                "SELECT id FROM moderation_vectors WHERE source = ? AND comment = ? LIMIT 1",
                (source, comment),
            ).fetchone()
            if row is not None:
                return 0
            cur = self._conn.execute(
                "INSERT INTO moderation_vectors (source, comment, category, embedding, dim)"
                " VALUES (?, ?, ?, ?, ?)",
                (source, comment, category, blob, len(vec)),
            )
            self._conn.commit()
            return cur.lastrowid

    def search(self, query_vec, k: int = 8, sources=("feedback", "anomaly")) -> list[dict]:
        """Retorna os top-k vizinhos por cosseno (vetores normalizados)."""
        if not query_vec or k < 1:
            return []
        q = _normalize([float(x) for x in query_vec])
        dim = len(q)
        placeholders = ",".join("?" for _ in sources)

        with self._lock:
            rows = self._conn.execute(
                "SELECT comment, category, embedding FROM moderation_vectors"
                f" WHERE source IN ({placeholders}) AND dim = ?",
                (*sources, dim),
            ).fetchall()

        scored = []
        for comment, category, blob in rows:
            vec = self._unpack(blob)
            if len(vec) != dim:
                continue
            score = sum(a * b for a, b in zip(q, vec))
            scored.append({"comment": comment, "category": category, "score": score})
        scored.sort(key=lambda r: r["score"], reverse=True)
        return scored[:k]

    def count(self, source: str | None = None) -> int:
        with self._lock:
            if source is not None:
                row = self._conn.execute(
                    "SELECT COUNT(*) FROM moderation_vectors WHERE source = ?",
                    (source,),
                ).fetchone()
            else:
                row = self._conn.execute(
                    "SELECT COUNT(*) FROM moderation_vectors"
                ).fetchone()
            return int(row[0])

    def close(self) -> None:
        with self._lock:
            self._conn.close()


async def backfill(embedder, vector_store, feedback_store, db_path: str, limit: int = 500) -> None:
    """Indexa o histórico existente: false_positives, anomaly_logs e user_messages.

    Idempotente (dedup por source+comment) e tolerante a falhas de embedding/banco.
    """
    if embedder is None or vector_store is None or limit < 1:
        return

    items: list[tuple[str, str, str]] = []

    for row in feedback_store.recent(limit):
        comment = (row.get("comment") or "").strip()
        if comment:
            items.append(("feedback", comment, expected_to_category(row.get("expected"))))

    try:
        conn = sqlite3.connect(db_path)
        try:
            rows = conn.execute(
                "SELECT comment, category FROM anomaly_logs WHERE is_anomaly = 1"
                " ORDER BY timestamp DESC LIMIT ?",
                (limit,),
            ).fetchall()
            for comment, category in rows:
                comment = (comment or "").strip()
                if comment:
                    items.append(("anomaly", comment, (category or "OUTRO").strip()))
            rows = conn.execute(
                "SELECT message FROM user_messages ORDER BY timestamp DESC LIMIT ?",
                (limit,),
            ).fetchall()
            for (message,) in rows:
                message = (message or "").strip()
                if message:
                    items.append(("chat", message, "OK"))
        finally:
            conn.close()
    except Exception as exc:  # noqa: BLE001
        log.warning("backfill: falha ao ler o banco: %s", exc)

    seen: set[tuple[str, str]] = set()
    unique: list[tuple[str, str, str]] = []
    for source, comment, category in items:
        key = (source, fold(comment))
        if key in seen:
            continue
        seen.add(key)
        unique.append((source, comment, category))
        if len(unique) >= limit:
            break

    batch = 32
    for i in range(0, len(unique), batch):
        chunk = unique[i : i + batch]
        texts = [c for _, c, _ in chunk]
        try:
            vecs = await embedder.embed(texts)
        except Exception as exc:  # noqa: BLE001
            log.warning("backfill: embedding indisponível: %s", exc)
            return
        for (source, comment, category), vec in zip(chunk, vecs):
            try:
                vector_store.upsert(source, comment, category, vec)
            except Exception as exc:  # noqa: BLE001
                log.warning("backfill: falha ao indexar %r: %s", comment, exc)

    log.info("backfill concluído: %d itens processados", len(unique))
