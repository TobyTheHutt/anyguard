# golangci-lint module plugin integration

## Stable plugin entrypoint

- Module path: `github.com/tobythehutt/anyguard/v2`
- Plugin import path: `github.com/tobythehutt/anyguard/v2/plugin`
- Linter name in `.golangci.yml`: `anyguard`
- Module-plugin diagnostics follow the same deterministic ordering compatibility guarantee as the CLI and public analyzer.
- The root README is the canonical reference for behavior and detection scope: [`Execution Model`](../../README.md#execution-model), [`Detection Contract`](../../README.md#detection-contract).

## Build a custom golangci-lint

1. Copy `docs/golangci-lint/.custom-gcl.yml` to your project root as `.custom-gcl.yml`.
2. Build the binary:

```bash
golangci-lint custom
```

This creates `./custom-gcl` by default.
The checked-in [`docs/golangci-lint/.custom-gcl.yml`](.custom-gcl.yml) example uses the versioned `v2` module and plugin import paths.

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

- golangci-lint runs `anyguard` as one `go/analysis` pass per package.
- Diagnostics stay package-local after applying the configured roots and `exclude_globs`.
- Repo-wide stale-selector validation is resolved once per golangci-lint process and reused across later package passes.
- The root README [`Execution Model`](../../README.md#execution-model) section is the canonical reference for the full model.

## Load mode and performance

- The module plugin requires `syntax` load mode.
- Matching, shadowed-`any` suppression, and stale-selector validation do not consume `types.Info`.
- The plugin pays one repo-wide allowlist-validation cost per golangci-lint process and does not rerun a whole-repo diagnostic audit on every package.
- Finding identity and reporting order stay exact and deterministic: `file`, `line`, `column`, `category`, `owner`.
- Measure the repository smoke path with:

```bash
/usr/bin/time -f 'elapsed=%E maxrss=%MKB' bash scripts/ci/run-golangci-plugin-smoke.sh
```

- Checked-in repo module-plugin regression baseline:

```bash
go test -run=^$ -bench=BenchmarkCheckedInRepoModulePluginPath -benchmem -benchtime=1x ./plugin
```

- Run the focused execution-model contract tests with:

```bash
go test ./internal/validation -run 'TestValidateAnyUsageAuditsWholeRepo|TestAnalyzerRunUsesRepoWideAllowlistValidation|TestAnalyzerRunReportsPackageLocalDiagnosticsAndReusesRepoValidation|TestCheckedInRepoAnalyzerReusesRepoValidationAcrossPackageSweep'
```

- Run the deterministic local module-plugin smoke benchmark without building `custom-gcl`:

```bash
go test -run=^$ -bench=BenchmarkModulePluginSmokePath -benchmem -benchtime=1x ./plugin
```

- Use `BenchmarkCheckedInRepoModulePluginPath` in CI and regression checks.

- For the full perf-sanity suite and benchmark meanings, see the root README [`Development`](../../README.md#development) section.

## Upstream readiness

For maintainers evaluating possible core inclusion:

- The normative spec is the root [`Behavior`](../../README.md#behavior), [`Comparison With Generic Ban-Pattern Linters`](../../README.md#comparison-with-generic-ban-pattern-linters), [`Allowlist Schema`](../../README.md#allowlist-schema), and [`Detection Contract`](../../README.md#detection-contract).
- The module plugin uses golangci-lint `syntax` load mode. Supported-slot matching, shadowed-`any` suppression, and repo-wide stale-selector validation run from parsed syntax plus lexical scope state rather than `types.Info`.
- Each finding has one exact identity: `{path, owner, category, line, column}`. Allowlist matching is exact on that identity.
- Legacy selectors without `line` and `column` are accepted only when their `{path, owner, category}` triple still resolves to exactly one current finding.
- Module-plugin execution is package-local for diagnostics, but stale-selector validation remains repo-wide across the configured roots and is reused across package passes in the same process.
- The analyzer fails closed on unresolved file identity, allowlist parse/validation errors, stale or ambiguous selectors, and traversal or parse failures.
- CLI, analyzer, and module-plugin reporting order is a compatibility guarantee: no scoring, no heuristic ranking, and stable sort order by `file`, `line`, `column`, `category`, and `owner`.
- Ordering does not depend on configured root order, filesystem traversal order, or map iteration.
- Treat this as a policy linter, not a detector. It enforces repository policy over `any`. Upstream inclusion is still not guaranteed.

## Run

```bash
./custom-gcl run -c .golangci.yml ./...
```

## Release and version pinning

- Keep `plugins[].version` pinned to the tag being released, for example `v2.0.2`.
- Module plugin support starts with `v1.0.0`. Do not pin below this version.
- The plugin entrypoint import path `github.com/tobythehutt/anyguard/v2/plugin` is stable and versioned with module tags.
