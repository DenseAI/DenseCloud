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
  "keda-custom-trigger.yaml"
)

cd "${REPO_ROOT}"
helm lint charts/dense-base
helm dependency build "${RENDERER_DIR}"
helm lint "${RENDERER_DIR}"

for values_file in "${VALUES_FILES[@]}"; do
  echo "rendering ${values_file}"
  helm template dense-base-smoke "${RENDERER_DIR}" \
    -f "${RENDERER_DIR}/values/${values_file}" \
    > "/tmp/${values_file}.rendered.yaml"
done

if grep -q 'kind: ScaledObject' "/tmp/minimal.yaml.rendered.yaml"; then
  echo "minimal.yaml unexpectedly rendered a KEDA ScaledObject" >&2
  exit 1
fi

if ! grep -q 'kind: ScaledObject' "/tmp/keda-custom-trigger.yaml.rendered.yaml"; then
  echo "keda-custom-trigger.yaml did not render a KEDA ScaledObject" >&2
  exit 1
fi

if grep -q 'vector(0)' "/tmp/keda-custom-trigger.yaml.rendered.yaml"; then
  echo "keda-custom-trigger.yaml rendered the removed placeholder query" >&2
  exit 1
fi

echo "rendering keda-missing-triggers.yaml (expected failure)"
if helm template dense-base-smoke "${RENDERER_DIR}" \
  -f "${RENDERER_DIR}/values/keda-missing-triggers.yaml" \
  > /tmp/keda-missing-triggers.yaml.rendered.yaml 2> /tmp/keda-missing-triggers.yaml.error; then
  echo "keda-missing-triggers.yaml rendered successfully, expected validation failure" >&2
  exit 1
fi

if ! grep -q 'keda.triggers.custom must include at least one trigger' /tmp/keda-missing-triggers.yaml.error; then
  echo "keda-missing-triggers.yaml failed with an unexpected error:" >&2
  cat /tmp/keda-missing-triggers.yaml.error >&2
  exit 1
fi
