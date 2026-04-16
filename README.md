# anyguard

A Go analyzer and CLI that controls where `any` can be used.

Release history lives in [`CHANGELOG.md`](CHANGELOG.md).

### Automated Releases

Release-prep pull requests may be squash-merged. The [`Release`](.github/workflows/release.yml) workflow validates release-prep pull requests before merge, then runs again after the final commit lands on `main`, creates the `vX.Y.Z` tag on that commit, and publishes the GitHub release with the changelog entry as the release body.

Use a pull request or squash commit title of `release: vX.Y.Z`, or make the top `CHANGELOG.md` release heading change to a new dated section. Release pull requests fail early when the version tag is malformed, the changelog entry is inconsistent, or the version tag already exists. After merge, if a matching version tag already exists on a different commit, the workflow fails instead of moving it.

### GitHub Release Notes

GitHub-generated release notes are configured in [`.github/release.yml`](.github/release.yml). That file only controls the generated GitHub Release UI and Releases API output.

Apply one of these labels to each merged pull request when generated release notes should place it in a specific section:

| Generated section | Labels |
| --- | --- |
| Breaking Changes | `breaking-change`, `semver-major` |
| Features | `enhancement`, `feature`, `semver-minor` |
| Fixes | `bug`, `fix` |
| Documentation | `documentation`, `docs` |
| Maintenance | `maintenance`, `chore`, `ci`, `refactor`, `dependencies` |
| Other Changes | any merged pull request that does not match an earlier section |

Use `skip-release-notes` or `release-note:skip` for pull requests that should stay out of generated release notes. Automation-only pull requests from `dependabot[bot]` and `github-actions[bot]` are excluded as release-note noise; label human-reviewed dependency, CI, or refactor work as `maintenance` when it should appear.

### Why

`any` is useful at boundaries but unchecked usage spreads quickly and weakens type safety.  
`anyguard` enforces an allowlist so usage stays intentional.

### Get Started

```bash
go install github.com/tobythehutt/anyguard/v2/cmd/anyguard@latest
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
- `anyguard` is AST-slot-driven with lexical resolution: it reports `any` in supported AST child slots, and every report requires the identifier to resolve to the Go universe `any` alias rather than a package, file, type-parameter, or local declaration.
- That supported-slot list is the public contract. Dedicated type-position slots and compatibility slots use the same universe-`any` resolution, which keeps shadowed declarations silent.
- The detection-contract table below distinguishes dedicated type-position slots from the three supported compatibility slots: `*ast.CallExpr.Fun`, `*ast.IndexExpr.Index`, and `*ast.IndexListExpr.Indices[i]`.
- There are no remaining syntax-only slots in the current implementation.
- `anyguard` is not a broader classifier for every type-like use. If your policy wants only dedicated type-position fields, the compatibility slots above are the only extra scope.
- Compares findings against an allowlist.
- Supports strict selector-based exceptions, exclude globs, and specific `//nolint` instructions.
- Exception metadata is minimal. `description` is required.
- Exit `0`: no disallowed usage found.
- Exit `3`: one or more disallowed usages were reported.
- Exit `1`: analyzer/runtime/validation error.
- Exit `2`: invalid CLI usage or flag parsing error.
- On diagnostics, prints `file:line:column` and a reason.
- CLI, analyzer, and golangci-lint plugin diagnostics are a compatibility guarantee: they are emitted deterministically in `file`, `line`, `column`, `category`, `owner` order, independent of root order, filesystem traversal, map iteration, formatting noise, and irrelevant comments.

### Comparison With Generic Ban-Pattern Linters

`anyguard` overlaps with generic identifier or pattern ban linters in one narrow way: both can help enforce "do not use `any` here" policy. If that is the whole requirement, a generic ban-pattern linter is simpler.

`anyguard` exists for the narrower case where `any` is allowed at a few explicit boundaries and those exceptions must stay exact, current, and reviewable.

