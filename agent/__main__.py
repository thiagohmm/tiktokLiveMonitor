"""Entrypoint do agente: python3 -m agent."""

import logging

import uvicorn

from . import config
from .api import app


def main():
    logging.basicConfig(
        level=logging.INFO,
        format="[agent] %(asctime)s %(levelname)s %(name)s: %(message)s",
    )
    uvicorn.run(app, host=config.AGENT_HOST, port=config.AGENT_PORT, log_level="info")


if __name__ == "__main__":
    main()
