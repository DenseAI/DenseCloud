# DenseCloud Migration Plan

## Stage 1: Foundation (this repo)

- Build shared server runtime (`server`), middleware (`middleware`), and telemetry (`telemetry`)
- Build shared Helm library chart (`dense-base`)

## Stage 2: Product onboarding

- DenseDiffusion migrates HTTP runtime bootstrap + middleware to shared runtime
- DenseCore migrates common middleware chain to shared runtime while keeping domain-specific auth/redis/otel layers
- DenseCore and DenseDiffusion Helm charts render core resources via `dense-base`

## Stage 3: Cleanup

- Remove duplicated boilerplate from product repos
- Keep only product-specific handlers, adapters, and domain config
- Keep only non-core chart extensions (for example app-specific HPA/network policy/dashboard templates)