| Concern | Generic ban-pattern linter | `anyguard` |
| --- | --- | --- |
| Basic overlap | Usually bans an identifier, token, or textual pattern and reports matches. | Reports `any` in supported AST child slots, and every supported slot uses lexical resolution of the universe `any` alias so shadowed declarations stay silent. The dedicated type-position slots and `*ast.CallExpr.Fun`, `*ast.IndexExpr.Index`, and `*ast.IndexListExpr.Indices[i]` remain deliberate compatibility slots for conversions and generic instantiations. |
| Allowlist precision | Exceptions are often broad file, symbol, regex, or inline suppression patterns. | Each exception resolves to one exact selector identity: `{path, owner, category, line, column}`. Legacy selectors without coordinates are accepted only when `{path, owner, category}` still resolves to exactly one current finding. Broad file-level or owner-only exceptions are not supported in schema version `2`. |
| Stale selector rejection | Suppressions can drift silently after refactors or when the original finding disappears. | Selectors that no longer resolve to a current finding are rejected as stale or typoed configuration. |
| Canonical finding identity | Findings are often tied to textual matches or positions only. | Each finding has one canonical identity captured as `{path, owner, category, line, column}`, and diagnostics are emitted in deterministic order. |
| Configuration hygiene | Config validation is often looser because the tool's job is just pattern matching. | Unknown, malformed, duplicate, ambiguous, and unresolved selectors are rejected and analysis fails closed. |
| Detection contract | Supported and unsupported cases are often implicit in the matcher. | The README defines the exact supported AST parent/child slots and documents unsupported and ambiguous cases as part of the public contract. |

The practical answer to "why not use an existing ban-pattern linter?" is:

- Use a generic ban-pattern linter when the policy is simply "match and ban `any`."
- Use `anyguard` when the policy is "allow only these exact `any` boundary usages, reject stale exceptions, and keep a stable contract for what counts as a finding."

### Allowlist Schema

The allowlist is strict configuration. The current schema version is `2`.

```yaml
version: 2
exclude_globs:
  - "**/*_test.go"
entries:
  - selector:
      path: internal/validation/analyzer.go
      owner: analyzerConfig
      category: "*ast.Field.Type"
      line: 84
      column: 54
    description: go/analysis Run API requires returning `any`
```

Each entry must resolve to one exact finding. The canonical finding identity is `{path, owner, category, line, column}`.

- Unknown fields are rejected during YAML decoding
- Unknown categories are rejected during validation
- Missing or malformed selector objects are rejected during validation
- Selectors that set only one of `line` or `column` are rejected during validation
- Duplicate selectors are rejected as ambiguous configuration
- Selectors that do not resolve to any collected finding are rejected as stale or typoed configuration
- Legacy selectors that omit `line` and `column` are accepted only when their `{path, owner, category}` triple resolves to exactly one current finding. Collisions are rejected as ambiguous configuration
- Broad file-level or owner-only allowlist entries are not supported in version `2`

### Detection Contract

`anyguard` is AST-slot driven. It reports `any` only when the identifier is the direct child of one of the AST slots below, and that supported-slot list is the public contract. Every supported slot uses the same lexical check: the matched identifier must resolve to the Go universe `any` alias with no shadowing package, file, type-parameter, or local declaration in scope. There are no syntax-only slots in the current implementation. The contract splits supported slots into dedicated type-position slots and compatibility slots because Go models conversions and generic instantiations with general expression nodes. Anything not listed is unsupported and is not detected or reported (you are welcome to contribute).

The syntax snippets in this section are mirrored in the corpus fixtures under `internal/validation/testdata/corpus/{supported,boundary,unsupported}` so the documented boundary stays testable.

| Parent AST node | Child slot | Resolution kind | Supported syntax |
| --- | --- | --- | --- |
| `*ast.Field` | `Type` | Lexical type-position slot | Parameter types, result types, struct field types, and interface field or member types |
| `*ast.ValueSpec` | `Type` | Lexical type-position slot | Explicit variable declaration types |
| `*ast.TypeSpec` | `Type` | Lexical type-position slot | Type alias and type definition right hand sides |
| `*ast.TypeAssertExpr` | `Type` | Lexical type-position slot | Type assertions such as `value.(any)` |
| `*ast.ArrayType` | `Elt` | Lexical type-position slot | Array and slice element types |
| `*ast.MapType` | `Key`, `Value` | Lexical type-position slot | Map key and value types |
| `*ast.ChanType` | `Value` | Lexical type-position slot | Channel element types |
| `*ast.StarExpr` | `X` | Lexical type-position slot | Pointer target types |
| `*ast.Ellipsis` | `Elt` | Lexical type-position slot | Variadic parameter element types |
| `*ast.CallExpr` | `Fun` | Lexical compatibility slot | Conversions such as `any(value)` when the callee resolves to the universe alias |
| `*ast.IndexExpr` | `Index` | Lexical compatibility slot | Single-argument instantiations such as `Box[any]` when the index resolves to the universe alias |
| `*ast.IndexListExpr` | `Indices[i]` | Lexical compatibility slot | Multi-argument instantiations such as `Box[int, any]` when the type argument resolves to the universe alias |

