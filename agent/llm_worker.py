"""Gestão do worker LLM (llama-server) — migração de ``internal/ai.Manager``.

O agente Python passa a ser o dono do processo ``llama-server``: spawn com o
modelo de ``model-config.json``, sondagem de saúde, restart em caso de queda e
shutdown limpo no ``lifespan`` da API.

Contratos mantidos (compatibilidade com os endpoints Go):
- ``ensure_ready()``  ≈ ``Manager.ProbeReady`` (GET /probe-llm).
- ``register_remote()`` ≈ ``Manager.RegisterWorker`` (POST /worker/register).
"""

import asyncio
import json
import logging
import os
import platform

import httpx

from . import config

log = logging.getLogger("agent.llm_worker")

HEALTH_MAX_TRIES = 90
HEALTH_RETRY_S = 2.0
HEALTH_TIMEOUT_S = 2.0
RESTART_DELAY_S = 15.0

# Registro de modelos (espelha o mapa ``config.Models`` do Go):
# chave de ``model-config.json`` → nome do arquivo GGUF.
_MODEL_REGISTRY = {
    "gemma-4b": "gemma-4-E4B-it-Q4_K_M.gguf",
    "llama-3.2-3b": "Llama-3.2-3B-Instruct-Q4_K_M.gguf",
}
_DEFAULT_MODEL_KEY = "gemma-4b"


def _go_arch() -> str:
    """Mapeia a arquitetura da máquina para o formato GOARCH (ex.: x86_64 → amd64)."""
    machine = platform.machine().lower()
    return {"x86_64": "amd64", "i386": "386", "i686": "386"}.get(machine, machine)


