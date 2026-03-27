#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
RENDERER_DIR="${REPO_ROOT}/charts/dense-base/examples/renderer"

VALUES_FILES=(
  "minimal.yaml"
  "ingress-cert-manager.yaml"
  "grpc-cert-manager.yaml"
  "networkpolicy-ingress-nginx.yaml"
  "networkpolicy-monitoring.yaml"
  "networkpolicy-otel-collector.yaml"
  "networkpolicy-strict.yaml"
)

cd "${REPO_ROOT}"
helm lint charts/dense-base
helm dependency build "${RENDERER_DIR}"

for values_file in "${VALUES_FILES[@]}"; do
  echo "rendering ${values_file}"
  helm template dense-base-smoke "${RENDERER_DIR}" \
    -f "${RENDERER_DIR}/values/${values_file}" \
    > "/tmp/${values_file}.rendered.yaml"
done
