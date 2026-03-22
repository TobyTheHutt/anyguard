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

## Execution model

- Analyzer/plugin path: golangci-lint loads `anyguard` as a `go/analysis` linter and runs one pass per package. Each pass reports only the findings owned by that package after applying the configured roots and `exclude_globs` to the same canonical repository-relative file identities used by repo-wide allowlist resolution.
- Repo-wide stale-selector validation still happens in that mode. The allowlist is resolved against repo-wide findings across the configured roots once per golangci-lint process and reused by later package passes.
- Audit path: the repo-wide validation helper used by this repository's tests and benchmarks walks the configured roots once, applies the active Go build context (`//go:build`, `GOOS`, `GOARCH`, `GOFLAGS=-tags=...`, file suffix constraints, and `CGO_ENABLED`), and returns the full violation set. That is the whole-repo audit reference point. The module plugin does not repeat that work on every package.

## Load mode and performance

- The module plugin requires `typesinfo` load mode.
- This increases golangci-lint package loading cost compared with a syntax-only plugin because packages are type checked before `anyguard` runs.
- The plugin also pays one repo-wide allowlist-validation cost per golangci-lint process so stale selectors remain fail-closed across the configured roots.
- It does not run a whole-repo diagnostic audit on every package. Only current-package diagnostics are emitted per pass.
- Compared with the audit path, the tradeoff is higher per-package `typesinfo` loading but no repeated repo scan for every package.
- Module-plugin diagnostics stay sorted by `file`, `line`, `column`, `category`, and `owner`, independent of configured root order, filesystem traversal order, and map iteration.
- Canonical finding identity and exact allowlist matching include `line` and `column` coordinates.
- Legacy allowlist selectors that omit coordinates are accepted only when `{path, owner, category}` still resolves to exactly one current finding.
- Measure the repository smoke path with:

```bash
/usr/bin/time -f 'elapsed=%E maxrss=%MKB' bash scripts/ci/run-golangci-plugin-smoke.sh
```

- Run the focused execution-model contract tests with:

```bash
go test ./internal/validation -run 'TestValidateAnyUsageAuditsWholeRepo|TestAnalyzerRunUsesRepoWideAllowlistValidation|TestAnalyzerRunReportsPackageLocalDiagnosticsAndReusesRepoValidation'
```

- Measure the local module-plugin benchmark path without building `custom-gcl`:

```bash
go test -bench=ModulePluginSmokePath -run=^$ ./plugin
```

- Run the focused analyzer/plugin perf-sanity benchmarks with:

```bash
go test -run=^$ -bench='BenchmarkAnalyzerRun|BenchmarkModulePluginSmokePath' -benchtime=1x ./internal/validation ./plugin
```

## Upstream readiness

For maintainers evaluating possible core inclusion:

- The normative spec is the root [`Behavior`](../../README.md#behavior), [`Comparison With Generic Ban-Pattern Linters`](../../README.md#comparison-with-generic-ban-pattern-linters), [`Allowlist Schema`](../../README.md#allowlist-schema), and [`Detection Contract`](../../README.md#detection-contract).
- Supported syntax categories are exactly the AST child slots in the detection contract. Every supported slot resolves the universe `any` alias. The only slots outside dedicated type positions are the three compatibility slots. Anything outside that list is out of scope and intentionally silent.
- Each finding has one exact identity: `{path, owner, category, line, column}`. Allowlist matching is exact on that identity.
- Legacy selectors without `line` and `column` are accepted only when their `{path, owner, category}` triple still resolves to exactly one current finding.
- Module-plugin execution is package-local for diagnostics, but stale-selector validation remains repo-wide across the configured roots and is reused across package passes in the same process.
- The analyzer fails closed on unresolved file identity, allowlist parse/validation errors, stale or ambiguous selectors, and traversal or parse failures.
- CLI, analyzer, and module-plugin reporting order is a compatibility guarantee: no scoring, no heuristic ranking, and stable sort order by `file`, `line`, `column`, `category`, and `owner`.
- Ordering does not depend on configured root order, filesystem traversal order, or map iteration.
- False positives should mostly come down to scope. There are no syntax-only slots. `*ast.CallExpr.Fun`, `*ast.IndexExpr.Index`, and `*ast.IndexListExpr.Indices[i]` remain supported compatibility slots, so cases such as `any(1)`, `Single[any]{}`, and `Box[int, any]{}` remain reportable by contract.
- Allowlist strictness is deliberate in schema version `2`: no broad file-level or owner-only exceptions, no duplicate selectors, no half-specified selector coordinates, and no selectors that fail to resolve to a current finding.
- Non-goals: type-parameter constraints, broader unsafe-dynamic-use detection, or claims that every finding is a bug or security issue.
- The root comparison section is the canonical answer to "why not just use an existing ban-pattern linter?": overlap exists at the policy level, but `anyguard` is specifically about exact exceptions, stale-selector rejection, and a documented syntax-slot contract.
- Treat this as a policy linter, not a detector. It enforces repository policy over `any`. Upstream inclusion is still not guaranteed.

## Run

```bash
./custom-gcl run -c .golangci.yml ./...
```

## Release and version pinning

- Keep `plugins[].version` pinned to the tag being released, for example `v2.0.0`.
- Module plugin support starts with `v1.0.0`. Do not pin below this version.
- The plugin entrypoint import path `github.com/tobythehutt/anyguard/plugin` is stable and versioned with module tags.
