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

helm package "${staging_dir}/dense-base" --destination "${destination}"
