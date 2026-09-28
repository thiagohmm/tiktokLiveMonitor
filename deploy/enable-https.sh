#!/usr/bin/env bash
# Emite/renova o certificado do DOMAIN e ativa HTTPS na borda sem apagar os
# blocos server {} de outros apps (Sigmenta, agenttk) do default.conf.
#
# - Já existe bloco 443 para o DOMAIN e o certificado está no volume: só
#   renova se estiver perto de expirar, valida e recarrega. A conf não muda.
# - Certificado ausente (instalação nova): troca apenas o bloco :80 do DOMAIN
#   por um bootstrap HTTP com ACME, emite o certificado e ativa o bloco 443.
#   Um bloco 443 existente do DOMAIN (com /agenttk/, /media/...) é recolocado
#   como estava; só é gerado um bloco novo se não houver nenhum.
# - Toda alteração gera backup com timestamp em BACKUP_DIR e é validada com
#   nginx -t; se falhar, o backup é restaurado e o script aborta.
# - A borda nunca é recriada: com o container rodando, só exec nginx -s reload.
set -euo pipefail

COMPOSE="docker compose -f docker-compose.production.yml"
DOMAIN="${DOMAIN:-livemonitortk.com.br}"
CONF="${NGINX_CONF:-deploy/nginx/default.conf}"
BACKUP_DIR="${NGINX_BACKUP_DIR:-backups/nginx}"
UPSTREAM_FRONTEND="${UPSTREAM_FRONTEND:-frontend:80}"

HTTP_MARK="# __TLM_DOMAIN_HTTP__"
HTTPS_MARK="# __TLM_DOMAIN_HTTPS__"
LAST_BACKUP=""

log() { echo "[enable-https] $*"; }
die() { echo "[enable-https] ERRO: $*" >&2; exit 1; }

