"""Roteamento de intenção, registro de ferramentas e resposta do copiloto.

- ``Router``: decide qual ferramenta usar (LLM ou heurística determinística).
- ``ToolRegistry``: execução das ferramentas (OCP: nova ferramenta = novo registro).
- ``Copilot``: orquestra rota + contexto + geração da resposta.
"""

import json
from dataclasses import dataclass
from typing import Awaitable, Callable

from . import config, llm, tools
from .buffer import LiveBuffer
from .context import ContextBuilder


# --- Registro de ferramentas (OCP) ---


@dataclass(frozen=True)
class Tool:
    name: str
    description: str
    handler: Callable[[str | None], Awaitable[dict]]
    requires_arg: bool = False

    async def invoke(self, arg: str | None) -> dict:
        if self.requires_arg and not arg:
            return {"error": f"argumento necessário para a ferramenta '{self.name}'"}
        return await self.handler(arg)


class ToolRegistry:
    """Mapeia nomes de ferramentas para handlers; extensível via ``register``."""

    def __init__(self, monitor: tools.MonitorClient | None = None):
        self._tools: dict[str, Tool] = {}
        monitor = monitor or tools.default
        self.register("ranking", "ranking de engajamento dos participantes", monitor.ranking)
        self.register(
            "profile",
            "perfil completo de um usuário (mensagens, presentes, risco)",
            monitor.profile,
            requires_arg=True,
        )
        self.register("gifts", "presentes enviados na live", monitor.gifts)
        self.register("history", "histórico de moderação", lambda arg: monitor.history())
        self.register("state", "estado atual da live", lambda arg: monitor.state())
        self.register("report", "relatório pós-live", monitor.report)
        self.register("pinned", "comentários fixados", lambda arg: monitor.pinned())
        self.register(
            "target_gifts",
            "presentes-alvo pendentes/respondidos",
            lambda arg: monitor.target_gifts(True),
        )

    def register(
        self,
        name: str,
        description: str,
        handler: Callable[[str | None], Awaitable[dict]],
        *,
        requires_arg: bool = False,
    ) -> "ToolRegistry":
        self._tools[name] = Tool(name, description, handler, requires_arg)
        return self

    @property
    def names(self) -> tuple[str, ...]:
        return tuple(self._tools)

    def prompt_lines(self) -> list[str]:
        return [f"- {t.name}: {t.description}" for t in self._tools.values()]

    async def run(self, tool: str, arg: str | None) -> dict:
        entry = self._tools.get(tool)
        if entry is None:
            return {"error": f"ferramenta desconhecida: {tool}"}
        return await entry.invoke(arg)


# --- Roteamento determinístico (data-driven, OCP) ---


@dataclass(frozen=True)
class _Rule:
    tool: str
    keywords: tuple[str, ...]
    arg_extractor: Callable[[str], str | None] | None = None


def _extract_name(question):
    lowered = question.lower()
    for marker in ("perfil de ", "profile de ", "quem é ", "quem e ", "me fala de ", "fala de "):
        index = lowered.find(marker)
        if index >= 0:
            rest = question[index + len(marker):].strip()
            token = rest.split()[0] if rest else ""
            name = token.strip("?.,!;:")
            return name or None
    return None


_DETERMINISTIC_RULES = (
    _Rule("ranking", ("ranking", "engajamento", "classificação", "classificacao", "top participantes", "top fãs", "top fas")),
    _Rule("profile", ("perfil", "profile", "quem é", "quem e", "usuário", "usuario", "me fala de", "fala de"), _extract_name),
    _Rule("gifts", ("presente", "presentes", "gift", "gifts")),
    _Rule("history", ("moderação", "moderacao", "flagado", "flagados", "histórico", "historico", "alerta", "alertas")),
    _Rule("report", ("relatório", "relatorio", "report")),
    _Rule("pinned", ("fixado", "fixados", "pinned")),
    _Rule("target_gifts", ("presente-alvo", "presente alvo", "target gift", "target gifts", "meta de presente")),
    _Rule("state", ("estado", "status", "ao vivo", "conectado", "live atual")),
)


def _deterministic_route(question):
    q = question.lower()
    for rule in _DETERMINISTIC_RULES:
        if any(word in q for word in rule.keywords):
            arg = rule.arg_extractor(question) if rule.arg_extractor else None
            return (rule.tool, arg)
    return (None, None)


# --- Roteamento via LLM ---

