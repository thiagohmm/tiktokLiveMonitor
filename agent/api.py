"""API HTTP do agente (FastAPI).

A composição dos componentes (buffer, cliente, modelo, consumidor SSE)
acontece no ``lifespan`` e é exposta via ``app.state`` — sem estado
global em módulo.
"""

import asyncio
import logging
from contextlib import asynccontextmanager

from fastapi import FastAPI, Request
from pydantic import BaseModel

from . import llm, sse, summary
from .buffer import MessageBuffer
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

    copilot = Copilot(model=model, registry=ToolRegistry(monitor=MonitorClient()), buffer=buffer)
    summarizer = summary.LiveSummarizer(model, buffer)
    sse_client = sse.SSEClient(buffer)
    sse_task = asyncio.create_task(sse_client.run())

    app.state.buffer = buffer
    app.state.copilot = copilot
    app.state.summarizer = summarizer
    app.state.sse = sse_client
    log.info("Consumidor SSE iniciado")
    try:
        yield
    finally:
        sse_client.stop()
        sse_task.cancel()
        try:
            await sse_task
        except asyncio.CancelledError:
            pass


app = FastAPI(title="TikTok Live Agent", lifespan=lifespan)


@app.get("/health")
async def health(request: Request):
    sse_client = request.app.state.sse
    return {
        "ok": True,
        "llm": True,
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