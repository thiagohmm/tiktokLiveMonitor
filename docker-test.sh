#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$ROOT"

# A UI (nginx) é a porta pública; o backend fica interno na rede do compose.
FRONTEND_PORT="${FRONTEND_PORT:-8080}"
BASE_URL="http://localhost:${FRONTEND_PORT}"

if ! command -v docker >/dev/null 2>&1; then
  echo "Docker não encontrado. Instale Docker Desktop ou docker-ce."
  exit 1
fi

if [[ ! -f .env ]]; then
  cp .env.example .env
  echo ""
  echo "Criado .env a partir de .env.example"
  echo "  • Teste rápido sem login: defina AUTH_ENABLED=0 no .env"
  echo "  • Com Supabase: preencha SUPABASE_URL, SUPABASE_ANON_KEY,"
  echo "    SUPABASE_JWT_SECRET e SUPABASE_SERVICE_ROLE_KEY"
  echo ""
fi

# shellcheck disable=SC1091
set -a
source .env 2>/dev/null || true
set +a

echo "==> Build e subida (frontend na porta ${FRONTEND_PORT})..."
docker compose up --build -d

echo "==> Aguardando healthcheck..."
deadline=$((SECONDS + 120))
while [[ $SECONDS -lt $deadline ]]; do
  status="$(docker compose ps --format json 2>/dev/null | head -1 || true)"
  if curl -sf "${BASE_URL}/api/readiness" >/dev/null 2>&1; then
    echo "OK — app respondendo em ${BASE_URL}"
    break
  fi
  sleep 2
done

if ! curl -sf "${BASE_URL}/api/readiness" >/dev/null 2>&1; then
  echo "Timeout: app não ficou healthy. Veja os logs:"
  echo "  docker compose logs -f"
  exit 1
fi

auth_enabled="$(curl -sf "${BASE_URL}/api/auth/config" | grep -o '"enabled":[^,}]*' | cut -d: -f2 || echo true)"
auth_enabled="${auth_enabled// /}"

echo ""
echo "────────────────────────────────────────"
echo "  App:   ${BASE_URL} (nginx -> backend Go)"
if [[ "$auth_enabled" == "false" ]]; then
  echo "  Auth:  desativada (AUTH_ENABLED=0)"
else
  echo "  Login: ${BASE_URL}/login.html"
  echo "  Auth:  ativa — teste bloqueio/logout/tema"
fi
echo ""
echo "  Logs:  docker compose logs -f"
echo "  Parar: docker compose down"
echo "────────────────────────────────────────"

if command -v open >/dev/null 2>&1; then
  if [[ "$auth_enabled" == "false" ]]; then
    open "${BASE_URL}"
  else
    open "${BASE_URL}/login.html"
  fi
fi
