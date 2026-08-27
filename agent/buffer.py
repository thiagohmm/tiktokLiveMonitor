"""Buffer em memória dos eventos da live + detecção de repetição.

Separado da montagem do texto de contexto (ver ``context.py``) para
responder a uma única responsabilidade (SRP). Define também os contratos
(Protocol) mínimos usados pelos consumidores (DIP/ISP).
"""

import time
from collections import Counter, defaultdict, deque
from typing import Protocol

CHAT_BUFFER_MAX = 500
REPEAT_WINDOW_MS = 60_000
REPEATS_REQUIRED = 3
FLAGGED_MAX = 200
REPETITIONS_MAX = 100


def _norm_id(value):
    return str(value or "").strip().lower()


class EventSink(Protocol):
    """Contrato mínimo para quem consome eventos da live (ISP)."""

    def ingest(self, event_type: str, data: dict) -> None:
        """Recebe um evento tipado vindo do monitor."""


class LiveBuffer(Protocol):
    """Contrato mínimo exigido pelo copilot/roteador (ISP/DIP)."""

    def ingest(self, event_type: str, data: dict) -> None: ...
    def recent_messages(self, limit: int = 50) -> list: ...
    def active_participants(self) -> list: ...
    def top_repetitions(self, limit: int = 10) -> list: ...
    def top_gifts(self, limit: int = 10) -> list: ...
    def participant_gift_matrix(self, limit: int | None = None) -> list: ...
    def gift_chat_correlation(self) -> dict: ...
    def recent_flagged(self, limit: int = 20) -> list: ...
    def participant_uid_by_name(self, name: str) -> str | None: ...
    def recent_messages_by_user(self, uid: str, nickname: str, limit: int = 20) -> list: ...
    def total_messages(self) -> int: ...


