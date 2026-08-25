"""Regras determinísticas de moderação — port 1:1 de ``internal/moderation``.

O pacote Go foi removido; estas regras são o pré-filtro/fallback determinístico
que roda ANTES do RAG+LLM no pipeline do agente. O allowlist de falso-positivo
é carregado do ``FeedbackStore`` e atualizado a cada ``/feedback``.
"""

import re

from . import feedback as feedback_mod

CATEGORY_LABELS = {
    "PROSELITISMO": "Proselitismo Cristão",
    "SPAM": "Spam / propaganda (IA)",
    "GOLPE": "Possível golpe ou fraude (IA)",
    "ODIO": "Ódio / insulto grave (IA)",
    "PERGUNTA": "Pergunta / Dúvida (IA)",
    "OUTRO": "Conteúdo impróprio (IA)",
}

CATEGORIES = ("OK", "PERGUNTA", "PROSELITISMO", "ODIO", "SPAM", "GOLPE", "OUTRO")

_ALLOWLIST_LIMIT = 500


def category_label(category: str) -> str:
    return CATEGORY_LABELS.get(category, CATEGORY_LABELS["OUTRO"])


def fold_text(s: str) -> str:
    s = (s or "").lower().strip()
    s = s.replace("ç", "c")
    # Remove marcas combinantes (0x0300–0x036F).
    return "".join(ch for ch in s if not (0x0300 <= ord(ch) <= 0x036F))


def looks_question(comment: str) -> bool:
    raw = (comment or "").strip()
    if not raw:
        return False
    if any(ch in raw for ch in "?¿"):
        return True
    folded = fold_text(raw)
    starts_like = re.compile(
        r"^(pq|pk|por\s+que|porque|como|quando|onde|aonde|quem|qual|quais|q\b|sera\s+que|"
        r"pode|poderia|tem\s+como|da\s+pra|d[aá]\s+pra|isso\s+e|isso\s+eh|v[oô]ce\s+sabe|"
        r"alguem\s+sabe|algm\s+sabe|me\s+tira\s+uma\s+duvida|duvida\b|duvida:|duvida\s*[-:])"
    )
    if starts_like.search(folded):
        return True
    contains_cue = re.compile(
        r"\b(pq|pk|por\s+que|como\s+assim|quem\s+sabe|alguem\s+sabe|algm\s+sabe|"
        r"tem\s+como|da\s+pra|d[aá]\s+pra|sera\s+que|qual\s+o|qual\s+a)\b"
    )
    return contains_cue.search(folded) is not None


def passes_christian_proselytizing(comment: str) -> bool:
    t = fold_text(comment)
    jc = re.search(r"\b(jesus|cristo|jeova)\b", t) is not None
    afro_ctx = re.search(
        r"\b(candombl|umbanda|macumba|orixa[s]?|feitico[s]?|terreiro|og[aã]|vodum)\b", t
    ) is not None
    if jc and afro_ctx:
        return True
    jesus_salva = re.search(r"\b(jesus|cristo|deus)\s+salva\b", t) is not None
    so_jesus = re.search(r"\bso\s+(jesus|cristo|deus)(\s+salva)?\b", t) is not None
    if jesus_salva or so_jesus:
        return True
    salvacao = (
        re.search(r"\b(jesus|cristo|deus)\b", t) is not None
        and re.search(r"\b(salva[cç]ao|salva)\b", t) is not None
    )
    if salvacao:
        return True
    tension = re.search(
        r"\b(converter|salvacao|entregar|arrep|pecado|cruz|inferno|pregac|culto|pregador)\b", t
    ) is not None
    if jc and tension:
        return True
    deus_tension = (
        re.search(r"\bdeus\b", t) is not None
        and re.search(r"\b(converter|salvacao|inferno|pecado|cruz|arrep)\b", t) is not None
    )
    if deus_tension:
        return True
    if re.search(r"\bigreja\b", t) is not None or re.search(r"\bpastor\b", t) is not None:
        return True
    return False


_SHORTLINK_RE = re.compile(r"(?i)bit\.ly\/|tinyurl\.com\/|cutt\.ly\/|wa\.me\/|t\.me\/|telegram\.me\/")
_URL_RE = re.compile(r"(?i)https?:\/\/[^\s]+|www\.[^\s]+")
_TIKTOK_RE = re.compile(r"(?i)tiktok\.com|vm\.tiktok\.com|vt\.tiktok\.com")


