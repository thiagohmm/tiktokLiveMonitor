#!/usr/bin/env bash
#
# prepereWorktree.sh — prepara um git worktree para desenvolvimento paralelo.
#
# Uso:
#   ./prepereWorktree.sh <branch> [pasta-destino] [base-ref]
#   ./prepereWorktree.sh merge <branch> [merge-destino]
#
# Exemplos:
#   ./prepereWorktree.sh feat/minha-feature
#   ./prepereWorktree.sh feat/minha-feature ../tiktokLiveMonitor-feat-minha-feature
#   ./prepereWorktree.sh hotfix/bug-123 ../tiktokLiveMonitor-hotfix-123 main
#   ./prepereWorktree.sh merge feat/minha-feature
#   ./prepereWorktree.sh merge feat/minha-feature main
#
# Comportamento (criação):
#   - Cria a pasta-destino (padrão: ../<repo>-<branch-sanitizado>)
#   - Se a branch existir, faz checkout dela; senão, cria a partir da base-ref (padrão: HEAD)
#   - Lista os worktrees no final para confirmar o estado
#
# Comportamento (merge):
#   - Localiza o worktree da branch e exige que ele esteja limpo
#   - Faz o merge no worktree principal (destino padrão: branch atual do worktree principal)
#   - Em sucesso, remove o worktree e a branch; em conflito, mantém ambos e explica como resolver
#
set -euo pipefail

# ---------- helpers ----------
err()  { printf '\033[31m[erro]\033[0m %s\n' "$*" >&2; }
info() { printf '\033[34m[info]\033[0m %s\n' "$*"; }
ok()   { printf '\033[32m[ok]\033[0m %s\n' "$*"; }

usage() {
  sed -n '2,25p' "$0" | sed 's/^# \{0,1\}//'
  exit "${1:-0}"
}

