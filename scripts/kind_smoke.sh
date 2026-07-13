#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
RENDERER_DIR="${REPO_ROOT}/charts/dense-base/examples/renderer"
CLUSTER_NAME="${CLUSTER_NAME:-densecloud-smoke-$$}"
CLUSTER_OWNED="false"
KUBECONFIG_PATH="${KUBECONFIG_PATH:-}"
KUBECONFIG_OWNED="false"
if [[ -z "${KUBECONFIG_PATH}" ]]; then
  KUBECONFIG_PATH="$(mktemp "/tmp/${CLUSTER_NAME}.XXXXXX.kubeconfig")"
  KUBECONFIG_OWNED="true"
fi
NAMESPACE="${NAMESPACE:-densecloud-smoke}"
KIND_NODE_IMAGE="${KIND_NODE_IMAGE:-}"
SKIP_RUNTIME_SMOKE="${SKIP_RUNTIME_SMOKE:-false}"
IMAGE_REPOSITORY="${IMAGE_REPOSITORY:-densecloud-kind-smoke}"
IMAGE_TAG="${IMAGE_TAG:-local}"
IMAGE_REF="${IMAGE_REPOSITORY}:${IMAGE_TAG}"
LOCAL_PORT="${LOCAL_PORT:-18081}"
SERVICE_PORT="${SERVICE_PORT:-8080}"
SMOKE_RETRIES="${SMOKE_RETRIES:-30}"
SMOKE_SLEEP_SECONDS="${SMOKE_SLEEP_SECONDS:-2}"
CRD_MANIFEST_PATH="/tmp/${CLUSTER_NAME}-smoke-crds.yaml"
RUNTIME_VALUES_PATH="/tmp/${CLUSTER_NAME}-runtime-values.yaml"
RUNTIME_MANIFEST_PATH="/tmp/${CLUSTER_NAME}-runtime-manifest.yaml"
PORT_FORWARD_LOG="/tmp/${CLUSTER_NAME}-port-forward.log"
PORT_FORWARD_PID=""

require_command() {
  local cmd="$1"
  if ! command -v "${cmd}" >/dev/null 2>&1; then
    echo "missing required command: ${cmd}" >&2
    exit 1
  fi
}

detect_cgroup_version() {
  if [[ -f /sys/fs/cgroup/cgroup.controllers ]]; then
    echo "v2"
    return
  fi
  echo "v1"
}

cleanup() {
  if [[ -n "${PORT_FORWARD_PID}" ]] && kill -0 "${PORT_FORWARD_PID}" >/dev/null 2>&1; then
    kill "${PORT_FORWARD_PID}" >/dev/null 2>&1 || true
    wait "${PORT_FORWARD_PID}" 2>/dev/null || true
  fi
  if [[ "${CLUSTER_OWNED}" == "true" ]]; then
    kind delete cluster --name "${CLUSTER_NAME}" >/dev/null 2>&1 || true
  fi
  rm -f \
    "${CRD_MANIFEST_PATH}" \
    "${RUNTIME_VALUES_PATH}" \
    "${RUNTIME_MANIFEST_PATH}" \
    "${PORT_FORWARD_LOG}"
  if [[ "${KUBECONFIG_OWNED}" == "true" ]]; then
    rm -f "${KUBECONFIG_PATH}"
  fi
}
trap cleanup EXIT

wait_for_http_ok() {
  local path="$1"
  local pattern="${2:-}"
  local body_file
  local attempt
  local status_code
  local url="http://127.0.0.1:${LOCAL_PORT}${path}"

  body_file="$(mktemp "/tmp/${CLUSTER_NAME}.XXXXXX.body")"
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
  echo "port-forward log:" >&2
  cat "${PORT_FORWARD_LOG}" >&2 || true
  echo "deployment state:" >&2
  kubectl get pods,svc,deploy -n "${NAMESPACE}-minimal" >&2 || true
  exit 1
}