`*ast.CallExpr.Fun`, `*ast.IndexExpr.Index`, and `*ast.IndexListExpr.Indices[i]` remain compatibility slots, not syntax-only exceptions. They require the universe `any` alias, but they are supported because Go exposes conversions and generic instantiations through expression nodes instead of dedicated type-position AST fields.

Nested `any` is reportable only when the nested identifier still appears in one of those slots. For example, `type NestedArray map[string][]any` reports because the innermost `any` is still the `Elt` of an `*ast.ArrayType`.

#### Unsupported and compatibility notes

- Type parameter constraints stay silent because `any` constrains a type parameter instead of occupying a concrete supported slot, for example `func Use[T any](value T) {}`.
- Any `any` occurrence whose direct parent child AST relationship is not listed above stays silent. That includes identifier names, selectors, assignments, composite literal elements, return expressions, type switch case lists, comments, and string literals. Example: `func TypeSwitchCaseList(value interface{}) { switch value.(type) { case any, string: } }`.
- Each report requires lexical resolution to the universe `any` alias. Shadowed declarations stay silent. Examples include `func any(v int) int` with `any(1)`, a function that binds a local `any := 0` before indexing `values[any]`, and a file that defines `type any interface{}` before using `Box[int, any]{}`.
- False positives should mostly come down to scope. If your policy excludes the three compatibility slots above, `anyguard` will still report them. Shadowed declarations and unsupported syntax stay silent.
- On invalid or incomplete code, `anyguard` does not use package type checking to guess intent. It reports supported-slot bare `any` when lexical scope leaves it unshadowed as the universe alias.
- Exact allowlist selectors and `//nolint:anyguard` remain the escape hatches when a resolved universe-`any` usage is intentionally allowed

#### Finding identity

- File identity is the normalized repository-relative path used for allowlist matching and `Error.File`
- Paths use slash separators and omit a leading `./`
- Analyzer mode resolves file identity only from the configured or detected repository root
- If a file does not resolve canonically under that repository root, analysis fails closed with an error
- `anyguard` does not infer file identity from GOPATH layout, package import paths, or basename fallbacks
- Owner identity is derived directly from the owning syntax node at collection time, not from positional or range overlap
- `*ast.TypeSpec` uses the type name
- `*ast.ValueSpec` uses the first declared name in source order
- `*ast.FuncDecl` uses the function name, or the receiver type name for methods
- Local declarations inside a function or method inherit that enclosing function or receiver type owner
- Category identity is the supported AST slot label captured at collection time, for example `*ast.MapType.Value`
- Line and column identity are the exact 1-based source coordinates of the matched `any` token
- Canonical finding identity is the exact collected `{path, owner, category, line, column}` tuple
- Allowlist selectors with `line` and `column` match only by that exact collected identity
- Legacy selectors without coordinates are resolved only if their `{path, owner, category}` triple matches exactly one current finding
- Owner or category are never inferred during allowlist matching
- A selector that does not resolve to a current finding is treated as a configuration error

#### Failure semantics

- Allowlist read, parse, and validation errors halt analysis with an error
- Stale, unresolved, malformed, or ambiguous allowlist selectors halt analysis with an error
- Root resolution, filesystem traversal, and Go parse errors halt CLI validation with an error
- Analyzer path resolution fails closed when a repository file cannot be mapped to a canonical repository-relative path under the repository root
- Toolchain-generated synthetic `go test` main files outside the repository root are skipped because they are not canonical source files for reporting
- Analyzer files with no filename or no token file are skipped
- Changing the supported slots above requires an explicit README update because this section is the public compatibility contract

