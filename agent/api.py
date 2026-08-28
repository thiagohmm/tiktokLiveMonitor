"""API HTTP do agente (FastAPI).

A composição dos componentes (buffer, cliente, modelo, worker LLM, feedback,
consumidor SSE) acontece no ``lifespan`` e é exposta via ``app.state`` — sem
estado global em módulo.

Endpoints compatíveis com o assistente Go (Fase 1 da unificação):
- ``POST /ask-ai``, ``GET /probe-llm``, ``POST /worker/register``, ``POST /feedback``.
"""

import asyncio
import logging
from contextlib import asynccontextmanager

from fastapi import FastAPI, Request
from fastapi.responses import JSONResponse
from pydantic import BaseModel

from . import config, correlate, embed, feedback, llm, moderate, sse, summary, vectors
from .buffer import MessageBuffer
from .history import AskAIService
from .llm_worker import LlamaWorker
from .router import Copilot, ToolRegistry
from .rules import RulesEngine
from .tools import MonitorClient

log = logging.getLogger("agent.api")


class AskRequest(BaseModel):
    question: str


class AskResponse(BaseModel):
    answer: str


class SummarizeResponse(BaseModel):
    summary: str


@asynccontextmanager
async def lifespan(app: FastAPI):
    buffer = MessageBuffer()
    model = llm.LlamaServerChatModel()
    monitor = MonitorClient()
    worker = LlamaWorker()
    store = feedback.FeedbackStore(config.FEEDBACK_DB)
    rules = RulesEngine(store)

    embedder = None
    vector_store = None
    try:
        embedder = embed.FastembedEmbedder()
    except Exception as exc:  # noqa: BLE001
        log.warning("embedder indisponível: %s", exc)
    if embedder is not None:
        try:
            vector_store = vectors.VectorStore(config.FEEDBACK_DB)
        except Exception as exc:  # noqa: BLE001
            log.warning("vector store indisponível: %s", exc)
            vector_store = None

    moderator = moderate.RagModerator(
        embedder=embedder, store=vector_store, model=model, rules=rules, monitor=monitor
    )

    # Correlação presente<->chat usa um cliente LLM com timeout curto:
    # o monitor Go aguarda no máximo 8s e cai para heurística local.
    correlate_model = llm.LlamaServerChatModel(timeout=config.CORRELATE_TIMEOUT)
    correlator = correlate.GiftQuestionCorrelator(model=correlate_model, buffer=buffer)

    ask_ai = AskAIService(model=model, monitor=monitor, buffer=buffer)
    copilot = Copilot(
        model=model,
        registry=ToolRegistry(monitor=monitor),
        buffer=buffer,
        embedder=embedder,
        store=vector_store,
    )
    summarizer = summary.LiveSummarizer(model, buffer)
    sse_client = sse.SSEClient([buffer, moderator])

    app.state.buffer = buffer
    app.state.worker = worker
    app.state.feedback = store
    app.state.ask_ai = ask_ai
    app.state.copilot = copilot
    app.state.summarizer = summarizer
    app.state.correlator = correlator
    app.state.sse = sse_client
    app.state.moderator = moderator
    app.state.embedder = embedder
    app.state.vector_store = vector_store

    sse_task = asyncio.create_task(sse_client.run())
    spawn_task = asyncio.create_task(worker.ensure_ready())
    moderator.start()
    backfill_task = asyncio.create_task(
        vectors.backfill(embedder, vector_store, store, config.FEEDBACK_DB, config.RAG_BACKFILL_LIMIT)
    )
    try:
        state = await monitor.state()
        moderator.set_settings(state.get("settings") or {})
    except Exception as exc:  # noqa: BLE001
        log.warning("falha ao ler settings iniciais: %s", exc)
    log.info("Consumidor SSE iniciado; worker llama-server em spawn; moderação RAG ativa")
    try:
        yield
    finally:
        sse_client.stop()
        sse_task.cancel()
        try:
            await sse_task
        except asyncio.CancelledError:
            pass
        backfill_task.cancel()
        try:
            await backfill_task
        except asyncio.CancelledError:
            pass
        await moderator.stop()
        await spawn_task
        await worker.stop()
        if vector_store is not None:
            vector_store.close()
        store.close()


async def _index_feedback(state, comment: str, expected: str) -> None:
    """Indexa um feedback corrigido no vector store (fonte 'feedback')."""
    vector_store = getattr(state, "vector_store", None)
    embedder = getattr(state, "embedder", None)
    if vector_store is None or embedder is None:
        return
    try:
        vecs = await embedder.embed([comment])
        if vecs:
            vector_store.upsert("feedback", comment, vectors.expected_to_category(expected), vecs[0])
    except Exception as exc:  # noqa: BLE001
        log.warning("falha ao indexar feedback: %s", exc)


app = FastAPI(title="TikTok Live Agent", lifespan=lifespan)


@app.get("/health")
async def health(request: Request):
    sse_client = request.app.state.sse
    worker = request.app.state.worker
    return {
        "ok": True,
        "llm": worker.ready,
        "sseConnected": sse_client.connected if sse_client is not None else False,
    }


