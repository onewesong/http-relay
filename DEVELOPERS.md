# Developers Guide

This document is for maintainers of this repository. It covers CI, release flow, and versioning.

## Local Development

- Run tests:

```bash
go test ./...
```

- Build locally:

```bash
go build ./cmd/http-relay
```

## CI

- Workflow file: `.github/workflows/ci.yml`
- Triggers: `push` (excluding `v*` tags) and `pull_request`
- Job: `go test ./...`

## Automated Release (GoReleaser)

- Workflow file: `.github/workflows/release.yml`
- GoReleaser config: `.goreleaser.yaml`
- Trigger: push a `v*` tag (for example: `v1.2.3`)
- Outputs:
  - Multi-platform binaries (linux/darwin/windows)
  - Archives (zip for windows, tar.gz for others)
  - `checksums.txt`
  - GitHub Release created automatically

## Release Steps

1. Ensure the main branch is healthy and tests pass.
2. Create and push a tag:

```bash
git tag v1.0.0
git push origin v1.0.0
```

3. Wait for the `Release` workflow in GitHub Actions to finish.

## Version Source

- `http-relay version` reads from `main.version` in `cmd/http-relay/main.go`.
- Release builds inject the version via GoReleaser ldflags:
  - `-X main.version={{.Version}}`
- Non-release builds default to `dev`.
