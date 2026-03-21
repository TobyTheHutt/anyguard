# golangci-lint module plugin integration

## Stable plugin entrypoint

- Module path: `github.com/tobythehutt/anyguard`
- Plugin import path: `github.com/tobythehutt/anyguard/plugin`
- Linter name in `.golangci.yml`: `anyguard`
- Module-plugin diagnostics follow the same deterministic ordering compatibility guarantee as the CLI and public analyzer.
- The plugin uses the same AST-slot-driven contract as the CLI and public analyzer.
- It reports `any` only in explicitly supported AST child slots and resolves the universe `any` alias to suppress shadowed declarations.
- It is not a full type-position semantic classifier, so supported-slot cases such as `any(1)`, `Single[any]{}`, and `Box[int, any]{}` remain reportable by contract.

## Build a custom golangci-lint

1. Copy `docs/golangci-lint/.custom-gcl.yml` to your project root as `.custom-gcl.yml`.
2. Build the binary:

```bash
golangci-lint custom
```

This creates `./custom-gcl` by default.

## Enable the linter in `.golangci.yml`

```yaml
version: "2"

linters:
  default: none
  enable:
    - anyguard
  settings:
    custom:
      anyguard:
        type: module
        description: Enforce allowlisted any usage
        original-url: github.com/tobythehutt/anyguard
        settings:
          allowlist: internal/ci/any_allowlist.yaml
          roots:
            - ./...
          # Optional: override repository root path for allowlist resolution.
          # repo-root: /absolute/path/to/repo
```

## Supported settings

- `allowlist` (string): path to the YAML allowlist file. Default is `internal/ci/any_allowlist.yaml`.
- `roots` (string or list): roots to analyze. Default is `./...`.
- `repo-root` (string): optional repository root override for path resolution.

## Upstream readiness

For maintainers evaluating possible core inclusion:

- The normative spec is the root [`Behavior`](../../README.md#behavior), [`Comparison With Generic Ban-Pattern Linters`](../../README.md#comparison-with-generic-ban-pattern-linters), [`Allowlist Schema`](../../README.md#allowlist-schema), and [`Detection Contract`](../../README.md#detection-contract).
- Supported syntax categories are exactly the AST child slots enumerated in the detection contract. The contract is AST-slot-driven with universe-`any` alias resolution. Anything outside that list is out of scope and intentionally silent.
- Each finding has one exact identity: `{path, owner, category}`. Allowlist matching is exact on that identity only.
- The analyzer fails closed on unresolved file identity, allowlist parse/validation errors, stale or ambiguous selectors, and traversal or parse failures.
- CLI, analyzer, and module-plugin reporting order is a compatibility guarantee: no scoring, no heuristic ranking, and stable sort order by `file`, `line`, `column`, `category`, and `owner`.
- Ordering does not depend on configured root order, filesystem traversal order, or map iteration.
- The false-positive boundary is explicit in the detection contract. `*ast.CallExpr.Fun`, `*ast.IndexExpr.Index`, and `*ast.IndexListExpr.Indices[i]` still resolve the universe `any` alias to suppress shadowed declarations, but cases such as `any(1)`, `Single[any]{}`, and `Box[int, any]{}` remain reportable because those slots are supported by contract.
- Allowlist strictness is deliberate in schema version `2`: no broad file-level or owner-only exceptions, no duplicate selectors, and no selectors that fail to resolve to a current finding.
- Non-goals: type-parameter constraints, broader unsafe-dynamic-use detection, or claims that every finding is a bug or security issue.
- The root comparison section is the canonical answer to "why not just use an existing ban-pattern linter?": overlap exists at the policy level, but `anyguard` is specifically about exact exceptions, stale-selector rejection, and a documented syntax-slot contract.
- The right framing is policy linter, not detector. It enforces an explicit repository policy over `any`; upstream inclusion is still not guaranteed.

## Run

```bash
./custom-gcl run -c .golangci.yml ./...
```

## Release and version pinning

- Keep `plugins[].version` pinned to a released tag such as `v1.0.0`.
- Update the pinned version only after a corresponding module tag/release is published.
- Module plugin support starts with `v1.0.0`. Do not pin below this version.
- The plugin entrypoint import path `github.com/tobythehutt/anyguard/plugin` is stable and versioned with module tags.
