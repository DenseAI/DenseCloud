#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

IMAGE_REPOSITORY="${IMAGE_REPOSITORY:-densecloud-local-reference}"
IMAGE_TAG="${IMAGE_TAG:-smoke}"
IMAGE_REF="${IMAGE_REPOSITORY}:${IMAGE_TAG}"
CONTAINER_NAME="${CONTAINER_NAME:-densecloud-smoke-$$}"
SMOKE_CONTAINER_LABEL="io.densecloud.smoke"
HOST_PORT="${HOST_PORT:-18080}"
CONTAINER_PORT="${CONTAINER_PORT:-8080}"
SMOKE_RETRIES="${SMOKE_RETRIES:-30}"
SMOKE_SLEEP_SECONDS="${SMOKE_SLEEP_SECONDS:-2}"

require_command() {
  local cmd="$1"
  if ! command -v "${cmd}" >/dev/null 2>&1; then
    echo "missing required command: ${cmd}" >&2
    exit 1
  fi
}

cleanup() {
  if ! docker container inspect "${CONTAINER_NAME}" >/dev/null 2>&1; then
    return 0
  fi
  if [[ "$(docker container inspect -f "{{ index .Config.Labels \"${SMOKE_CONTAINER_LABEL}\" }}" "${CONTAINER_NAME}")" != "true" ]]; then
    echo "refusing to remove container not owned by this smoke run: ${CONTAINER_NAME}" >&2
    return 0
  fi
  docker rm -f "${CONTAINER_NAME}" >/dev/null 2>&1 || true
}
trap cleanup EXIT

wait_for_http_ok() {
  local path="$1"
  local pattern="${2:-}"
  local body_file
  local attempt
  local status_code
  local url="http://127.0.0.1:${HOST_PORT}${path}"

  body_file="$(mktemp "/tmp/${CONTAINER_NAME}.XXXXXX.body")"
  for attempt in $(seq 1 "${SMOKE_RETRIES}"); do
    status_code="$(curl -sS -o "${body_file}" -w '%{http_code}' "${url}" 2>/dev/null || true)"
    if [[ "${status_code}" == "200" ]]; then
      if [[ -z "${pattern}" ]] || grep -Eq "${pattern}" "${body_file}"; then
        rm -f "${body_file}"
        return 0
      fi
    fi
    sleep "${SMOKE_SLEEP_SECONDS}"
  done

  echo "runtime smoke failed for ${url}" >&2
  echo "last response body:" >&2
  cat "${body_file}" >&2 || true
  rm -f "${body_file}"
  echo "container logs:" >&2
  docker logs "${CONTAINER_NAME}" >&2 || true
  exit 1
}

require_command docker
require_command curl

cd "${REPO_ROOT}"

docker build -t "${IMAGE_REF}" -f "${REPO_ROOT}/Dockerfile" "${REPO_ROOT}"
cleanup
docker run -d \
  --name "${CONTAINER_NAME}" \
  --label "${SMOKE_CONTAINER_LABEL}=true" \
  -p "${HOST_PORT}:${CONTAINER_PORT}" \
  "${IMAGE_REF}" \
  >/dev/null

wait_for_http_ok "/health/startup"
wait_for_http_ok "/health/live"
wait_for_http_ok "/health/ready"
wait_for_http_ok "/metrics" 'densecloud_http_requests_total'
wait_for_http_ok "/v1/hello" '"status"[[:space:]]*:[[:space:]]*"ok"'

echo "docker runtime smoke passed for ${IMAGE_REF}"