# Separa a conf em esqueleto + blocos do DOMAIN. Os blocos server {} do DOMAIN
# viram marcadores no esqueleto; os :80 vão para $3 e os 443 para $4. Todo o
# resto (map, upstream, comentários e server {} de outros domínios) fica igual.
split_conf() {
    local src="$1" skeleton="$2" http_out="$3" https_out="$4"
    : > "$http_out"
    : > "$https_out"
    awk -v domain="$DOMAIN" -v http_out="$http_out" -v https_out="$https_out" \
        -v http_mark="$HTTP_MARK" -v https_mark="$HTTPS_MARK" '
        function strip(s) { sub(/#.*/, "", s); return s }
        function is_domain(block,   n, lines, i, t, m, toks, j) {
            n = split(block, lines, "\n")
            for (i = 1; i <= n; i++) {
                t = strip(lines[i])
                if (t !~ /^[ \t]*server_name[ \t]/) continue
                sub(/^[ \t]*server_name[ \t]+/, "", t)
                sub(/;.*/, "", t)
                m = split(t, toks, /[ \t]+/)
                for (j = 1; j <= m; j++)
                    if (toks[j] == domain || toks[j] == "www." domain) return 1
            }
            return 0
        }
        function is_https(block,   n, lines, i) {
            n = split(block, lines, "\n")
            for (i = 1; i <= n; i++)
                if (strip(lines[i]) ~ /^[ \t]*listen[ \t]+([^;]*[: ])?443([ \t;]|$)/) return 1
            return 0
        }
        function handle(block) {
            if (!is_domain(block)) { printf "%s", block; return }
            if (is_https(block)) {
                if (!https_seen++) print https_mark
                printf "%s", block >> https_out
            } else {
                if (!http_seen++) print http_mark
                printf "%s", block >> http_out
            }
        }
        {
            code = strip($0)
            if (!inblk && top == 0 && code ~ /^[ \t]*server[ \t]*\{/) { inblk = 1; depth = 0; block = "" }
            opens = gsub(/\{/, "{", code); closes = gsub(/\}/, "}", code)
            if (inblk) {
                block = block $0 "\n"
                depth += opens - closes
                if (depth <= 0) { handle(block); inblk = 0 }
                next
            }
            top += opens - closes
            print
        }
        END {
            if (inblk || top != 0) { print "chaves desbalanceadas na conf" > "/dev/stderr"; exit 2 }
        }
    ' "$src" > "$skeleton"
}

# Monta a conf: esqueleto com os marcadores trocados por $2 (bloco :80) e $3
# (blocos 443; vazio = nenhum). Sem marcador 443, os blocos 443 entram logo
# depois do :80; sem marcador nenhum, vão para o fim do arquivo.
assemble_conf() {
    local skeleton="$1" http_file="$2" https_file="$3" inline_https=0
    grep -qxF "$HTTPS_MARK" "$skeleton" || inline_https=1
    awk -v http_file="$http_file" -v https_file="$https_file" -v inline_https="$inline_https" \
        -v http_mark="$HTTP_MARK" -v https_mark="$HTTPS_MARK" '
        function emit(f,   l) { if (f == "") return; while ((getline l < f) > 0) print l; close(f) }
        $0 == http_mark { emit(http_file); http_done = 1; if (inline_https) { emit(https_file); https_done = 1 }; next }
        $0 == https_mark { emit(https_file); https_done = 1; next }
        { print }
        END {
            if (!http_done) { print ""; emit(http_file) }
            if (!https_done && https_file != "") { print ""; emit(https_file) }
        }
    ' "$skeleton"
}

# Garante o map e o upstream usados pelos blocos gerados (instalação nova).
ensure_prereqs() {
    local skeleton="$1" tmp
    tmp="$(mktemp)"
    {
        # shellcheck disable=SC2016 # $http_upgrade é literal do nginx
        grep -q 'map[[:space:]]\{1,\}\$http_upgrade[[:space:]]\{1,\}\$connection_upgrade' "$skeleton" || cat <<'EOF'
map $http_upgrade $connection_upgrade {
    default upgrade;
    '' close;
}
EOF
        grep -q 'upstream[[:space:]]\{1,\}app_frontend[[:space:]{]' "$skeleton" \
            || echo "upstream app_frontend { server $UPSTREAM_FRONTEND; }"
        cat "$skeleton"
    } > "$tmp"
    cat "$tmp" > "$skeleton"
    rm -f "$tmp"
}

write_http_bootstrap() {
    cat > "$1" <<EOF
server {
    listen 80;
    server_name $DOMAIN www.$DOMAIN;
    location ^~ /.well-known/acme-challenge/ { root /var/www/certbot; }
    location / {
        proxy_pass http://app_frontend;
        proxy_http_version 1.1;
        proxy_set_header Host \$host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto \$scheme;
        proxy_set_header Upgrade \$http_upgrade;
        proxy_set_header Connection \$connection_upgrade;
    }
}
EOF
}

write_http_redirect() {
    cat > "$1" <<EOF
server {
    listen 80;
    server_name $DOMAIN www.$DOMAIN;
    location ^~ /.well-known/acme-challenge/ { root /var/www/certbot; }
    location / { return 301 https://\$host\$request_uri; }
}
EOF
}

write_https_default() {
    cat > "$1" <<EOF
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
}

# Gera em $3, a partir da conf $2, a conf de uma fase: "bootstrap" (só HTTP +
# ACME para o DOMAIN) ou "https" (redirect :80 + blocos 443 de $2 ou o padrão).
render_conf() {
    local phase="$1" src="$2" out="$3" dir
    dir="$(mktemp -d)"
    split_conf "$src" "$dir/skeleton" "$dir/http" "$dir/https"
    ensure_prereqs "$dir/skeleton"
    if [ "$phase" = bootstrap ]; then
        write_http_bootstrap "$dir/http.new"
        assemble_conf "$dir/skeleton" "$dir/http.new" "" > "$out"
    else
        write_http_redirect "$dir/http.new"
        [ -s "$dir/https" ] || write_https_default "$dir/https"
        assemble_conf "$dir/skeleton" "$dir/http.new" "$dir/https" > "$out"
    fi
    rm -rf "$dir"
}

# 0 se a conf tem um server {} com listen 443 e server_name do DOMAIN.
conf_has_https() {
    local dir rc=1
    dir="$(mktemp -d)"
    split_conf "$CONF" "$dir/skeleton" "$dir/http" "$dir/https"
    [ -s "$dir/https" ] && rc=0
    rm -rf "$dir"
    return "$rc"
}

nginx_running() {
    [ -n "$($COMPOSE ps -q --status running nginx)" ]
}

nginx_test() {
    if nginx_running; then
        $COMPOSE exec -T nginx nginx -t
    else
        $COMPOSE run --rm --no-deps -T --entrypoint nginx nginx -t
    fi
}

# Com a borda rodando, só reload. Parada (instalação nova), apenas inicia o
# container, sem recriar um existente.
nginx_apply() {
    if nginx_running; then
        $COMPOSE exec -T nginx nginx -s reload
    else
        log "Nginx de borda parado; iniciando sem recriar (--no-recreate)."
        $COMPOSE up -d --no-recreate nginx
    fi
}

# Grava $1 no lugar da conf (mesmo inode, o bind mount do arquivo continua
# válido), com backup antes e restauração automática se nginx -t falhar.
apply_conf() {
    local new="$1" label="$2" backup
    if cmp -s "$new" "$CONF"; then
        log "Conf já está no estado '$label'; nada a gravar."
        return 0
    fi
    mkdir -p "$BACKUP_DIR"
    backup="$BACKUP_DIR/default.conf.$(date -u +%Y%m%dT%H%M%SZ).pre-$label.bak"
    cp -p "$CONF" "$backup"
    LAST_BACKUP="$backup"
    log "Backup da conf atual: $backup"
    cat "$new" > "$CONF"
    if ! nginx_test; then
        cat "$backup" > "$CONF"
        die "nginx -t falhou com a conf '$label'; $CONF restaurado de $backup (nenhum reload feito)."
    fi
    nginx_apply
    log "Conf '$label' aplicada."
}

cert_exists() {
    local out
    out="$($COMPOSE --profile certificates run --rm --no-deps --entrypoint sh certbot -c \
        "if [ -f /etc/letsencrypt/live/$DOMAIN/fullchain.pem ]; then echo sim; else echo nao; fi")" \
        || die "não foi possível consultar o volume letsencrypt."
    case "$out" in
        *sim*) return 0 ;;
        *nao*) return 1 ;;
        *) die "resposta inesperada ao consultar o certificado: $out" ;;
    esac
}

# Emite, ou renova se estiver perto de expirar (sem efeito caso contrário).
issue_cert() {
    $COMPOSE --profile certificates run --rm --no-deps --entrypoint certbot \
        certbot certonly --webroot -w /var/www/certbot \
        --non-interactive --keep-until-expiring --cert-name "$DOMAIN" \
        --email "$EMAIL" --agree-tos --no-eff-email \
        -d "$DOMAIN" -d "www.$DOMAIN"
}

reload_only() {
    nginx_test || die "nginx -t falhou; a conf não foi alterada e nada foi recarregado."
    nginx_apply
}

main() {
    cd "$(dirname "$0")/.."
    EMAIL="${CERTBOT_EMAIL:?Informe CERTBOT_EMAIL com o e-mail de renovação}"
    [ -f "$CONF" ] || die "$CONF não existe."

    WORK="$(mktemp -d)"
    trap 'rm -rf "$WORK"' EXIT
    cp -p "$CONF" "$WORK/original.conf"

    if cert_exists; then
        if conf_has_https; then
            log "$CONF já tem HTTPS para $DOMAIN e o certificado existe; a conf não será alterada."
        else
            log "Certificado existe, mas falta o bloco 443 de $DOMAIN; ativando HTTPS."
            render_conf https "$WORK/original.conf" "$WORK/next.conf"
            apply_conf "$WORK/next.conf" https
        fi
        issue_cert
        reload_only
    else
        log "Sem certificado para $DOMAIN: bootstrap HTTP só para este domínio."
        if conf_has_https; then
            log "O bloco 443 atual de $DOMAIN sai durante a emissão e volta igual em seguida."
        fi
        render_conf bootstrap "$WORK/original.conf" "$WORK/next.conf"
        apply_conf "$WORK/next.conf" bootstrap
        issue_cert || die "emissão falhou; $CONF ficou no bootstrap HTTP. A conf anterior está em ${LAST_BACKUP:-$WORK/original.conf}."
        render_conf https "$WORK/original.conf" "$WORK/next.conf"
        apply_conf "$WORK/next.conf" https
    fi
    log "HTTPS ativo em https://$DOMAIN"
}

if [ "${BASH_SOURCE[0]}" = "$0" ]; then
    main "$@"
fi
