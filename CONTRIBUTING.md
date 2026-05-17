# Contributing to gpuaudit

Thanks for helping improve gpuaudit.

## Build and test

```bash
git clone https://github.com/gpuaudit/cli.git
cd cli
go build -o gpuaudit ./cmd/gpuaudit
go test ./...
```

## Project layout

See [README.md](README.md) for architecture and scanner overview. Core packages live under `internal/` (analysis rules, cloud providers, output formatters, and the CLI in `cmd/gpuaudit`).

## Adding an analysis rule

1. Implement the rule in `internal/analysis/rules.go` (or a focused helper called from `analyzeInstance`).
2. Register the rule in the `analyzeInstance` flow so matching instances get recommendations.
3. Add tests under `internal/analysis/` covering the new signal.

## Adding an output format

1. Implement a formatter under `internal/output/`.
2. Wire the format into the CLI flag handling in `cmd/gpuaudit`.
3. Add tests with golden or snapshot output where practical.

## Adding a cloud provider

1. Add provider-specific scanning under `internal/cloud/` (follow existing AWS patterns).
2. Integrate the scanner into the main `scan` pipeline.
3. Document required credentials and flags in the README.

## Pull requests

- One feature or fix per PR
- Include tests for behavior changes
- Describe how you validated the change (`go test ./...`, sample scan output, etc.)
