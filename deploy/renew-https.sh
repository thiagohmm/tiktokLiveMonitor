#!/bin/sh
set -eu

COMPOSE="docker compose -f docker-compose.production.yml"
$COMPOSE --profile certificates run --rm certbot renew
$COMPOSE exec -T nginx nginx -t
$COMPOSE exec -T nginx nginx -s reload