def has_external_shortlink(raw: str) -> bool:
    return _SHORTLINK_RE.search(raw or "") is not None


def has_non_tiktok_http_link(raw: str) -> bool:
    urls = _URL_RE.findall(raw or "")
    if not urls:
        return False
    return any(_TIKTOK_RE.search(u) is None for u in urls)


def passes_spam_scam(raw: str, folded: str) -> bool:
    t = folded or fold_text(raw)
    if has_external_shortlink(raw) or has_non_tiktok_http_link(raw):
        return True
    patterns = [
        r"\b(pix\s+qrcode|pix\s+copia|mande\s+pix|clica\s+no\s+link|link\s+na\s+bio|link\s+do\s+perfil)\b",
        r"\b(ganhe\s+(dinheiro|gratis)|dinheiro\s+facil)\b",
    ]
    for p in patterns:
        if re.search(p, t):
            return True
    if re.search(r"\bcurso\s+gratis\b", t) and re.search(r"(?i)https?:\/\/", raw or ""):
        return True
    return False


def passes_regional_slur(folded: str) -> bool:
    return re.search(r"\b(testud[oa]|marmoteir[oa]|enganad[oa])\b", folded) is not None


def passes_personal_attack(folded: str) -> bool:
    if passes_regional_slur(folded):
        return True
    directed = re.search(
        r"\b(voc[eê]|voce\b|\bvc\b|\bce\b|tu\s+t[eá]|pra\s+voce\b|pra\s+voc[eê])\b", folded
    ) is not None
    insult_core = re.search(
        r"\b(idiota|imbecil|burr[oa]|estupid[oa]|nojent[oa]|noj[o]|lixo|palha[cç][oa]|"
        r"ridicul[oa]|inutil|fracassad[oa])\b",
        folded,
    ) is not None
    strong_slur = re.search(
        r"\b(filho\s+da\s+puta|filho\s+de\s+puta|fdp\b|vsf\b|vtnc\b|vai\s+(tomar\s+no\s+cu|"
        r"pro\s+inferno|a\s+merda)|se\s+fod(e|eu)|pau\s+no\s+cu|cuz[aã]o|escrot[oa])\b",
        folded,
    ) is not None
    threat_shut = re.search(
        r"\b(morre\b|apaga(\s+a\s+live)?|some(\s+daqui)?|cal[aá]\s+(a\s+)?boca|para\s+de\s+falar|"
        r"cala\s+boca|te\s+arrodo|te\s+quebro)\b",
        folded,
    ) is not None
    family_attack = (
        re.search(r"\b(sua\s+m[aã]e|teu\s+pai|tua\s+familia)\b", folded) is not None
        and re.search(r"\b(puta|viad[o]|burr[o]?)\b", folded) is not None
    )
    if strong_slur or threat_shut or family_attack:
        return True
    if directed and insult_core:
        return True
    return False


def classify_by_rules(comment: str, folded: str) -> dict:
    """Classifica com regras determinísticas. Prioridade igual à do Go."""
    if passes_personal_attack(folded):
        return {"flagged": True, "category": "ODIO", "reason": category_label("ODIO")}
    if passes_christian_proselytizing(comment):
        return {"flagged": True, "category": "PROSELITISMO", "reason": category_label("PROSELITISMO")}
    if passes_spam_scam(comment, folded):
        return {"flagged": True, "category": "SPAM", "reason": category_label("SPAM")}
    if looks_question(comment):
        return {"flagged": False, "category": "PERGUNTA"}
    return {"flagged": False, "category": "OK"}


class RulesEngine:
    """Classificador determinístico + allowlist de falso-positivo."""

    def __init__(self, feedback_store: "feedback_mod.FeedbackStore | None" = None):
        self._feedback = feedback_store
        self._allowlist: set[str] = set()
        self.refresh_allowlist()

    def refresh_allowlist(self) -> None:
        allow: set[str] = set()
        if self._feedback is not None:
            try:
                for comment in self._feedback.false_positive_comments(_ALLOWLIST_LIMIT):
                    folded = fold_text(comment)
                    if folded:
                        allow.add(folded)
            except Exception as exc:  # noqa: BLE001
                from logging import getLogger

                getLogger("agent.rules").warning("falha ao carregar allowlist: %s", exc)
        self._allowlist = allow

    def classify(self, comment: str) -> dict:
        comment = (comment or "").strip()
        folded = fold_text(comment)
        if folded in self._allowlist:
            return {"flagged": False, "category": "OK"}
        return classify_by_rules(comment, folded)
