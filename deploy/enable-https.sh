#!/bin/sh
set -eu

COMPOSE="docker compose -f docker-compose.production.yml"
DOMAIN="${DOMAIN:-livemonitortk.com.br}"
EMAIL="${CERTBOT_EMAIL:?Informe CERTBOT_EMAIL com o e-mail de renovação}"

$COMPOSE up -d nginx
$COMPOSE --profile certificates run --rm --entrypoint certbot \
  certbot certonly --webroot -w /var/www/certbot \
  --email "$EMAIL" --agree-tos --no-eff-email \
  -d "$DOMAIN" -d "www.$DOMAIN"

cat > deploy/nginx/default.conf <<EOF
map \$http_upgrade \$connection_upgrade {
    default upgrade;
    '' close;
}
upstream app_frontend { server frontend:80; }
server {
    listen 80;
    server_name $DOMAIN www.$DOMAIN;
    location ^~ /.well-known/acme-challenge/ { root /var/www/certbot; }
    location / { return 301 https://\$host\$request_uri; }
}
server {
    listen 443 ssl;
    http2 on;
    server_name $DOMAIN www.$DOMAIN;
    ssl_certificate /etc/letsencrypt/live/$DOMAIN/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/$DOMAIN/privkey.pem;
    location / {
        proxy_pass http://app_frontend;
        proxy_http_version 1.1;
        proxy_set_header Host \$host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto https;
        proxy_set_header Upgrade \$http_upgrade;
        proxy_set_header Connection \$connection_upgrade;
        proxy_buffering off;
        proxy_read_timeout 3600s;
    }
}
EOF

$COMPOSE exec -T nginx nginx -t
$COMPOSE exec -T nginx nginx -s reload
echo "HTTPS ativo em https://$DOMAIN"
