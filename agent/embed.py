"""Embeddings de texto via fastembed (ONNX), com contrato abstrato (DIP/ISP).

Define o protocolo ``Embedder`` para que o restante do pacote dependa da
abstração, não da implementação (assim como ``llm.ChatModel``).
"""

import asyncio
import logging
from typing import Protocol, runtime_checkable

from . import config

log = logging.getLogger("agent.embed")


@runtime_checkable
class Embedder(Protocol):
    """Contrato mínimo para quem gera vetores de texto."""

    async def embed(self, texts: list[str]) -> list[list[float]]:
        """Retorna um vetor por texto, na mesma ordem."""


class FastembedEmbedder:
    """Implementação de ``Embedder`` baseada no fastembed (ONNX, sem torch)."""

    def __init__(self, model_name=None, cache_dir=None):
        # DIP: valores por injeção, default vindo de config.
        self._model_name = model_name or config.FASTEMBED_MODEL
        self._cache_dir = cache_dir or config.FASTEMBED_CACHE
        self._model = None

    def _ensure(self):
        if self._model is None:
            from fastembed import TextEmbedding

            log.info(
                "Carregando modelo de embeddings %s (cache=%s)",
                self._model_name,
                self._cache_dir,
            )
            self._model = TextEmbedding(
                model_name=self._model_name,
                cache_dir=self._cache_dir,
            )
        return self._model

    def _embed_sync(self, texts: list[str]) -> list[list[float]]:
        model = self._ensure()
        out: list[list[float]] = []
        for vec in model.embed(list(texts)):
            out.append([float(x) for x in vec])
        return out

    async def embed(self, texts: list[str]) -> list[list[float]]:
        if not texts:
            return []
        # fastembed é síncrono e CPU-bound: roda em thread para não travar o loop.
        return await asyncio.to_thread(self._embed_sync, texts)
