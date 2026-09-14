#!/usr/bin/env bash

set -Eeuo pipefail

SCRIPT_DIR="${BASH_SOURCE[0]%/*}"
if [[ "${SCRIPT_DIR}" == "${BASH_SOURCE[0]}" ]]; then
  SCRIPT_DIR="."
fi
ROOT_DIR="$(cd -- "${SCRIPT_DIR}/.." && pwd)"
CONFIG_PATH="${ROOT_DIR}/configs/researchd.dev.json"
BACKEND_BIN="${ROOT_DIR}/bin/researchd-dev.exe"
MODE="all"
PIDS=()

usage() {
  printf '%s\n' \
    'Usage: ./scripts/run.sh [options]' \
    '' \
    'Start the local strategy research backend and frontend.' \
    '' \
    'Options:' \
    '  --backend-only       Start only researchd.' \
    '  --frontend-only      Start only the Vite frontend.' \
    '  --config <path>      Backend JSON config (default: configs/researchd.dev.json).' \
    '  -h, --help           Show this help.'
}

die() {
  printf 'run.sh: %s\n' "$*" >&2
  exit 1
}

while (($# > 0)); do
  case "$1" in
    --backend-only)
      [[ "${MODE}" == "all" ]] || die "choose only one startup mode"
      MODE="backend"
      shift
      ;;
    --frontend-only)
      [[ "${MODE}" == "all" ]] || die "choose only one startup mode"
      MODE="frontend"
      shift
      ;;
    --config)
      (($# >= 2)) || die "--config requires a path"
      CONFIG_PATH="$2"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      die "unknown option: $1"
      ;;
  esac
done

if [[ "${CONFIG_PATH}" != /* && ! "${CONFIG_PATH}" =~ ^[A-Za-z]:[\\/] ]]; then
  CONFIG_PATH="${ROOT_DIR}/${CONFIG_PATH}"
fi

cleanup() {
  local status=$?
  trap - EXIT

  if ((${#PIDS[@]} > 0)); then
    printf '\nStopping development processes...\n'
    for pid in "${PIDS[@]}"; do
      if kill -0 "${pid}" 2>/dev/null; then
        # npm creates child processes, and Git Bash needs an explicit tree stop
        # or the node child can survive after the shell exits.
        case "${OSTYPE:-}" in
          msys*|cygwin*)
            taskkill.exe //PID "${pid}" //T //F >/dev/null 2>&1 || true
            ;;
          *)
            kill "${pid}" 2>/dev/null || true
            ;;
        esac
      fi
    done
    for pid in "${PIDS[@]}"; do
      wait "${pid}" 2>/dev/null || true
    done
  fi

  exit "${status}"
}

trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

prepare_backend() {
  command -v go >/dev/null 2>&1 || die "Go is not installed or not on PATH"
  [[ -f "${CONFIG_PATH}" ]] || die "config file not found: ${CONFIG_PATH}"

  mkdir -p "${ROOT_DIR}/bin"
  printf 'Building backend...\n'
  (
    cd "${ROOT_DIR}"
    go build -o "${BACKEND_BIN}" ./cmd/researchd
  )
}

start_backend() {
  prepare_backend
  printf 'Starting backend with config: %s\n' "${CONFIG_PATH}"
  (
    cd "${ROOT_DIR}"
    exec "${BACKEND_BIN}" -config "${CONFIG_PATH}"
  ) &
  PIDS+=("$!")
}

run_backend() {
  prepare_backend
  printf 'Starting backend with config: %s\n' "${CONFIG_PATH}"
  cd "${ROOT_DIR}"
  exec "${BACKEND_BIN}" -config "${CONFIG_PATH}"
}

start_frontend() {
  command -v npm >/dev/null 2>&1 || die "Node.js/npm is not installed or not on PATH"
  [[ -d "${ROOT_DIR}/web/node_modules" ]] || die "frontend dependencies are missing; run: cd web && npm install"

  printf 'Starting frontend: http://127.0.0.1:5173\n'
  (
    cd "${ROOT_DIR}/web"
    exec npm run dev -- --host 127.0.0.1 --strictPort
  ) &
  PIDS+=("$!")
}

run_frontend() {
  command -v npm >/dev/null 2>&1 || die "Node.js/npm is not installed or not on PATH"
  [[ -d "${ROOT_DIR}/web/node_modules" ]] || die "frontend dependencies are missing; run: cd web && npm install"

  printf 'Starting frontend: http://127.0.0.1:5173\n'
  cd "${ROOT_DIR}/web"
  exec npm run dev -- --host 127.0.0.1 --strictPort
}

case "${MODE}" in
  backend)
    run_backend
    ;;
  frontend)
    run_frontend
    ;;
  all)
    start_backend
    start_frontend
    ;;
esac

printf 'Press Ctrl+C to stop.\n'

set +e
wait -n "${PIDS[@]}"
STATUS=$?
set -e

if ((STATUS != 0 && STATUS != 130 && STATUS != 143)); then
  printf 'A development process exited with status %d.\n' "${STATUS}" >&2
fi

exit "${STATUS}"
