#!/usr/bin/env sh
# HTTPS dos domínios de produção é gerido pelo stack /opt/vps-edge-nginx.
# Este script só valida o nginx de borda (não reemite certificados).
set -eu

EDGE_ROOT="${EDGE_ROOT:-/opt/vps-edge-nginx}"
if [ ! -d "$EDGE_ROOT" ]; then
  echo "Edge nginx não encontrado em $EDGE_ROOT" >&2
  exit 1
fi

docker exec vps-edge-nginx nginx -t
docker exec vps-edge-nginx nginx -s reload
echo "Nginx de borda OK. Certificados: $EDGE_ROOT (Let's Encrypt volumes legados do TLM)."
echo "Para renovar: $EDGE_ROOT/scripts/renew-https.sh"
