# Contributing

## Scope

DenseCloud accepts contributions that improve the shared platform layer:

- HTTP/gRPC runtime lifecycle
- middleware and telemetry
- Helm chart primitives and validation
- portability, testing, and documentation for shared infrastructure

Changes that introduce product-specific business logic, model behavior, or
repo-local deployment assumptions should stay in consumer repositories.

## Development Expectations

- Keep shared APIs generic and reusable.
- Preserve semantic versioning expectations for public consumers.
- Add or update tests when behavior changes.
- Update documentation when interfaces, values, or release steps change.

## Pull Requests

Before opening a pull request:

- run relevant Go and Helm validation locally
- keep the change narrowly scoped
- explain any breaking change explicitly
- note downstream migration impact when applicable

## Review Criteria

Review prioritizes:

- backwards compatibility
- operational safety
- clarity of public contracts
- documentation and test coverage

## Licensing

By contributing to DenseCloud, you agree that your contribution will be
licensed under the Apache License, Version 2.0.
