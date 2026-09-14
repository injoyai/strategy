#!/usr/bin/env bash

set -Eeuo pipefail

SCRIPT_DIR="${BASH_SOURCE[0]%/*}"
if [[ "${SCRIPT_DIR}" == "${BASH_SOURCE[0]}" ]]; then
  SCRIPT_DIR="."
fi
ROOT_DIR="$(cd -- "${SCRIPT_DIR}/.." && pwd)"
WEB_DIR="${ROOT_DIR}/web"

if (($# > 0)); then
  if (($# == 1)) && [[ "$1" == "-h" || "$1" == "--help" ]]; then
    printf '%s\n' \
      'Usage: bash scripts/run-frontend.sh' \
      '' \
      'Run the Vite frontend in the foreground at http://127.0.0.1:5173.'
    exit 0
  fi

  printf 'run-frontend.sh: no arguments are supported\n' >&2
  exit 1
fi

command -v npm >/dev/null 2>&1 || {
  printf 'run-frontend.sh: Node.js/npm is not installed or not on PATH\n' >&2
  exit 1
}

[[ -d "${WEB_DIR}/node_modules" ]] || {
  printf 'run-frontend.sh: dependencies are missing; run: cd web && npm install\n' >&2
  exit 1
}

printf 'Starting frontend in the foreground: http://127.0.0.1:5173\n'
cd "${WEB_DIR}"
exec npm run dev -- --host 127.0.0.1 --strictPort
