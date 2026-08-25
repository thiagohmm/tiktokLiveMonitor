"""Persistência de feedback de moderação — migração de ``internal/database``.

O arquivo ``feedback.db`` (SQLite, WAL) passa a ser dono do agente Python,
mantendo o mesmo schema do Go (tabela ``false_positives``) e as mesmas regras
de validação/deduplicação do ``AddFeedback``.
"""

import logging
import os
import sqlite3
import threading

log = logging.getLogger("agent.feedback")

VALID_EXPECTED = frozenset(
    {
        "NAO",
        "SIM_PERGUNTA",
        "SIM_PROSELITISMO",
        "SIM_ODIO",
        "SIM_SPAM",
        "SIM_GOLPE",
        "SIM_OUTRO",
    }
)

VALID_CATEGORY = frozenset(
    {"OK", "PERGUNTA", "PROSELITISMO", "ODIO", "SPAM", "GOLPE", "OUTRO"}
)

_SCHEMA = """
CREATE TABLE IF NOT EXISTS false_positives (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    comment TEXT NOT NULL,
    category TEXT NOT NULL,
    expected TEXT DEFAULT 'NAO',
    timestamp DATETIME DEFAULT CURRENT_TIMESTAMP
)
"""


class FeedbackError(ValueError):
    """Erro de validação (mapeia para HTTP 400, como no handler Go)."""


class FeedbackStore:
    """Acesso thread-safe ao ``feedback.db`` (SQLite serializa as escritas)."""

    def __init__(self, path: str):
        directory = os.path.dirname(os.path.abspath(path))
        os.makedirs(directory, exist_ok=True)
        self._conn = sqlite3.connect(path, check_same_thread=False)
        self._conn.execute("PRAGMA journal_mode=WAL")
        self._conn.execute("PRAGMA busy_timeout=5000")
        self._lock = threading.Lock()
        with self._lock:
            self._conn.execute(_SCHEMA)
            self._conn.commit()
        log.info("feedback.db aberto em %s", path)

    def add(self, comment: str, category: str, expected: str) -> int:
        """Registra um feedback; retorna o id (0 se já existia).

        Mensagens de erro idênticas às do Go: "comment is required",
        "invalid category", "invalid expected".
        """
        comment = (comment or "").strip()
        if not comment:
            raise FeedbackError("comment is required")
        if category not in VALID_CATEGORY:
            raise FeedbackError("invalid category")
        if expected not in VALID_EXPECTED:
            raise FeedbackError("invalid expected")

        with self._lock:
            row = self._conn.execute(
                "SELECT id FROM false_positives WHERE comment = ? AND expected = ? LIMIT 1",
                (comment, expected),
            ).fetchone()
            if row is not None:
                return 0
            cursor = self._conn.execute(
                "INSERT INTO false_positives (comment, category, expected) VALUES (?, ?, ?)",
                (comment, category, expected),
            )
            self._conn.commit()
            return cursor.lastrowid

    def recent(self, limit: int = 100) -> list[dict]:
        """Últimos N feedbacks, do mais recente para o mais antigo."""
        if limit < 1 or limit > 500:
            limit = 100
        with self._lock:
            rows = self._conn.execute(
                "SELECT id, comment, category, expected, timestamp "
                "FROM false_positives ORDER BY timestamp DESC, id DESC LIMIT ?",
                (limit,),
            ).fetchall()
        return [
            {"id": r[0], "comment": r[1], "category": r[2], "expected": r[3], "timestamp": r[4]}
            for r in rows
        ]

    def false_positive_comments(self, limit: int = 100) -> list[str]:
        """Comentários distintos marcados como falso positivo (expected='NAO')."""
        if limit < 1 or limit > 500:
            limit = 100
        with self._lock:
            rows = self._conn.execute(
                "SELECT DISTINCT comment FROM false_positives "
                "WHERE expected = 'NAO' ORDER BY timestamp DESC LIMIT ?",
                (limit,),
            ).fetchall()
        return [r[0] for r in rows]

    def close(self) -> None:
        with self._lock:
            self._conn.close()
