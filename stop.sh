#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TMP_DIR="${ROOT_DIR}/tmp"
PID_FILE="${TMP_DIR}/cli-proxy-runtime.pid"
CONFIG_FILE="${ROOT_DIR}/config.yaml"

configured_port() {
  if [[ -f "${CONFIG_FILE}" ]]; then
    awk '$1 == "port:" { print $2; exit }' "${CONFIG_FILE}"
  fi
}

stop_pid() {
  local pid="$1"
  local label="$2"

  if [[ -z "${pid}" ]]; then
    return 1
  fi
  if ! kill -0 "${pid}" 2>/dev/null; then
    return 1
  fi

  kill "${pid}"

  for _ in {1..30}; do
    if ! kill -0 "${pid}" 2>/dev/null; then
      rm -f "${PID_FILE}"
      echo "cli-proxy stopped (${label} PID ${pid})."
      return 0
    fi
    sleep 1
  done

  kill -9 "${pid}" 2>/dev/null || true
  rm -f "${PID_FILE}"
  echo "cli-proxy force stopped (${label} PID ${pid})."
  return 0
}

stopped=0
tracked_pid=""

if [[ -f "${PID_FILE}" ]]; then
  tracked_pid="$(tr -d '[:space:]' < "${PID_FILE}")"
  if stop_pid "${tracked_pid}" "tracked"; then
    stopped=1
  else
    rm -f "${PID_FILE}"
  fi
fi

port="$(configured_port)"
if [[ -n "${port}" ]] && command -v lsof >/dev/null 2>&1; then
  while IFS= read -r port_pid; do
    if [[ -z "${port_pid}" || "${port_pid}" == "${tracked_pid}" ]]; then
      continue
    fi
    if stop_pid "${port_pid}" "port ${port}"; then
      stopped=1
    fi
  done < <(lsof -tiTCP:"${port}" -sTCP:LISTEN -n -P 2>/dev/null || true)
fi

rm -f "${PID_FILE}"
if [[ "${stopped}" == "0" ]]; then
  echo "cli-proxy is not running."
fi
