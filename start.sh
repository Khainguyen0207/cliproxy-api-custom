#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TMP_DIR="${ROOT_DIR}/tmp"
PID_FILE="${TMP_DIR}/cli-proxy-runtime.pid"
LOG_FILE="${TMP_DIR}/cli-proxy-runtime.log"

start_server() {
  mkdir -p "${TMP_DIR}"

  "${ROOT_DIR}/stop.sh"

  if [[ -f "${PID_FILE}" ]]; then
    rm -f "${PID_FILE}"
  fi

  cd "${ROOT_DIR}"
  nohup go run ./cmd/server > "${LOG_FILE}" 2>&1 &
  local pid="$!"
  printf '%s\n' "${pid}" > "${PID_FILE}"

  echo "cli-proxy started with PID ${pid}."
  echo "Log: ${LOG_FILE}"
}

restart_server() {
  start_server
}

case "${1:-start}" in
  start)
    start_server
    ;;
  restart)
    restart_server
    ;;
  *)
    echo "Usage: ./start.sh [start|restart]"
    exit 1
    ;;
esac