class MessageBuffer:
    """Acumula mensagens, presentes e eventos flagados vindos do SSE.

    Roda no mesmo event loop do servidor HTTP (asyncio single-threaded),
    portanto dispensa locks para as mutações síncronas abaixo.
    """

    def __init__(self):
        self.messages = deque(maxlen=CHAT_BUFFER_MAX)
        self.gift_counts = Counter()
        self.gift_total = 0
        self.flagged = deque(maxlen=FLAGGED_MAX)
        self.repetitions = []
        self._repeat_stamps = defaultdict(list)
        self._repeat_alerted = set()
        # OCP: adicionar um novo tipo de evento = adicionar uma entrada no mapa.
        self._event_handlers = {
            "new-chat-message": self._add_message,
            "any-gift-received": self._add_gift,
            "flagged-message": self._add_flagged,
        }

    # --- ingestão ---

    def ingest(self, event_type, data):
        handler = self._event_handlers.get(event_type)
        if handler is not None:
            handler(data)

    def _add_message(self, data):
        comment = str(data.get("comment") or "").strip()
        if not comment:
            return
        uid = str(data.get("uniqueId") or "")
        nickname = str(data.get("nickname") or "")
        now = data.get("timestamp")
        now = int(now) if now else int(time.time() * 1000)
        sender = _norm_id(uid) or "?"
        comment_lower = comment.lower()
        key = (sender, comment_lower)

        stamps = [t for t in self._repeat_stamps.get(key, ()) if now - t < REPEAT_WINDOW_MS]
        repeats = len(stamps)
        stamps.append(now)
        self._repeat_stamps[key] = stamps

        if repeats >= REPEATS_REQUIRED - 1:
            if key not in self._repeat_alerted:
                self._repeat_alerted.add(key)
                self.repetitions.append({
                    "uniqueId": uid,
                    "nickname": nickname or uid or "?",
                    "comment": comment,
                    "count": repeats + 1,
                    "timestamp": now,
                })
                if len(self.repetitions) > REPETITIONS_MAX:
                    self.repetitions = self.repetitions[-REPETITIONS_MAX:]
        else:
            self._repeat_alerted.discard(key)

        self.messages.append({
            "uniqueId": uid,
            "nickname": nickname,
            "comment": comment,
            "timestamp": now,
            "isFollower": bool(data.get("isFollower")),
        })

    def _add_gift(self, data):
        gift_name = str(data.get("giftName") or "")
        if not gift_name:
            return
        uid = _norm_id(data.get("uniqueId")) or "?"
        nickname = str(data.get("nickname") or "")
        repeat = data.get("repeatCount")
        try:
            repeat = int(repeat) if repeat else 1
        except (TypeError, ValueError):
            repeat = 1
        if repeat < 1:
            repeat = 1
        self.gift_counts[(uid, nickname, gift_name)] += repeat
        self.gift_total += repeat

    def _add_flagged(self, data):
        self.flagged.append({
            "uniqueId": str(data.get("uniqueId") or ""),
            "nickname": str(data.get("nickname") or ""),
            "comment": str(data.get("comment") or ""),
            "category": str(data.get("category") or ""),
            "reason": str(data.get("reason") or ""),
            "timestamp": data.get("timestamp"),
        })

    # --- leitura ---

    def total_messages(self):
        return len(self.messages)

    def recent_messages(self, limit=50):
        messages = list(self.messages)
        return messages[-limit:]

    def active_participants(self):
        users = {}
        for m in self.messages:
            key = _norm_id(m["uniqueId"]) or "?"
            entry = users.get(key)
            if entry is None:
                entry = {"uniqueId": m["uniqueId"], "nickname": m["nickname"], "messages": 0}
                users[key] = entry
            entry["messages"] += 1
            if not entry["nickname"] and m["nickname"]:
                entry["nickname"] = m["nickname"]
        return list(users.values())

    def top_repetitions(self, limit=10):
        ordered = sorted(self.repetitions, key=lambda r: (-r["count"], -r["timestamp"]))
        return ordered[:limit]

    def top_gifts(self, limit=10):
        agg = Counter()
        for (uid, nickname, gift_name), count in self.gift_counts.items():
            agg[(nickname or uid or "?", gift_name)] += count
        return [
            {"nickname": nickname, "giftName": gift_name, "count": count}
            for (nickname, gift_name), count in agg.most_common(limit)
        ]

    def participant_gift_matrix(self, limit=None):
        """Cruzamento presentes x mensagens por participante (SRP).

        Cruza os usuários que deram presentes com os que comentaram,
        permitindo ver quem dá presentes também comenta (e vice-versa). A
        ordêação prioriza quem dá presentes e, em seguida, o volume
        combinado de presentes e mensagens.
        """
        msgs_by_uid = defaultdict(int)
        gifts_by_uid = defaultdict(int)
        nicks = {}

        for m in self.messages:
            key = _norm_id(m["uniqueId"]) or "?"
            msgs_by_uid[key] += 1
            nicks.setdefault(key, m["nickname"] or "")

        for (uid, nickname, _gift_name), count in self.gift_counts.items():
            gifts_by_uid[uid] += count
            nicks.setdefault(uid, nickname or "")

        rows = []
        for uid in set(msgs_by_uid) | set(gifts_by_uid):
            rows.append({
                "user": nicks.get(uid) or uid,
                "uid": uid,
                "gifts": gifts_by_uid.get(uid, 0),
                "messages": msgs_by_uid.get(uid, 0),
                "gave_gifts": uid in gifts_by_uid,
                "commented": uid in msgs_by_uid,
            })
        rows.sort(key=lambda r: (not r["gave_gifts"], -r["gifts"], -r["messages"]))
        return rows[:limit] if limit is not None else rows

    def gift_chat_correlation(self):
        """Resumo da correlação presentes <-> chat.

        Resume, em um dicionário, quantos participantes deram presentes,
        quantos comentaram e quantos fizeram as duas coisas. A lista
        completa (por participante) fica em participant_gift_matrix.
        """
        rows = self.participant_gift_matrix()
        both = [r for r in rows if r["gave_gifts"] and r["commented"]]
        gifts_only = [r for r in rows if r["gave_gifts"] and not r["commented"]]
        comments_only = [r for r in rows if r["commented"] and not r["gave_gifts"]]
        return {
            "total_participants": len(rows),
            "gift_givers": len({r["uid"] for r in rows if r["gave_gifts"]}),
            "commenters": len({r["uid"] for r in rows if r["commented"]}),
            "both": len(both),
            "gifts_only": len(gifts_only),
            "comments_only": len(comments_only),
            "rows": rows,
        }
    def recent_flagged(self, limit=20):
        return list(self.flagged)[-limit:]

    def participant_uid_by_name(self, name):
        needle = _norm_id(name)
        if not needle:
            return None
        for m in self.messages:
            if _norm_id(m["nickname"]) == needle or _norm_id(m["uniqueId"]) == needle:
                return m["uniqueId"]
        return None

    def recent_messages_by_user(self, uid, nickname, limit=20):
        """Últimas mensagens de um usuário (por uid; cai para nickname).

        Usado pelo correlator presente<->chat para verificar as últimas
        mensagens do doador de um presente-alvo.
        """
        uid = _norm_id(uid)
        nickname = str(nickname or "").strip().lower()
        if not uid and not nickname:
            return []
        rows = []
        for m in self.messages:
            if uid and _norm_id(m["uniqueId"]) == uid:
                rows.append(m)
            elif nickname and not uid and _norm_id(m["nickname"]) == nickname:
                rows.append(m)
        return rows[-limit:]