write_smoke_crds() {
  cat > "${CRD_MANIFEST_PATH}" <<'EOF'
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: certificates.cert-manager.io
spec:
  group: cert-manager.io
  names:
    kind: Certificate
    plural: certificates
    singular: certificate
  scope: Namespaced
  versions:
    - name: v1
      served: true
      storage: true
      schema:
        openAPIV3Schema:
          type: object
          x-kubernetes-preserve-unknown-fields: true
---
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: scaledobjects.keda.sh
spec:
  group: keda.sh
  names:
    kind: ScaledObject
    plural: scaledobjects
    singular: scaledobject
  scope: Namespaced
  versions:
    - name: v1alpha1
      served: true
      storage: true
      schema:
        openAPIV3Schema:
          type: object
          x-kubernetes-preserve-unknown-fields: true
---
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: servicemonitors.monitoring.coreos.com
spec:
  group: monitoring.coreos.com
  names:
    kind: ServiceMonitor
    plural: servicemonitors
    singular: servicemonitor
  scope: Namespaced
  versions:
    - name: v1
      served: true
      storage: true
      schema:
        openAPIV3Schema:
          type: object
          x-kubernetes-preserve-unknown-fields: true
EOF
}

install_smoke_crds() {
  write_smoke_crds
  kubectl apply -f "${CRD_MANIFEST_PATH}" >/dev/null
  kubectl wait --for=condition=Established crd/certificates.cert-manager.io --timeout=60s >/dev/null
  kubectl wait --for=condition=Established crd/scaledobjects.keda.sh --timeout=60s >/dev/null
  kubectl wait --for=condition=Established crd/servicemonitors.monitoring.coreos.com --timeout=60s >/dev/null
}

apply_values_manifest() {
  local values_file="$1"
  local namespace="$2"
  local manifest_path="/tmp/${CLUSTER_NAME}-${values_file%.yaml}.yaml"

  helm template dense-base-smoke "${RENDERER_DIR}" \
    -f "${RENDERER_DIR}/values/${values_file}" \
    > "${manifest_path}"

  kubectl create namespace "${namespace}" --dry-run=client -o yaml | kubectl apply -f - >/dev/null
  kubectl apply -n "${namespace}" -f "${manifest_path}" >/dev/null
  kubectl get deploy,svc,sa,ingress,networkpolicy,certificate,scaledobject,servicemonitor \
    -n "${namespace}" \
    --ignore-not-found
}

build_runtime_override_values() {
  cat > "${RUNTIME_VALUES_PATH}" <<EOF
dense-base:
  image:
    repository: ${IMAGE_REPOSITORY}
    tag: ${IMAGE_TAG}
    pullPolicy: IfNotPresent
  service:
    enabled: true
    port: 8080
  probes:
    liveness:
      initialDelaySeconds: 0
      periodSeconds: 1
      timeoutSeconds: 1
      failureThreshold: 10
    readiness:
      initialDelaySeconds: 0
      periodSeconds: 1
      timeoutSeconds: 1
      failureThreshold: 10
    startup:
      initialDelaySeconds: 0
      periodSeconds: 1
      timeoutSeconds: 1
      failureThreshold: 30
EOF
}

apply_runtime_manifest() {
  local namespace="$1"

  build_runtime_override_values
  helm template dense-base-smoke "${RENDERER_DIR}" \
    -f "${RENDERER_DIR}/values/minimal.yaml" \
    -f "${RUNTIME_VALUES_PATH}" \
    > "${RUNTIME_MANIFEST_PATH}"

  kubectl apply -n "${namespace}" -f "${RUNTIME_MANIFEST_PATH}" >/dev/null
}