### Execution Model

- Analyzer/plugin path: the CLI (`cmd/anyguard`), public analyzer (`anyguard.NewAnalyzer()`), and golangci-lint module plugin all run as `go/analysis` frontends. Each pass emits diagnostics only for findings in the package currently under analysis after applying the configured roots and `exclude_globs` to the same canonical repository-relative file identities used by repo-wide allowlist resolution. The cached repo-wide validation result retains those findings indexed by canonical repository-relative path, so later passes filter cached findings instead of repeating package-local AST-slot collection.
- Repo-wide stale-selector validation still happens on that path. Allowlist resolution is built from repo-wide findings across the configured roots, cached once per process, and reused by later analyzer/plugin passes so stale selectors anywhere under those roots still fail closed.
- Audit path: the repo-wide validation helper used by this repository's tests and benchmarks walks the configured roots once, applies the active Go build context (`//go:build`, `GOOS`, `GOARCH`, `GOFLAGS=-tags=...`, file suffix constraints, and `CGO_ENABLED`), and returns the full repo violation set in a single call. That is the canonical whole-repo audit path.
- Performance tradeoff: analyzer/plugin execution avoids rescanning the repo for every package pass. On the golangci-lint module-plugin path, `anyguard` uses syntax package loading. Supported-slot matching, shadowed-`any` suppression, and repo-wide stale-selector validation read parsed syntax plus lexical scope state rather than `types.Info`. The audit path does one full repo walk and is the reference whole-repo measurement path.

### Development

```bash
# Run tests
go test ./...

# Run focused execution-model contract tests
go test ./internal/validation -run 'TestValidateAnyUsageAuditsWholeRepo|TestAnalyzerRunUsesRepoWideAllowlistValidation|TestAnalyzerRunReportsPackageLocalDiagnosticsAndReusesRepoValidation'

# Run benchmarks
go test -bench=. ./...

# Benchmark only (skip unit tests)
go test -bench=. -run=^$ ./...

# Run focused perf-sanity benchmarks
go test -run=^$ -bench='BenchmarkAnalyzerRun|BenchmarkModulePluginSmokePath' -benchtime=1x ./internal/validation ./plugin

# Run lint
golangci-lint run

# Run the golangci-lint module-plugin smoke test
bash scripts/ci/run-golangci-plugin-smoke.sh
```

The benchmark suite includes `ValidateAnyUsage`, repo-wide finding collection and allowlist resolution helpers, analyzer `Run` in `cold-pass` and `reused-pass` modes, and the golangci-lint module-plugin smoke path. The focused execution-model tests and perf-sanity benchmarks above are the maintainers' contract for package-local analyzer diagnostics plus repo-wide stale-selector validation.

### golangci-lint integration

#### module plugin

`anyguard` can run as a golangci-lint module plugin.

- Stable plugin import path: `github.com/tobythehutt/anyguard/v2/plugin`
- Plugin name in `.golangci.yml`: `anyguard`
- Plugin diagnostics follow the same deterministic ordering contract as the CLI and public analyzer.
- The module plugin requests golangci-lint `syntax` load mode. Supported-slot matching, shadowed-`any` suppression, and repo-wide stale-selector validation use parsed syntax plus lexical scope resolution rather than `types.Info`.
- The module plugin reports diagnostics for the current package only. Repo-wide allowlist and stale-selector validation are shared across the golangci-lint process and are not redone as a whole-repo audit for every package.
- Integration docs and examples: `docs/golangci-lint/README.md`
- Upstream readiness notes: `docs/golangci-lint/README.md#upstream-readiness`

#### core integration

For direct integration into `golangci-lint`, import the public analyzer entrypoint.

- Module path: `github.com/tobythehutt/anyguard/v2`
- Analyzer constructor: `anyguard.NewAnalyzer()`
- The analyzer runs despite errors and uses lexical scope resolution for supported-slot `any` matching.
- Analyzer diagnostics follow the same deterministic ordering contract as the CLI and module plugin.

```go
import anyguard "github.com/tobythehutt/anyguard/v2"
```

```bash
go get github.com/tobythehutt/anyguard/v2@v2.0.2
```

### License

Apache-2.0
