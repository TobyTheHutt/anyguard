# anyguard

A Go lint guard that enforces controlled use of the `any` type.

### Motivation

`any` can be useful at boundaries, but unmanaged usage spreads quickly and weakens type safety.
`anyguard` keeps usage explicit, reviewed, and auditable for CI and delivery pipelines.

### Get Started

```bash
# Example binary usage
anyguard -allowlist internal/ci/any_allowlist.yaml ./...
```

### Usage

```
Flags:

  -allowlist   path to allowlist YAML (default: internal/ci/any_allowlist.yaml)
  -roots       comma-separated directories to scan (default: ./...)
  -repo-root   repository root for path matching (default: auto-detect)

Packages:

  anyguard [flags] [packages]
```

### Behavior

- Scans `.go` files under configured roots.
- Parses AST (not raw text), so findings are true type-position usages of `any`.
- Compares findings against an allowlist.
- Supports file-level and symbol-level exceptions, exclude globs, and specific `//nolint` instructions.
- Exception metadata is intentionally minimal: the `description` field is required.
- Exit `0`: no disallowed usage found.
- Exit `1`: violations or runtime/validation errors.
- On failure, prints `file:line` and reason (including the offending code line snippet).

### Development

```bash
# Run tests
go test ./...

# Run lint
golangci-lint run
```

### golangci-lint module plugin

`anyguard` can be consumed as a golangci-lint module plugin.

- Stable plugin import path: `github.com/tobythehutt/anyguard/plugin`
- Plugin name in `.golangci.yml`: `anyguard`
- Integration docs and examples: `docs/golangci-lint/README.md`

### License

Apache-2.0
