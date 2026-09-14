#!/usr/bin/env bash

set -Eeuo pipefail

SCRIPT_DIR="${BASH_SOURCE[0]%/*}"
if [[ "${SCRIPT_DIR}" == "${BASH_SOURCE[0]}" ]]; then
  SCRIPT_DIR="."
fi
ROOT_DIR="$(cd -- "${SCRIPT_DIR}/.." && pwd)"
CONFIG_PATH="${ROOT_DIR}/configs/researchd.dev.json"
BACKEND_BIN="${ROOT_DIR}/bin/researchd-dev.exe"

usage() {
  printf '%s\n' \
    'Usage: bash scripts/run-backend.sh [config-path]' \
    '' \
    'Build and run researchd in the foreground.' \
    'The default config is configs/researchd.dev.json.'
}

if (($# > 1)); then
  usage >&2
  exit 1
fi

if (($# == 1)); then
  case "$1" in
    -h|--help)
      usage
      exit 0
      ;;
    *)
      CONFIG_PATH="$1"
      ;;
  esac
fi

if [[ "${CONFIG_PATH}" != /* && ! "${CONFIG_PATH}" =~ ^[A-Za-z]:[\\/] ]]; then
  CONFIG_PATH="${ROOT_DIR}/${CONFIG_PATH}"
fi

command -v go >/dev/null 2>&1 || {
  printf 'run-backend.sh: Go is not installed or not on PATH\n' >&2
  exit 1
}

[[ -f "${CONFIG_PATH}" ]] || {
  printf 'run-backend.sh: config file not found: %s\n' "${CONFIG_PATH}" >&2
  exit 1
}

mkdir -p "${ROOT_DIR}/bin"
printf 'Building backend...\n'
(
  cd "${ROOT_DIR}"
  go build -o "${BACKEND_BIN}" ./cmd/researchd
)

printf 'Starting backend in the foreground with config: %s\n' "${CONFIG_PATH}"
cd "${ROOT_DIR}"
exec "${BACKEND_BIN}" -config "${CONFIG_PATH}"
