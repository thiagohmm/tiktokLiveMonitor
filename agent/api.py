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

from . import config, feedback, llm, sse, summary
from .buffer import MessageBuffer
from .history import AskAIService
from .llm_worker import LlamaWorker
from .router import Copilot, ToolRegistry
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
    ask_ai = AskAIService(model=model, monitor=monitor, buffer=buffer)
    copilot = Copilot(model=model, registry=ToolRegistry(monitor=monitor), buffer=buffer)
    summarizer = summary.LiveSummarizer(model, buffer)
    sse_client = sse.SSEClient(buffer)

    app.state.buffer = buffer
    app.state.worker = worker
    app.state.feedback = store
    app.state.ask_ai = ask_ai
    app.state.copilot = copilot
    app.state.summarizer = summarizer
    app.state.sse = sse_client

    sse_task = asyncio.create_task(sse_client.run())
    spawn_task = asyncio.create_task(worker.ensure_ready())
    log.info("Consumidor SSE iniciado; worker llama-server em spawn")
    try:
        yield
    finally:
        sse_client.stop()
        sse_task.cancel()
        try:
            await sse_task
        except asyncio.CancelledError:
            pass
        await spawn_task
        await worker.stop()
        store.close()


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
    try:
        request.app.state.feedback.add(
            str(body.get("comment") or ""),
            str(body.get("category") or ""),
            str(body.get("expected") or ""),
        )
    except feedback.FeedbackError as exc:
        return JSONResponse(status_code=400, content={"error": str(exc)})
    return {"success": True}


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
