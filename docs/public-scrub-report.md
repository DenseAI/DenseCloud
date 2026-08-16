# DenseCloud Public Scrub Report

Date: 2026-08-16
Baseline: `bf4eb77` plus the local `v1.1.0` release-candidate hardening

This report records the release-candidate scrub of the tracked working tree and
reachable Git history. It complements dependency and artifact validation; it is
not a substitute for provider-side credential revocation or organization policy
review.

## Result

- No known cloud access-key, GitHub token, Slack token, Google API key, or
  private-key signatures were found in tracked files.
- The same signature scan found no matches in patches reachable from refs.
- `git fsck --full --no-reflogs` completed without corrupt objects. Unreferenced
  commits left by the earlier public-history rewrite remain local Git objects and
  are not reachable from public refs.
- Public examples use placeholders or loopback endpoints. No customer
  identifiers, private registry names, or internal service endpoints were found.
- `LICENSE`, `NOTICE`, `THIRD_PARTY_NOTICES.md`, `SECURITY.md`, and
  `CONTRIBUTING.md` are present in the tracked tree.

## Deliberate Public Content

- DenseCore, DenseDiffusion, DenseOps, and DenseEnterprise names document the
  Dense Series ownership boundary and are intentional public product references.
- `DenseCloud_CTO_Report.md` is an intentionally public architecture reference.
  It describes ownership and validation boundaries without credentials,
  customer data, private infrastructure addresses, or unpublished commercial
  policy.
- `localhost` and `127.0.0.1` occur only in local defaults, tests, and smoke
  harnesses.

## Release Boundary

- The source repository, Go module, and `dense-base` Helm chart are public
  release surfaces.
- The Dockerfile produces a reference validation image, not a consumer-facing
  Dense product image.
- `DenseCloud_CTO_Report.md` is tracked in the public repository and remains an
  intentional public architecture reference.
- Ignored IDE files, local build outputs, caches, and generated chart archives
  such as `charts/dense-base/examples/renderer/charts/dense-base-1.1.0.tgz`
  are outside the tracked release tree.

## Remaining External Checks

- Confirm `v1.1.0` resolves from the public Git remote after tagging.
- Confirm `dense-base` 1.1.0 is anonymously pullable from GHCR after publication.
- Confirm GitHub branch protection and private vulnerability reporting remain
  enabled in repository settings.