@app.post("/ask", response_model=AskResponse)
async def ask(req: AskRequest, request: Request):
    question = req.question.strip()
    if not question:
        return {"answer": "Pergunta vazia."}
    answer = await request.app.state.copilot.ask(question)
    return {"answer": answer}


@app.get("/summarize", response_model=SummarizeResponse)
async def summarize(request: Request):
    text = await request.app.state.summarizer.summarize()
    return {"summary": text}


# --- Endpoints compatíveis com o assistente Go (Fase 1) ---


@app.get("/probe-llm")
async def probe_llm(request: Request):
    """Mesmo contrato do ``/api/probe-llm`` Go."""
    ready, error = await request.app.state.worker.ensure_ready()
    if error:
        return {"llmActive": False, "error": error}
    return {"llmActive": ready}


@app.post("/worker/register")
async def worker_register(request: Request):
    """Mesmo contrato do ``/api/worker/register`` Go."""
    try:
        body = await request.json()
        host = str(body.get("host") or "")
        port = int(body.get("port") or 0)
    except Exception:  # noqa: BLE001
        return JSONResponse(status_code=400, content={"error": "invalid body"})
    if not host or port <= 0:
        return JSONResponse(status_code=400, content={"error": "invalid body"})
    request.app.state.worker.register_remote(host, port)
    return {"success": True}


@app.post("/feedback")
async def post_feedback(request: Request):
    """Mesmo contrato do ``/api/feedback`` Go."""
    try:
        body = await request.json()
    except Exception:  # noqa: BLE001
        return JSONResponse(status_code=400, content={"error": "invalid body"})
    comment = str(body.get("comment") or "")
    category = str(body.get("category") or "")
    expected = str(body.get("expected") or "")
    try:
        request.app.state.feedback.add(comment, category, expected)
    except feedback.FeedbackError as exc:
        return JSONResponse(status_code=400, content={"error": str(exc)})
    await _index_feedback(request.app.state, comment, expected)
    return {"success": True}


@app.post("/moderate")
async def moderate_comment(request: Request):
    """Classifica um comentário (regras + RAG + LLM) sem reportar flag."""
    try:
        body = await request.json()
    except Exception:  # noqa: BLE001
        return JSONResponse(status_code=400, content={"error": "invalid body"})
    comment = str(body.get("comment") or "").strip()
    if not comment:
        return JSONResponse(status_code=400, content={"error": "comment is required"})
    moderator = getattr(request.app.state, "moderator", None)
    if moderator is None:
        return JSONResponse(status_code=503, content={"error": "moderation unavailable"})
    try:
        result = await moderator.classify(comment)
    except Exception as exc:  # noqa: BLE001
        return JSONResponse(status_code=503, content={"error": f"moderation unavailable: {exc}"})
    return result


@app.post("/correlate-gift")
async def correlate_gift(request: Request):
    """Correlação presente-alvo <-> chat (chamado pelo monitor Go).

    O monitor só chama quando o presente é um presente-alvo escolhido pelo
    streamer (``isTarget``); caso contrário a função não é ativada.
    """
    try:
        body = await request.json()
    except Exception:  # noqa: BLE001
        return JSONResponse(status_code=400, content={"error": "invalid body"})
    if not isinstance(body, dict):
        return JSONResponse(status_code=400, content={"error": "invalid body"})
    gift = body.get("gift")
    if not isinstance(gift, dict) or not str(gift.get("giftName") or "").strip():
        return JSONResponse(status_code=400, content={"error": "gift.giftName is required"})
    if body.get("isTarget") is False:
        return {"match": None, "method": "", "confidence": ""}
    candidates = body.get("candidates")
    if not isinstance(candidates, list):
        candidates = None
    correlator = getattr(request.app.state, "correlator", None)
    if correlator is None:
        return JSONResponse(status_code=503, content={"error": "correlation unavailable"})
    try:
        match = await correlator.correlate(gift, candidates)
    except Exception as exc:  # noqa: BLE001
        return JSONResponse(status_code=500, content={"error": f"correlation error: {exc}"})
    if match is None:
        return {"match": None, "method": "", "confidence": ""}
    return {
        "match": {
            "uniqueId": match.get("uniqueId", ""),
            "nickname": match.get("nickname", ""),
            "comment": match.get("comment", ""),
            "timestamp": match.get("timestamp"),
            "isFollower": bool(match.get("isFollower")),
        },
        "method": match.get("method", ""),
        "confidence": match.get("confidence", ""),
    }


@app.post("/ask-ai")
async def ask_ai(request: Request):
    """Mesmo contrato do ``/api/ask-ai`` Go (com histórico por sessão)."""
    try:
        body = await request.json()
    except Exception:  # noqa: BLE001
        return JSONResponse(status_code=400, content={"error": "invalid body"})
    question = str(body.get("question") or "").strip()
    if not question:
        return JSONResponse(status_code=400, content={"error": "question is required"})
    session = str(body.get("session") or "default") or "default"
    try:
        answer = await request.app.state.ask_ai.ask(question, session=session)
    except Exception as exc:  # noqa: BLE001
        return JSONResponse(status_code=500, content={"error": f"AI error: {exc}"})
    return {"question": question, "answer": answer}