_ROUTE_SYSTEM_TEMPLATE = (
    "Você decide qual ferramenta usar para responder a pergunta de um streamer de live.\n"
    "Ferramentas disponíveis:\n"
    "{tools}\n"
    "- none: responder com os dados do buffer (mensagens/presentes/repetições)\n\n"
    "Responda com EXATAMENTE uma linha no formato:\n"
    "TOOL:<nome>  ou  TOOL:<nome>:<argumento>  ou  NONE\n"
    "Exemplos:\n"
    "TOOL:ranking\n"
    "TOOL:profile:joao\n"
    "NONE\n"
    "Sem explicações."
)

_FINAL_SYSTEM = (
    "Você é o copiloto de uma live do TikTok. Responda de forma direta, concisa e em "
    "português do Brasil, exclusivamente com base nos dados fornecidos."
)

_DEFAULT_TOOL_NAMES = frozenset(
    ("ranking", "profile", "gifts", "history", "state", "report", "pinned", "target_gifts")
)


def _parse_llm_route(raw, allowed=_DEFAULT_TOOL_NAMES):
    raw = (raw or "").strip()
    if not raw or raw.upper() == "NONE":
        return (None, None)
    parts = raw.split(":", 2)
    if len(parts) < 2 or parts[0].strip().upper() != "TOOL":
        return (None, None)
    tool = parts[1].strip().lower()
    arg = parts[2].strip() if len(parts) > 2 else None
    if tool not in allowed:
        return (None, None)
    return (tool, arg or None)


class Router:
    """Decide qual ferramenta usar para responder à pergunta do streamer."""

    def __init__(self, model: llm.ChatModel, registry: ToolRegistry | None = None):
        self._model = model
        self._registry = registry or ToolRegistry()

    def _route_prompt(self):
        tools_lines = "\n".join(self._registry.prompt_lines())
        return _ROUTE_SYSTEM_TEMPLATE.format(tools=tools_lines)

    async def route(self, question):
        messages = [
            {"role": "system", "content": self._route_prompt()},
            {"role": "user", "content": question},
        ]
        raw = await self._model.chat(messages, max_tokens=16, temperature=0.0)
        tool, arg = _parse_llm_route(raw, self._registry.names)
        if tool:
            return (tool, arg)
        return _deterministic_route(question)


class Copilot:
    """Orquestra: roteia, executa a ferramenta (ou usa o buffer) e responde."""

    def __init__(
        self,
        model: llm.ChatModel,
        registry: ToolRegistry | None = None,
        buffer: LiveBuffer | None = None,
        embedder=None,
        store=None,
    ):
        self._model = model
        self._registry = registry or ToolRegistry()
        self._router = Router(model, self._registry)
        self._buffer = buffer
        self._context = ContextBuilder(buffer) if buffer is not None else None
        self._embedder = embedder
        self._store = store

    async def _rag_context(self, question: str) -> str:
        """Busca semântica sobre chat + corpus de moderação para grounding."""
        if self._embedder is None or self._store is None:
            return ""
        try:
            vecs = await self._embedder.embed([question])
        except Exception:  # noqa: BLE001
            return ""
        if not vecs:
            return ""
        results = self._store.search(
            vecs[0], k=config.RAG_TOP_K, sources=("chat", "feedback", "anomaly")
        )
        if not results:
            return ""
        lines = [f'- [{r["category"]}] {r["comment"]}' for r in results]
        return "CONTEXTO RECUPERADO (RAG):\n" + "\n".join(lines)

    async def ask(self, question: str) -> str:
        tool, arg = await self._router.route(question)

        if tool == "profile" and not arg and self._buffer is not None:
            arg = self._buffer.participant_uid_by_name(question)

        if tool:
            try:
                result = await self._registry.run(tool, arg)
                result_text = json.dumps(result, ensure_ascii=False, indent=2)
            except Exception as exc:  # noqa: BLE001
                result_text = f"Erro ao executar a ferramenta {tool}: {exc}"
            context = f"RESULTADO DA FERRAMENTA {tool}:\n{result_text}"
        else:
            context = self._context.build() if self._context is not None else ""
            rag = await self._rag_context(question)
            if rag:
                context = f"{context}\n\n{rag}" if context else rag

        messages = [
            {"role": "system", "content": _FINAL_SYSTEM},
            {"role": "user", "content": f"{context}\n\nPergunta do streamer: {question}"},
        ]
        return await self._model.chat(messages, max_tokens=512)
