#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
destination="${1:-${repo_root}/dist}"
staging_dir="$(mktemp -d)"
trap 'rm -rf "${staging_dir}"' EXIT

mkdir -p "${destination}" "${staging_dir}/dense-base"
cp -R "${repo_root}/charts/dense-base/." "${staging_dir}/dense-base/"
install -m 0644 "${repo_root}/LICENSE" "${staging_dir}/dense-base/LICENSE"
install -m 0644 "${repo_root}/NOTICE" "${staging_dir}/dense-base/NOTICE"
install -m 0644 "${repo_root}/THIRD_PARTY_NOTICES.md" "${staging_dir}/dense-base/THIRD_PARTY_NOTICES.md"

chart_version="$(helm show chart "${staging_dir}/dense-base" | awk '/^version:/ { print $2 }')"
if [[ -z "${chart_version}" ]]; then
  echo "failed to determine dense-base chart version" >&2
  exit 1
fi

if [[ -n "${RELEASE_TAG:-}" ]]; then
  if [[ "${RELEASE_TAG#v}" == "${RELEASE_TAG}" ]]; then
    echo "RELEASE_TAG must be v-prefixed when set, got ${RELEASE_TAG}" >&2
    exit 1
  fi
  release_version="${RELEASE_TAG#v}"
  if [[ "${release_version}" != "${chart_version}" ]]; then
    echo "release tag ${RELEASE_TAG} does not match chart version ${chart_version}" >&2
    exit 1
  fi
fi

helm package "${staging_dir}/dense-base" --destination "${destination}" >/dev/null

chart_archive="${destination}/dense-base-${chart_version}.tgz"
if [[ ! -f "${chart_archive}" ]]; then
  echo "expected packaged chart archive ${chart_archive} was not created" >&2
  exit 1
fi

archive_listing="$(mktemp)"
trap 'rm -rf "${staging_dir}" "${archive_listing}"' EXIT
tar -tzf "${chart_archive}" > "${archive_listing}"

for required_path in \
  "dense-base/LICENSE" \
  "dense-base/NOTICE" \
  "dense-base/THIRD_PARTY_NOTICES.md"
do
  if ! grep -Fxq "${required_path}" "${archive_listing}"; then
    echo "packaged chart is missing required file ${required_path}" >&2
    exit 1
  fi
done

if grep -Eq '^dense-base/.*/.*\.(tgz|tar\.gz)$' "${archive_listing}"; then
  echo "packaged chart contains nested generated archives" >&2
  exit 1
fi

if grep -Eq '^dense-base/(build|dist|tmp|\.tmp|\.cache|\.codex|\.omx)/' "${archive_listing}"; then
  echo "packaged chart contains local build or tool output" >&2
  exit 1
fi