class LlamaWorker:
    """Ciclo de vida do worker llama-server (local ou remoto)."""

    def __init__(
        self,
        port: int | None = None,
        bin_dir: str | None = None,
        models_dir: str | None = None,
        model_config: str | None = None,
        bind_host: str | None = None,
        use_mmap: bool | None = None,
        ctx_size: int | None = None,
    ):
        self.port = port or config.LLM_PORT
        self._bin_dir = bin_dir or config.LLM_BIN_DIR
        self._models_dir = models_dir or config.MODELS_DIR
        self._model_config = model_config or config.MODEL_CONFIG
        self._use_mmap = config.LLM_USE_MMAP if use_mmap is None else use_mmap
        self._ctx_size = ctx_size or config.LLM_CTX_SIZE

        bind_host = bind_host or config.LLM_BIND
        if not bind_host:
            # Mesmo critério do Go: container → 0.0.0.0, senão loopback.
            bind_host = "0.0.0.0" if os.path.exists("/.dockerenv") else "127.0.0.1"
        self._bind_host = bind_host

        self.host = bind_host
        self.port = self.port
        self.is_local = True
        self.ready = False
        self._process: asyncio.subprocess.Process | None = None
        self._spawning = False
        self._stopping = False
        self._watch_task: asyncio.Task | None = None

    # --- Resolução de caminhos (espelha ``Manager.resolvePaths``) ---

    def resolve_bin(self) -> str:
        bin_name = "llama-server.exe" if platform.system() == "Windows" else "llama-server"
        arch_dir = os.path.join(self._bin_dir, platform.system().lower(), _go_arch())
        candidates = [
            os.path.join(arch_dir, bin_name),
            os.path.join(arch_dir, "build", "bin", bin_name),
        ]
        for path in candidates:
            if os.path.exists(path):
                return path
        # Diretórios aninhados (mesma estratégia do Go).
        if os.path.isdir(arch_dir):
            for entry in sorted(os.listdir(arch_dir)):
                nested = os.path.join(arch_dir, entry, bin_name)
                if os.path.exists(nested):
                    return nested
        return candidates[0]

    def resolve_model(self) -> str:
        selected = ""
        try:
            with open(self._model_config, encoding="utf-8") as fh:
                selected = str((json.load(fh) or {}).get("selectedModel") or "").strip()
        except (OSError, ValueError):
            selected = ""
        if not selected:
            selected = _DEFAULT_MODEL_KEY

        # Prefere o arquivo do modelo selecionado (registro espelhado do Go).
        filename = _MODEL_REGISTRY.get(selected)
        if filename:
            candidate = os.path.join(self._models_dir, filename)
            if os.path.exists(candidate):
                return candidate

        # Fallback: auto-detecção de .gguf no diretório de modelos.
        if os.path.isdir(self._models_dir):
            names = sorted(os.listdir(self._models_dir))
            for name in names:
                if name.lower().endswith(".gguf") and selected.lower() in name.lower():
                    return os.path.join(self._models_dir, name)
            for name in names:
                if name.lower().endswith(".gguf"):
                    return os.path.join(self._models_dir, name)
        return os.path.join(self._models_dir, _MODEL_REGISTRY[_DEFAULT_MODEL_KEY])

    # --- Saúde (espelha ``Worker.checkHealth``) ---

    async def check_health(self) -> bool:
        url = f"http://{self.host}:{self.port}/health"
        try:
            async with httpx.AsyncClient(timeout=HEALTH_TIMEOUT_S) as client:
                resp = await client.get(url)
            self.ready = resp.status_code == 200
        except Exception:  # noqa: BLE001
            self.ready = False
        return self.ready

    # --- Spawn (espelha ``Manager.spawnLocal``) ---

    async def spawn(self) -> str | None:
        """Inicia o llama-server local. Retorna ``None`` ou a mensagem de erro."""
        if self._spawning or self._stopping:
            return None
        self._spawning = True
        try:
            # Encerra qualquer servidor local anterior para liberar a porta
            # (evita "port is not free" + processos órfãos, como no Go).
            self._kill_local()

            bin_path = self.resolve_bin()
            model_path = self.resolve_model()
            if not os.path.exists(bin_path):
                return f"llama-server binary not found at {bin_path}"
            if not os.path.exists(model_path):
                return f"model not found at {model_path}"

            threads = str(max(1, os.cpu_count() or 1))
            args = [
                "-m", model_path,
                "--host", self._bind_host,
                "--port", str(self.port),
                "--n-gpu-layers", "0",
                "--threads", threads,
                "--ctx-size", str(self._ctx_size),
            ]
            if not self._use_mmap:
                args.append("--no-mmap")

            log.info("Iniciando llama-server na porta %d (modelo: %s)...", self.port, model_path)
            process = await asyncio.create_subprocess_exec(
                bin_path,
                *args,
                cwd=os.path.dirname(bin_path),
                stdout=None,  # herda stdout/stderr do pai (mesmo comportamento do Go)
                stderr=None,
            )
            self._process = process
            self.host = self._bind_host
            self.is_local = True
            self.ready = False
            self._watch_task = asyncio.get_running_loop().create_task(self._watch(process))

            # Aguarda a saúde (até HEALTH_MAX_TRIES × HEALTH_RETRY_S). Se o
            # carregamento demorar além disso, mantemos o processo: ele pode
            # terminar de carregar em segundo plano.
            for _ in range(HEALTH_MAX_TRIES):
                await asyncio.sleep(HEALTH_RETRY_S)
                if await self.check_health():
                    log.info("llama-server pronto na porta %d.", self.port)
                    return None
            log.warning("llama-server não ficou pronto a tempo; mantendo (ainda pode carregar).")
            return None
        except Exception as exc:  # noqa: BLE001
            return f"start llama-server: {exc}"
        finally:
            self._spawning = False

    # --- Probe (espelha ``Manager.ProbeReady``) ---

    async def ensure_ready(self) -> tuple[bool, str | None]:
        if self.is_local and self._process is None and not self._spawning:
            error = await self.spawn()
            if error:
                return False, error
        ready = await self.check_health()
        return ready, None

    # --- Registro de worker remoto (espelha ``Manager.RegisterWorker``) ---

    def register_remote(self, host: str, port: int) -> None:
        if self.is_local and self.host == host and self.port == port:
            self.ready = True
            return
        log.info("Novo worker registrado: %s:%d", host, port)
        self._kill_local()
        self.host = host
        self.port = port
        self.is_local = False
        self.ready = True

    # --- Shutdown (espelha ``Manager.Stop``) ---

    async def stop(self) -> None:
        self._stopping = True
        if self._watch_task is not None:
            self._watch_task.cancel()
            try:
                await self._watch_task
            except asyncio.CancelledError:
                pass
            self._watch_task = None
        self._kill_local()

    # --- Internos ---

    def _kill_local(self) -> None:
        process, self._process = self._process, None
        if process is not None and process.returncode is None:
            try:
                process.kill()
            except ProcessLookupError:
                pass
        self.ready = False

    async def _watch(self, process: asyncio.subprocess.Process) -> None:
        """Reap do processo + restart agendado (espelha ``scheduleRetry``)."""
        await process.wait()
        if self._stopping or self._process is not process:
            return
        self.ready = False
        log.warning(
            "llama-server saiu (código %s); reiniciando em %.0fs.",
            process.returncode,
            RESTART_DELAY_S,
        )
        await asyncio.sleep(RESTART_DELAY_S)
        if self._stopping or self._process is not process:
            return
        await self.spawn()
