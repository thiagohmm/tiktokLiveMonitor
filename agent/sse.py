"""Consumidor SSE dos eventos do monitor.

Depende do contrato ``EventSink`` (DIP/ISP) e recebe a URL por injeção.
"""

import asyncio
import json
import logging

import httpx

from . import config
from .buffer import EventSink

log = logging.getLogger("agent.sse")


class SSEClient:
    def __init__(self, sink: EventSink | list[EventSink], base_url: str | None = None):
        self._sinks = sink if isinstance(sink, (list, tuple)) else [sink]
        self._base_url = (base_url or config.MONITOR_URL).rstrip("/")
        self.connected = False
        self._stop = asyncio.Event()

    def _dispatch(self, event_type: str, data) -> None:
        for sink in self._sinks:
            sink.ingest(event_type, data)

    async def run(self):
        backoff = 1.0
        while not self._stop.is_set():
            try:
                await self._stream()
            except asyncio.CancelledError:
                raise
            except Exception as exc:  # noqa: BLE001
                log.warning("SSE desconectado: %s", exc)
            self.connected = False
            try:
                await asyncio.wait_for(self._stop.wait(), timeout=backoff)
                return
            except asyncio.TimeoutError:
                pass
            backoff = min(backoff * 2, 30.0)

    async def _stream(self):
        timeout = httpx.Timeout(connect=10.0, read=None, write=None, pool=10.0)
        url = self._base_url + "/events"
        async with httpx.AsyncClient(timeout=timeout) as client:
            async with client.stream("GET", url) as resp:
                resp.raise_for_status()
                self.connected = True
                log.info("SSE conectado em %s", url)
                event_type = None
                async for line in resp.aiter_lines():
                    if self._stop.is_set():
                        break
                    if line.startswith("event:"):
                        event_type = line[len("event:"):].strip()
                    elif line.startswith("data:"):
                        raw = line[len("data:"):].strip()
                        if raw and event_type:
                            try:
                                data = json.loads(raw)
                            except json.JSONDecodeError:
                                continue
                            self._dispatch(event_type, data)
        self.connected = False

    def stop(self):
        self._stop.set()
