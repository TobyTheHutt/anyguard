# anyguard

A Go analyzer and CLI that controls where `any` can be used.

### Why

`any` is useful at boundaries but unchecked usage spreads quickly and weakens type safety.  
`anyguard` enforces an allowlist so usage stays intentional.

### Get Started

```bash
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
- Parses AST, resolves identifiers semantically, and reports supported-slot usage of the universe `any` alias.
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
| Basic overlap | Usually bans an identifier, token, or textual pattern and reports matches. | Reports concrete `any` usage too, but only when the identifier resolves semantically to the universe alias in the supported AST slots. |
| Allowlist precision | Exceptions are often broad file, symbol, regex, or inline suppression patterns. | Each exception must match one exact selector: `{path, owner, category}`. Broad file-level or owner-only exceptions are not supported in schema version `2`. |
| Stale selector rejection | Suppressions can drift silently after refactors or when the original finding disappears. | Selectors that no longer resolve to a current finding are rejected as stale or typoed configuration. |
| Canonical finding identity | Findings are often tied to textual matches or positions only. | Each finding has one canonical identity captured as `{path, owner, category}`, and diagnostics are emitted in deterministic order. |
| Configuration hygiene | Config validation is often looser because the tool's job is just pattern matching. | Unknown, malformed, duplicate, ambiguous, and unresolved selectors are rejected and analysis fails closed. |
| Detection contract | Supported and unsupported cases are often implicit in the matcher. | The README defines the exact supported AST parent/child slots and explicitly documents unsupported and ambiguous cases as part of the public contract. |

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
    description: go/analysis Run API requires returning `any`
```

Each entry must provide an exact `selector` with the canonical `{path, owner, category}` finding identity.

- Unknown fields are rejected during YAML decoding
- Unknown categories are rejected during validation
- Missing or malformed selector objects are rejected during validation
- Duplicate selectors are rejected as ambiguous configuration
- Selectors that do not resolve to any collected finding are rejected as stale or typoed configuration
- Broad file-level or owner-only allowlist entries are not supported in version `2`

### Detection Contract

`anyguard` is slot driven. It only reports `any` when the identifier is the direct child of one of the AST slots below and resolves semantically to the Go universe `any` alias. Anything not listed is unsupported and is not detected or reported (you are welcome to contribute).

| Parent AST node | Child slot | Supported syntax |
| --- | --- | --- |
| `*ast.Field` | `Type` | Parameter types, result types, struct field types, and interface field or member types |
| `*ast.ValueSpec` | `Type` | Explicit variable declaration types |
| `*ast.TypeSpec` | `Type` | Type alias and type definition right hand sides |
| `*ast.TypeAssertExpr` | `Type` | Type assertions such as `value.(any)` |
| `*ast.ArrayType` | `Elt` | Array and slice element types |
| `*ast.MapType` | `Key`, `Value` | Map key and value types |
| `*ast.ChanType` | `Value` | Channel element types |
| `*ast.StarExpr` | `X` | Pointer target types |
| `*ast.Ellipsis` | `Elt` | Variadic parameter element types |
| `*ast.CallExpr` | `Fun` | Conversions such as `any(value)` when the callee resolves to the universe alias |
| `*ast.IndexExpr` | `Index` | Single-argument instantiations such as `Box[any]` when the index resolves to the universe alias |
| `*ast.IndexListExpr` | `Indices[i]` | Multi-argument instantiations such as `Box[int, any]` when the type argument resolves to the universe alias |

Nested `any` is reportable only when the nested identifier still appears in one of those slots. For example, `map[string][]any` reports because the innermost `any` is the `Elt` of an `*ast.ArrayType`.

#### Unsupported and ambiguous cases

- Type parameter constraints such as `func Use[T any](v T) {}` and `type Box[T any] struct{}`
- In those examples, `any` constrains `T`. It is not a concrete type position like `func Use(v any) {}` or `type Value = any`
- Any `any` occurrence whose direct parent child AST relationship is not listed above
- Identifier names, selectors, assignments, composite literal elements, return expressions, type switch case lists, comments, and string literals
- Each report requires semantic resolution to the universe `any` alias. Shadowed declarations such as `func any(v int)`, `values[any]` with a local index variable, or `type any interface{}; Box[int, any]{}` stay silent.
- On invalid or incomplete code, `anyguard` does not guess from bare syntax. It only reports when the identifier can still be resolved as the universe alias.
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
- Allowlist selectors match only by the exact collected `{path, owner, category}` identity
- Owner or category are never inferred during allowlist matching
- A selector that does not resolve to a current finding is treated as a configuration error

#### Failure semantics

- Allowlist read, parse, and validation errors halt analysis with an error
- Stale, unresolved, malformed, or ambiguous allowlist selectors halt analysis with an error
- Root resolution, filesystem traversal, and Go parse errors halt CLI validation with an error
- Analyzer path resolution fails closed when a file cannot be mapped to a canonical repository-relative path under the repository root
- Analyzer files with no filename or no token file are skipped
- Changing the supported slots above requires an explicit README update because this section is the public compatibility contract

### Development

```bash
# Run tests
go test ./...

# Run lint
golangci-lint run
```

### golangci-lint integration

#### module plugin

`anyguard` can run as a golangci-lint module plugin.

- Stable plugin import path: `github.com/tobythehutt/anyguard/plugin`
- Plugin name in `.golangci.yml`: `anyguard`
- Plugin diagnostics follow the same deterministic ordering contract as the CLI and public analyzer.
- Integration docs and examples: `docs/golangci-lint/README.md`
- Upstream readiness notes: `docs/golangci-lint/README.md#upstream-readiness`

#### core integration

For direct integration into `golangci-lint`, import the public analyzer entrypoint.

- Module path: `github.com/tobythehutt/anyguard`
- Analyzer constructor: `anyguard.NewAnalyzer()`
- Analyzer diagnostics follow the same deterministic ordering contract as the CLI and module plugin.

### License

Apache-2.0
