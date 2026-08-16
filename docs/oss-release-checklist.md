# DenseCloud OSS Release Checklist

Use this checklist before each public release.

## 1. Repository Hygiene

- Confirm no secrets, internal endpoints, private registry names, or customer
  identifiers exist in tracked files or examples.
- Confirm examples use placeholder image repositories and non-sensitive values.
- Confirm no internal-only roadmap, policy, or architecture documents remain in
  the repository history that should be rewritten or removed before release.
- Verify the default branch is stable and green in CI.

## 2. Legal and Policy

- `LICENSE` present and correct for Apache 2.0.
- `NOTICE` present.
- `SECURITY.md` present with a private reporting path.
- `CONTRIBUTING.md` present with contribution and review expectations.
- Verify third-party dependency licenses are compatible with Apache 2.0
  distribution.
- Verify the packaged Helm chart and reference image contain `LICENSE`,
  `NOTICE`, and `THIRD_PARTY_NOTICES.md`.

## 3. Public Documentation

- `README.md` explains what DenseCloud is and is not.
- Public installation instructions exist for Go and Helm consumers.
- Versioning and release channels are documented.
- Support boundaries and non-goals are explicit.

## 4. Release Mechanics

- Tag the repository root with the release version.
- Publish a GitHub Release with:
  - summary of Go runtime changes
  - summary of Helm chart changes
  - upgrade notes for downstream consumers
- Package and publish the `dense-base` chart to the OCI registry.
- Confirm the release tag matches the Helm chart version and has versioned
  release notes under `docs/releases/`.
- Confirm the packaged chart archive contains `LICENSE`, `NOTICE`, and
  `THIRD_PARTY_NOTICES.md`, and does not contain nested generated chart archives
  or local build output.
- Run `govulncheck` and resolve every reachable advisory before tagging.
- Verify `go get` and Helm dependency resolution succeed from a clean public
  environment.

## 5. GitHub Configuration

- Set repository visibility to public.
- Enable branch protection on `main`.
- Require CI checks before merge.
- Configure issue tracking and security reporting settings.
- Add CODEOWNERS if maintainer routing is needed.
- Replace placeholder values in:
  - `.github/CODEOWNERS`
  - `.github/ISSUE_TEMPLATE/config.yml`
- Confirm GitHub Releases changelog categories match the label taxonomy in use.

## 6. Post-Launch

- Validate the public README, Releases page, and package links.
- Smoke-test downstream consumption from a clean clone.
- Announce support expectations and versioning policy to downstream repos.
