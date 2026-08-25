"""Cliente assíncrono das APIs REST do monitor.

Encapsulado em ``MonitorClient`` para permitir injeção da URL base (DIP)
e testes com um servidor fake.
"""

import httpx

from . import config


class MonitorClient:
    """Acceso à API do monitor de live."""

    def __init__(self, base_url=None, timeout=30.0):
        self._base_url = (base_url or config.MONITOR_URL).rstrip("/")
        self._timeout = timeout

    async def _get(self, path, params=None):
        async with httpx.AsyncClient(timeout=self._timeout) as client:
            resp = await client.get(self._base_url + path, params=params)
            resp.raise_for_status()
            return resp.json()

    async def _post(self, path, payload):
        async with httpx.AsyncClient(timeout=self._timeout) as client:
            resp = await client.post(self._base_url + path, json=payload)
            resp.raise_for_status()
            return resp.json()

    async def state(self):
        return await self._get("/api/state")

    async def ranking(self, live=None):
        return await self._get("/api/ranking", params={"live": live} if live else None)

    async def profile(self, uid):
        return await self._get("/api/profile", params={"uid": uid})

    async def gifts(self, user=None):
        return await self._get("/api/gifts", params={"user": user} if user else None)

    async def history(self):
        return await self._get("/api/history")

    async def report(self, live=None):
        return await self._get("/api/report", params={"live": live} if live else None)

    async def pinned(self):
        return await self._get("/api/pinned-comments")

    async def target_gifts(self, pending=True):
        return await self._get("/api/target-gift-history", params={"pending": "1" if pending else "0"})

    async def flag(self, payload):
        return await self._post("/api/moderation/flag", payload)


# Instância padrão para conveniência.
default = MonitorClient()