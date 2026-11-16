#!/usr/bin/env sh
# Uso: source infra/langfuse/env.sh
DIR="$(cd "$(dirname "$0")" && pwd)"

if [ ! -f "$DIR/.env" ]; then
  echo "infra/langfuse/.env não existe; rode infra/langfuse/init-env.sh" >&2
  return 1 2>/dev/null || exit 1
fi

set -a
. "$DIR/.env"
set +a

export LANGFUSE_HOST="${LANGFUSE_HOST:-http://localhost:3000}"
export LANGFUSE_PUBLIC_KEY="$LANGFUSE_INIT_PROJECT_PUBLIC_KEY"
export LANGFUSE_SECRET_KEY="$LANGFUSE_INIT_PROJECT_SECRET_KEY"
