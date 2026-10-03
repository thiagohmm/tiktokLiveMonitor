#!/usr/bin/env sh
# Publica os estáticos do TikTok Live Monitor no nginx de borda.
set -eu
ROOT="$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)"
EDGE_ROOT="${EDGE_ROOT:-/opt/vps-edge-nginx}"
exec "$EDGE_ROOT/scripts/deploy-static.sh" livemonitortk "$ROOT/frontend"