# ---------- validação de argumentos ----------
if [[ $# -lt 1 ]]; then
  err "Falta o subcomando (criação: <branch>; merge: merge <branch>)."
  echo
  usage 1
fi

if [[ ${1:-} == "-h" || ${1:-} == "--help" ]]; then
  usage 0
fi

REPO_ROOT="$(git rev-parse --show-toplevel 2>/dev/null)" || {
  err "Você não está dentro de um repositório git."
  exit 1
}

MODE="$1"

# ---------- modo merge ----------
if [[ $MODE == "merge" ]]; then
  if [[ $# -lt 2 ]]; then
    err "Falta o nome da branch para o merge."
    echo
    usage 1
  fi
  BRANCH="$2"
  MERGE_DEST="${3:-}"

  # worktree principal = primeiro registro de 'git worktree list --porcelain'
  MAIN_WT="$(git -C "$REPO_ROOT" worktree list --porcelain | awk 'NR==1 {print $2}')"

  # caminho do worktree onde <branch> está checada (saída 1 se não encontrar)
  worktree_of_branch() {
    local branch="$1" current="" line
    while IFS= read -r line; do
      if [[ $line == "worktree "* ]]; then
        current="${line#worktree }"
      elif [[ $line == "branch refs/heads/$branch" ]]; then
        printf '%s\n' "$current"
        return 0
      fi
    done < <(git -C "$REPO_ROOT" worktree list --porcelain)
    return 1
  }

  if ! WT_PATH="$(worktree_of_branch "$BRANCH")"; then
    err "A branch '$BRANCH' não está checada em nenhum worktree."
    exit 1
  fi

  info "Repositório : $REPO_ROOT"
  info "Branch      : $BRANCH"
  info "Worktree    : $WT_PATH"

  if [[ "$WT_PATH" == "$MAIN_WT" ]]; then
    err "A branch '$BRANCH' está checada no worktree principal — nada a fazer."
    exit 1
  fi

  if [[ -n "$(git -C "$WT_PATH" status --porcelain)" ]]; then
    err "O worktree '$WT_PATH' tem alterações não commitadas. Commit ou stash antes do merge."
    exit 1
  fi

  if [[ -z $MERGE_DEST ]]; then
    MERGE_DEST="$(git -C "$MAIN_WT" rev-parse --abbrev-ref HEAD)"
  fi
  if [[ $MERGE_DEST == "$BRANCH" ]]; then
    err "O destino é a própria branch '$BRANCH' — escolha outra branch de destino."
    exit 1
  fi
  if [[ $MERGE_DEST == "HEAD" ]] || ! git -C "$REPO_ROOT" rev-parse --verify "refs/heads/$MERGE_DEST" >/dev/null 2>&1; then
    err "A branch de destino '$MERGE_DEST' não existe neste repositório."
    exit 1
  fi
  info "Destino     : $MERGE_DEST"

  CURRENT_MAIN="$(git -C "$MAIN_WT" rev-parse --abbrev-ref HEAD)"
  if [[ $CURRENT_MAIN != "$MERGE_DEST" ]]; then
    if [[ -n "$(git -C "$MAIN_WT" status --porcelain)" ]]; then
      err "O worktree principal está sujo e seria preciso trocar para '$MERGE_DEST'. Commit ou stash antes."
      exit 1
    fi
    info "Trocando o worktree principal para '$MERGE_DEST'."
    git -C "$MAIN_WT" switch "$MERGE_DEST"
  fi

  MERGE_OUT="$(LC_ALL=C git -C "$MAIN_WT" merge --no-edit "$BRANCH" 2>&1)" || {
    err "Falha no merge de '$BRANCH' em '$MERGE_DEST':"
    echo "$MERGE_OUT"
    echo
    info "Como resolver:"
    echo "  cd $MAIN_WT"
    echo "  # resolva os conflitos e finalize o merge:"
    echo "  git add <arquivos> && git commit"
    echo "  # depois remova o worktree e a branch manualmente:"
    echo "  git worktree remove $WT_PATH"
    echo "  git branch -d $BRANCH"
    exit 1
  }

  if grep -q "Already up to date" <<< "$MERGE_OUT"; then
    info "Nada para mergear — '$MERGE_DEST' já está atualizado com '$BRANCH'."
  else
    ok "Merge de '$BRANCH' em '$MERGE_DEST' concluído."
  fi

  git -C "$REPO_ROOT" worktree remove "$WT_PATH"
  ok "Worktree removido: $WT_PATH"

  git -C "$REPO_ROOT" branch -d "$BRANCH"
  ok "Branch deletada: $BRANCH"

  echo
  info "Resumo:"
  echo "  merge: $BRANCH -> $MERGE_DEST"
  echo "  worktree removido: $WT_PATH"
  echo "  branch deletada: $BRANCH"
  exit 0
fi

# ---------- modo criação ----------
BRANCH="$1"
BASE_REF="${3:-HEAD}"
# pasta-destino padrão: ao lado do repo, com nome sanitizado da branch
REPO_NAME="$(basename "$REPO_ROOT")"
SAFE_BRANCH="${BRANCH//\//-}"
TARGET="${2:-$(dirname "$REPO_ROOT")/${REPO_NAME}-${SAFE_BRANCH}}"

info "Repositório : $REPO_ROOT"
info "Branch      : $BRANCH"
info "Base ref    : $BASE_REF"
info "Destino     : $TARGET"

# ---------- pré-checks ----------
if git -C "$REPO_ROOT" rev-parse --verify "refs/heads/$BRANCH" >/dev/null 2>&1; then
  info "Branch '$BRANCH' já existe — será feita apenas a checagem no worktree."
  CREATE_BRANCH=0
else
  info "Branch '$BRANCH' não existe — será criada a partir de '$BASE_REF'."
  CREATE_BRANCH=1
fi

if ! git -C "$REPO_ROOT" rev-parse --verify "$BASE_REF" >/dev/null 2>&1; then
  err "Base ref '$BASE_REF' não existe neste repositório."
  exit 1
fi

if [[ -e "$TARGET" ]]; then
  err "O destino '$TARGET' já existe. Escolha outro caminho ou remova a pasta."
  exit 1
fi

if git -C "$REPO_ROOT" worktree list --porcelain | grep -qxF "worktree $TARGET"; then
  err "Já existe um worktree registrado em '$TARGET'."
  exit 1
fi

# ---------- criação ----------
mkdir -p "$(dirname "$TARGET")"

if [[ $CREATE_BRANCH -eq 1 ]]; then
  git -C "$REPO_ROOT" worktree add -b "$BRANCH" "$TARGET" "$BASE_REF"
else
  git -C "$REPO_ROOT" worktree add "$TARGET" "$BRANCH"
fi

ok "Worktree criado em: $TARGET"

# ---------- resumo ----------
echo
info "Worktrees deste repositório:"
git -C "$REPO_ROOT" worktree list
echo
info "Para entrar no worktree:"
echo "  cd $TARGET"
echo
info "Para terminar (merge + limpeza automática):"
echo "  ./prepereWorktree.sh merge $BRANCH"
echo
info "Ou manualmente:"
echo "  git worktree remove $TARGET"
echo "  git branch -d $BRANCH"
