#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
RENDERER_DIR="${REPO_ROOT}/charts/dense-base/examples/renderer"
CLUSTER_NAME="${CLUSTER_NAME:-densecloud-smoke}"
KUBECONFIG_PATH="${KUBECONFIG:-/tmp/${CLUSTER_NAME}.kubeconfig}"
NAMESPACE="${NAMESPACE:-densecloud-smoke}"
KIND_NODE_IMAGE="${KIND_NODE_IMAGE:-}"
CRD_MANIFEST_PATH="/tmp/${CLUSTER_NAME}-smoke-crds.yaml"

detect_cgroup_version() {
  if [[ -f /sys/fs/cgroup/cgroup.controllers ]]; then
    echo "v2"
    return
  fi
  echo "v1"
}

cleanup() {
  kind delete cluster --name "${CLUSTER_NAME}" >/dev/null 2>&1 || true
}
trap cleanup EXIT

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
