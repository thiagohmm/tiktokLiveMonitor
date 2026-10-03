#!/usr/bin/env sh
# Renovação HTTPS delegada ao nginx de borda compartilhado.
set -eu
exec /opt/vps-edge-nginx/scripts/renew-https.sh
