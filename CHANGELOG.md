# Changelog

All notable changes to this project will be documented in this file.

The format is based on Keep a Changelog, with the current development state tracked in the `Unreleased` section at the top.

## [Unreleased]

## [2.0.1] - 2026-03-22

### Fixed

- Tightened canonical finding identity with exact `line` and `column` coordinates so one allowlist entry can no longer silently suppress multiple same-file, same-owner, same-category findings. Legacy three-field selectors now resolve only when unique and otherwise fail closed as ambiguous. (@TobyTheHutt)
- Aligned repo-wide audit file discovery with the active Go build context so inactive `//go:build`, custom `GOFLAGS=-tags=...`, `GOOS`, `GOARCH`, file-suffixed, and cgo-gated files no longer create stale-selector or violation mismatches versus analyzer and plugin runs. (@TobyTheHutt)
- Honored `exclude_globs` on the analyzer, CLI, and golangci-lint package-local diagnostic path so those frontends now use the same repository-relative file set as repo-wide allowlist resolution. (@TobyTheHutt)
- Skipped synthetic `go test` main files outside `repoRoot` on the analyzer, CLI, and singlechecker path so `./...` stays stable on normal modules with `_test.go` files while real repo files still fail closed on ambiguous identity. (@TobyTheHutt)

## [2.0.0] - 2026-03-22

### Added

- Added corpus coverage for analyzer behavior, allowlist hygiene, explicit boundary cases, safe-slot shadowing, and hot-path benchmarks for validation, analyzer, and module-plugin execution. (@TobyTheHutt)
- Added execution-model contract tests that distinguish the repo-wide audit path from package-local analyzer and module-plugin diagnostics, including coverage for cached repo-wide stale-selector validation reuse. (@TobyTheHutt)
- Added focused benchmark commands and smoke-test guidance for analyzer, validation, and golangci-lint plugin paths to make performance regressions easier to catch before release. (@TobyTheHutt)

### Changed

- Formalized the public detection contract around explicit AST slots, exact allowlist selector identity, and the documented differences between anyguard and generic ban-pattern linters. (@TobyTheHutt)
- Documented upstream-readiness expectations for golangci-lint integration and clarified the slot-resolution contract for supported `any` usage. (@TobyTheHutt)
- Introduced canonical finding identity and strict v2 allowlist selectors as the stable basis for exact matching and stale-selector rejection. (@TobyTheHutt)
- Centralized semantic `any` resolution and updated the analyzer to rely on `types.Info` for supported-slot matching instead of bare syntax. (@TobyTheHutt)
- Cached repo-wide analyzer validation across repeated analyzer passes to avoid unnecessary rescans while preserving repo-wide stale-selector validation. (@TobyTheHutt)
- Clarified the execution model in the root README and golangci-lint integration docs: CLI, public analyzer, and module plugin runs emit package-local diagnostics, while repo-wide stale-selector validation remains fail-closed across configured roots and is reused once per process. (@TobyTheHutt)
- Tightened the analyzer internals used by tests so repo-wide validation loading can be observed safely without mutating package-global state. (@TobyTheHutt)

### Fixed

- Pinned all CI `golangci-lint-action` runs to golangci-lint `v2.7.2` so local, plugin-smoke, and workflow lint behavior stay reproducible across the repository. (@TobyTheHutt)
- Guaranteed deterministic ordering across CLI, analyzer, and module-plugin output by sorting findings consistently and preserving stable report order. (@TobyTheHutt)
- Fail-closed handling now rejects ambiguous analyzer file identity instead of guessing canonical paths. (@TobyTheHutt)
- Supported-slot matching now resolves `any` semantically against the universe alias across declaration slots and composite compatibility slots, keeping shadowed or user-defined `any` silent. (@TobyTheHutt)

## [1.0.0] - 2026-03-13

### Changed

- Aligned the repository with golangci-lint's new-linter readiness requirements for the `v1.0.0` release. (@TobyTheHutt)

## [0.3.0] - 2026-03-12

### Added

- Exposed a public analyzer API for embedding anyguard into other tools and cleaned up the integration documentation around that public entrypoint. (@TobyTheHutt)

### Changed

- Aligned the documented exit-code behavior with `singlechecker` so CLI expectations match the analyzer-based implementation. (@TobyTheHutt)

## [0.2.0] - 2026-03-11

### Added

- Added golangci-lint module-plugin integration, custom-binary build support, versioned smoke fixtures, and CI coverage for the supported plugin flow. (@TobyTheHutt)

### Changed

- Migrated anyguard from the original standalone guard implementation to a `go/analysis` analyzer, which changed the core integration model for downstream users. (@TobyTheHutt)
- Simplified test-lint maintenance by removing the `goconst` test exclusion and deduplicating repeated literals. (@TobyTheHutt)

### Fixed

- Hardened custom golangci-lint build steps and pinned compatibility to golangci-lint `v2.7.2` so plugin smoke runs behave predictably in CI. (@TobyTheHutt)

## [0.1.0] - 2026-03-04

### Added

- Initial anyguard release with YAML allowlist support, repository scanning, and CI bootstrap for enforcing controlled `any` usage. (@TobyTheHutt)

[Unreleased]: https://github.com/tobythehutt/anyguard/compare/v2.0.1...HEAD
[2.0.1]: https://github.com/tobythehutt/anyguard/compare/v2.0.0...v2.0.1
[2.0.0]: https://github.com/tobythehutt/anyguard/compare/v1.0.0...v2.0.0
[1.0.0]: https://github.com/tobythehutt/anyguard/compare/v0.3.0...v1.0.0
[0.3.0]: https://github.com/tobythehutt/anyguard/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/tobythehutt/anyguard/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/tobythehutt/anyguard/releases/tag/v0.1.0