run_runtime_smoke() {
  local runtime_namespace="${NAMESPACE}-minimal"
  local deployment_name
  local service_name

  require_command docker
  require_command curl

  docker build -t "${IMAGE_REF}" -f "${REPO_ROOT}/Dockerfile" "${REPO_ROOT}"
  kind load docker-image "${IMAGE_REF}" --name "${CLUSTER_NAME}"

  apply_runtime_manifest "${runtime_namespace}"

  deployment_name="$(
    kubectl get deployment -n "${runtime_namespace}" \
      -l app.kubernetes.io/instance=dense-base-smoke \
      -o jsonpath='{.items[0].metadata.name}'
  )"
  service_name="$(
    kubectl get service -n "${runtime_namespace}" \
      -l app.kubernetes.io/instance=dense-base-smoke \
      -o jsonpath='{.items[0].metadata.name}'
  )"

  kubectl rollout status "deployment/${deployment_name}" -n "${runtime_namespace}" --timeout=180s >/dev/null

  kubectl -n "${runtime_namespace}" port-forward "service/${service_name}" "${LOCAL_PORT}:${SERVICE_PORT}" \
    > "${PORT_FORWARD_LOG}" 2>&1 &
  PORT_FORWARD_PID=$!

  wait_for_http_ok "/health/startup"
  wait_for_http_ok "/health/live"
  wait_for_http_ok "/health/ready"
  wait_for_http_ok "/metrics" 'densecloud_http_requests_total'
  wait_for_http_ok "/v1/hello" '"status"[[:space:]]*:[[:space:]]*"ok"'
}

require_command helm
require_command kind
require_command kubectl

cd "${REPO_ROOT}"
helm dependency build "${RENDERER_DIR}"

cgroup_version="$(detect_cgroup_version)"
if [[ "${cgroup_version}" == "v1" && -z "${KIND_NODE_IMAGE}" ]]; then
  KIND_NODE_IMAGE="kindest/node:v1.29.14"
fi

kind_args=(create cluster --name "${CLUSTER_NAME}" --wait 120s)
if [[ -n "${KIND_NODE_IMAGE}" ]]; then
  kind_args+=(--image "${KIND_NODE_IMAGE}")
fi

echo "host cgroup=${cgroup_version}"
if [[ -n "${KIND_NODE_IMAGE}" ]]; then
  echo "kind node image=${KIND_NODE_IMAGE}"
fi

if kind get clusters 2>/dev/null | grep -Fxq "${CLUSTER_NAME}"; then
  echo "refusing to reuse existing kind cluster: ${CLUSTER_NAME}" >&2
  exit 1
fi
CLUSTER_OWNED="true"
kind "${kind_args[@]}"
kind get kubeconfig --name "${CLUSTER_NAME}" > "${KUBECONFIG_PATH}"
export KUBECONFIG="${KUBECONFIG_PATH}"

kubectl wait --for=condition=Ready node --all --timeout=120s >/dev/null
install_smoke_crds

apply_values_manifest "minimal.yaml" "${NAMESPACE}-minimal"
apply_values_manifest "ingress-cert-manager.yaml" "${NAMESPACE}-ingress"
apply_values_manifest "grpc-cert-manager.yaml" "${NAMESPACE}-grpc"
apply_values_manifest "networkpolicy-ingress-nginx.yaml" "${NAMESPACE}-networkpolicy-ingress"
apply_values_manifest "networkpolicy-monitoring.yaml" "${NAMESPACE}-networkpolicy-monitoring"
apply_values_manifest "networkpolicy-otel-collector.yaml" "${NAMESPACE}-networkpolicy-otel"
apply_values_manifest "networkpolicy-strict.yaml" "${NAMESPACE}-networkpolicy"
apply_values_manifest "keda-custom-trigger.yaml" "${NAMESPACE}-keda"

if [[ "${SKIP_RUNTIME_SMOKE}" == "true" ]]; then
  echo "skipping runtime smoke; manifest apply matrix completed"
  exit 0
fi

run_runtime_smoke
echo "kind runtime smoke passed for ${IMAGE_REF}"
