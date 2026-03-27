# DenseCloud Public Scrub Report

Date: 2026-03-21

This report records a lightweight pre-publication scrub of repository contents.
It is not a full history rewrite or credential audit.

## Summary

- No obvious plaintext credentials or private keys were found in the readable
  portions of the working tree.
- Public-facing examples already use placeholder image repositories where
  applicable.
- The repository still contains product-family references such as DenseCore,
  DenseDiffusion in public documentation and
  package paths. These appear intentional, but they should be reviewed for
  branding and ownership consistency before the repository is made public.
- Helm registry references were normalized to `ghcr.io/DenseAI/charts`
  so the docs match the planned public publishing workflow.

## Findings

### Low Risk

- Public docs reference internal product names:
  - `README.md`
  - `go/README.md`
  - `docs/migration.md`
  These do not look secret, but they are public positioning choices and should
  be confirmed deliberately.

- The Go module path is `github.com/DenseAI/DenseCloud` in `go.mod`.
  The module path has been updated to match the public GitHub organization.

### No Immediate Secret Exposure Observed

- No obvious `BEGIN ... PRIVATE KEY` blocks were found in the readable files.
- No obvious cloud access key patterns were found in the readable files.
- Example chart values use placeholders rather than real image locations.

## Scan Limitations

- Several paths returned `Input/output error` during local scanning, including
  parts of `go/`, `charts/dense-base/examples/`, and some git object storage.
- Because of those read failures, this report should not be treated as a
  complete repository audit.

## Required Follow-Up Before Public Launch

- Re-run the scrub once filesystem I/O issues are resolved.
- GitHub organization set to `DenseAI`; verified in `go.mod`, `README.md`,
  `go/README.md`, `docs/release.md`, and `charts/dense-base/README.md`.
- Review `docs/migration.md` and any strategy or report documents for content
  that is not intended for external readers.
