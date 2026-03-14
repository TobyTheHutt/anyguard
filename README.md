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
- Parses AST and reports true type-position usage of `any`.
- Compares findings against an allowlist.
- Supports file-level and symbol-level exceptions, exclude globs, and specific `//nolint` instructions.
- Exception metadata is minimal. `description` is required.
- Exit `0`: no disallowed usage found.
- Exit `3`: one or more disallowed usages were reported.
- Exit `1`: analyzer/runtime/validation error.
- Exit `2`: invalid CLI usage or flag parsing error.
- On diagnostics, prints `file:line:column` and a reason.

### Detection Contract

`anyguard` is syntax driven. It only reports `any` when the identifier is the direct child of one of the AST slots below. Anything not listed is unsupported and is not detected or reported (you are welcome to contribute).

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
| `*ast.CallExpr` | `Fun` | `any(...)` forms matched by syntax only |
| `*ast.IndexExpr` | `Index` | `T[any]` and `value[any]` forms matched by syntax only |
| `*ast.IndexListExpr` | `Indices[i]` | `T[int, any]` style forms matched by syntax only |

Nested `any` is reportable only when the nested identifier still appears in one of those slots. For example, `map[string][]any` reports because the innermost `any` is the `Elt` of an `*ast.ArrayType`.

Unsupported and ambiguous cases:

- Type parameter constraints such as `func Use[T any](v T) {}` and `type Box[T any] struct{}`
- In those examples, `any` constrains `T`. It is not a concrete type position like `func Use(v any) {}` or `type Value = any`
- Any `any` occurrence whose direct parent child AST relationship is not listed above
- Identifier names, selectors, assignments, composite literal elements, return expressions, type switch case lists, comments, and string literals
- No type info is used. `any(...)`, `T[any]`, and `T[int, any]` are matched by syntax alone because those exact AST child slots are part of the compatibility contract
- Example false positive, a user function named `any` that is called as `any(1)` reports because the callee matches `*ast.CallExpr.Fun`
- Example false positive, a value named `any` used in `values[any]` reports because the index matches `*ast.IndexExpr.Index`
- Example false positive, `Box[int, any]` style syntax reports because the second slot matches `*ast.IndexListExpr.Indices[i]`
- These syntax-only matches can be suppressed with a file scoped or symbol scoped allowlist entry, or with `//nolint:anyguard` on the same line or the previous line

Finding identity:

- File identity is the normalized repository-relative path used for allowlist matching and `Error.File`
- Paths use slash separators and omit a leading `./`
- Owner identity is the first enclosing top level declaration range that contains the finding
- `*ast.TypeSpec` uses the type name
- `*ast.ValueSpec` uses declared names in source order
- `*ast.FuncDecl` uses the function name, or the receiver type name for methods
- Local declarations inside a function or method inherit that enclosing function or receiver type owner
- File scoped allowlist entries match by path only
- Symbol scoped allowlist entries match by `{path, owner}`
- If no owner resolves, symbol scoped matching fails closed and only a file scoped allowlist entry can suppress the finding

Failure semantics:

- Allowlist read, parse, and validation errors halt analysis with an error
- Root resolution, filesystem traversal, and Go parse errors halt CLI validation with an error
- Analyzer path resolution fails only after repository root, GOPATH, and package path derivation have all failed
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
- Integration docs and examples: `docs/golangci-lint/README.md`

#### core integration

For direct integration into `golangci-lint`, import the public analyzer entrypoint.

- Module path: `github.com/tobythehutt/anyguard`
- Analyzer constructor: `anyguard.NewAnalyzer()`

### License

Apache-2.0
