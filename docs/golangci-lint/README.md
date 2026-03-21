# golangci-lint module plugin integration

## Stable plugin entrypoint

- Module path: `github.com/tobythehutt/anyguard`
- Plugin import path: `github.com/tobythehutt/anyguard/plugin`
- Linter name in `.golangci.yml`: `anyguard`
- Module-plugin diagnostics follow the same deterministic ordering compatibility guarantee as the CLI and public analyzer.
- The plugin uses the same AST-slot-driven contract as the CLI and public analyzer.
- It requires golangci-lint `typesinfo` load mode so supported-slot matching can use `analysis.Pass.TypesInfo`.
- It reports `any` only in explicitly supported AST child slots, and every supported slot resolves the universe `any` alias via `types.Info` to suppress shadowed declarations.
- The detection contract distinguishes semantically resolved type-position slots from the three semantically resolved compatibility slots, and there are no syntax-only slots left in the implementation.

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

## Load mode and performance

- The module plugin requires `typesinfo` load mode.
- This increases golangci-lint package loading cost compared with a syntax-only plugin because packages are type checked before `anyguard` runs.
- Finding identity, allowlist matching, and diagnostic ordering do not change.
- Measure the repository smoke path with:

```bash
/usr/bin/time -f 'elapsed=%E maxrss=%MKB' bash scripts/ci/run-golangci-plugin-smoke.sh
```

## Upstream readiness

For maintainers evaluating possible core inclusion:

- The normative spec is the root [`Behavior`](../../README.md#behavior), [`Comparison With Generic Ban-Pattern Linters`](../../README.md#comparison-with-generic-ban-pattern-linters), [`Allowlist Schema`](../../README.md#allowlist-schema), and [`Detection Contract`](../../README.md#detection-contract).
- Supported syntax categories are exactly the AST child slots in the detection contract. Every supported slot resolves the universe `any` alias. The only slots outside dedicated type positions are the three compatibility slots. Anything outside that list is out of scope and intentionally silent.
- Each finding has one exact identity: `{path, owner, category}`. Allowlist matching is exact on that identity only.
- The analyzer fails closed on unresolved file identity, allowlist parse/validation errors, stale or ambiguous selectors, and traversal or parse failures.
- CLI, analyzer, and module-plugin reporting order is a compatibility guarantee: no scoring, no heuristic ranking, and stable sort order by `file`, `line`, `column`, `category`, and `owner`.
- Ordering does not depend on configured root order, filesystem traversal order, or map iteration.
- False positives should mostly come down to scope. There are no syntax-only slots. `*ast.CallExpr.Fun`, `*ast.IndexExpr.Index`, and `*ast.IndexListExpr.Indices[i]` remain supported compatibility slots, so cases such as `any(1)`, `Single[any]{}`, and `Box[int, any]{}` remain reportable by contract.
- Allowlist strictness is deliberate in schema version `2`: no broad file-level or owner-only exceptions, no duplicate selectors, and no selectors that fail to resolve to a current finding.
- Non-goals: type-parameter constraints, broader unsafe-dynamic-use detection, or claims that every finding is a bug or security issue.
- The root comparison section is the canonical answer to "why not just use an existing ban-pattern linter?": overlap exists at the policy level, but `anyguard` is specifically about exact exceptions, stale-selector rejection, and a documented syntax-slot contract.
- Treat this as a policy linter, not a detector. It enforces repository policy over `any`. Upstream inclusion is still not guaranteed.

## Run

```bash
./custom-gcl run -c .golangci.yml ./...
```

## Release and version pinning

- Keep `plugins[].version` pinned to a released tag such as `v1.0.0`.
- Update the pinned version only after a corresponding module tag/release is published.
- Module plugin support starts with `v1.0.0`. Do not pin below this version.
- The plugin entrypoint import path `github.com/tobythehutt/anyguard/plugin` is stable and versioned with module tags.
