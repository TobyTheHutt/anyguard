# golangci-lint module plugin integration

## Stable plugin entrypoint

- Module path: `github.com/tobythehutt/anyguard`
- Plugin import path: `github.com/tobythehutt/anyguard/plugin`
- Linter name in `.golangci.yml`: `anyguard`

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

## Run

```bash
./custom-gcl run -c .golangci.yml ./...
```

## Release and version pinning

- Keep `plugins[].version` pinned to a released tag such as `v1.0.0`.
- Update the pinned version only after a corresponding module tag/release is published.
- Module plugin support starts with `v1.0.0`. Do not pin below this version.
- The plugin entrypoint import path `github.com/tobythehutt/anyguard/plugin` is stable and versioned with module tags.
