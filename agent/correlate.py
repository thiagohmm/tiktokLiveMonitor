"""Correlação presente <-> chat (presente-alvo x pergunta do doador).

Quando um participante envia um presente-alvo, a IA verifica as últimas
mensagens do usuário e escolhe a mensagem que melhor se encaixa como a
pergunta "referente ao presente dado". A pergunta pode ter chegado ANTES
do presente (caso coberto aqui); o caso presente-antes-da-pergunta é
complementado pela revisão avante no monitor Go.

Fluxo de decisão:
1. candidata única -> caminho determinístico (sem custo de LLM);
2. múltiplas candidatas -> o LLM escolhe o melhor encaixe;
3. LLM indisponível -> fallback heurístico (a mensagem que mais parece
   pergunta, priorizando as mais recentes).

Importante: esta função só é acionada pelo monitor quando o presente é um
"presente-alvo" escolhido pelo streamer (``isTarget``). Sem presente-alvo
escolhido, a correlação não é ativada.
"""

import logging
import re

log = logging.getLogger("agent.correlate")

MAX_CANDIDATES = 8
RECENT_BUFFER_LIMIT = 12

_QUESTION_CUES = re.compile(
    r"\b(pq\b|pk\b|por\s+que|porque|como\b|quando|onde|aonde|quem\b|qual\b|quais\b|"
    r"duvida|tem\s+como|da\s+pra|d[aá]\s+pra|me\s+tira\s+uma\s+duvida)\b",
    re.IGNORECASE,
)


class CorrelatorModel:
    """Protocolo mínimo do LLM reutilizado pelo correlator (DIP/ISP)."""

    async def chat(self, messages, max_tokens=512, temperature=0.1) -> str:  # pragma: no cover
        raise NotImplementedError


def _norm_id(value):
    return str(value or "").strip().lower()


def looks_like_question(text) -> bool:
    text = (text or "").strip()
    if not text:
        return False
    if "?" in text or "¿" in text:
        return True
    return bool(_QUESTION_CUES.search(text))


def score_candidate(candidate) -> float:
    """Pontuação determinística de 'quanto a mensagem parece a pergunta a responder'."""
    text = str(candidate.get("comment") or "").strip()
    if not text:
        return 0.0
    score = 0.0
    if looks_like_question(text):
        score += 3.0
    if "?" in text:
        score += 1.0
    if 8 <= len(text) <= 220:
        score += 0.5
    return score


_SYSTEM = (
    "Você é o assistente de correlação presente<->chat de uma live do TikTok.\n"
    "Um participante enviou um presente-alvo e, na janela recente do chat, existem "
    "mensagens candidatas (normalmente do próprio doador). Escolha a mensagem que "
    "melhor se encaixa como a pergunta/fala REFERENTE ao presente enviado — "
    "normalmente é uma pergunta que o doador quer que o streamer responda (por ter "
    "enviado o presente) ou que cita o presente / a ação de presentear.\n\n"
    "Responda com EXATAMENTE uma linha no formato:\n"
    "MATCH:<numero>   (índice da mensagem, começando em 1)   ou   NONE\n"
    "Sem explicações."
)


def build_user_prompt(gift, candidates) -> str:
    gift_user = str(gift.get("nickname") or gift.get("uniqueId") or "?")
    lines = [
        f"Presente enviado: {gift.get('giftName') or '?'} por {gift_user}",
        "",
        "Mensagens candidatas do chat (do mais antigo para o mais recente):",
    ]
    for i, cand in enumerate(candidates, start=1):
        who = cand.get("nickname") or cand.get("uniqueId") or "?"
        lines.append(f"{i}. [{who}] {cand.get('comment') or ''}")
    return "\n".join(lines)


def parse_pick(raw, count):
    """Interpreta a resposta do LLM; retorna índice (0-based) ou None."""
    text = (raw or "").strip().upper()
    for token in re.split(r"[\n;]+", text):
        token = token.strip()
        if token.startswith("MATCH"):
            digits = re.sub(r"\D", "", token.split("MATCH", 1)[1])
            if digits:
                idx = int(digits)
                if 1 <= idx <= count:
                    return idx - 1
            return None
        if token == "NONE":
            return None
    return None


class GiftQuestionCorrelator:
    """Escolhe a melhor pergunta/mensagem do doador de um presente-alvo.

    SRP: única responsabilidade de correlação presente<->chat. O contexto
    (LLM, buffer do agente) chega por injeção para permitir testes.
    """

    def __init__(self, model=None, buffer=None):
        self._model = model
        self._buffer = buffer

    # --- candidatos ---

    def buffer_candidates(self, gift) -> list:
        """Últimas mensagens do doador vindas do próprio buffer do agente.

        O agente consome o mesmo fluxo SSE do monitor, portanto possui as
        mensagens do usuário. Usado quando o monitor não envia candidatos.
        """
        if self._buffer is None:
            return []
        uid = _norm_id(gift.get("uniqueId"))
        nickname = str(gift.get("nickname") or "").strip().lower()
        if not uid and not nickname:
            return []
        getter = getattr(self._buffer, "recent_messages_by_user", None)
        if not callable(getter):
            return []
        return list(getter(uid, nickname, RECENT_BUFFER_LIMIT))

    @staticmethod
    def _normalize(candidates) -> list:
        out = []
        for cand in candidates or []:
            if not isinstance(cand, dict):
                continue
            comment = str(cand.get("comment") or "").strip()
            if not comment:
                continue
            out.append({
                "uniqueId": str(cand.get("uniqueId") or ""),
                "nickname": str(cand.get("nickname") or ""),
                "comment": comment,
                "timestamp": cand.get("timestamp"),
                "isFollower": bool(cand.get("isFollower")),
            })
        if len(out) > MAX_CANDIDATES:
            out = out[-MAX_CANDIDATES:]
        return out

    # --- LLM ---

    async def _llm_pick(self, gift, candidates):
        messages = [
            {"role": "system", "content": _SYSTEM},
            {"role": "user", "content": build_user_prompt(gift, candidates)},
        ]
        raw = await self._model.chat(messages, max_tokens=16, temperature=0.0)
        return parse_pick(raw, len(candidates))

    # --- API pública ---

    async def correlate(self, gift, candidates=None):
        """Retorna ``{...mensagem, method, confidence}`` ou ``None``."""
        cands = self._normalize(candidates)
        if not cands:
            cands = self._normalize(self.buffer_candidates(gift))
        if not cands:
            return None

        # Caminho determinístico: candidata única não precisa de LLM.
        if len(cands) == 1:
            cand = cands[0]
            confidence = "high" if looks_like_question(cand["comment"]) else "medium"
            return {**cand, "method": "single-candidate", "confidence": confidence}

        if self._model is not None:
            try:
                idx = await self._llm_pick(gift, cands)
            except Exception as exc:  # noqa: BLE001
                log.warning("LLM indisponível na correlação presente<->chat: %s", exc)
                idx = None
            if idx is not None:
                cand = cands[idx]
                confidence = "high" if looks_like_question(cand["comment"]) else "medium"
                return {**cand, "method": "llm", "confidence": confidence}

        best = max(cands, key=lambda c: (score_candidate(c), c.get("timestamp") or 0))
        confidence = "medium" if looks_like_question(best["comment"]) else "low"
        return {**best, "method": "heuristic-fallback", "confidence": confidence}