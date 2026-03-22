module github.com/tobythehutt/anyguard/v2

go 1.22.0

require (
	// Keep v0.1.1 to retain Go 1.22 compatibility (v0.1.2 requires Go 1.24+).
	github.com/golangci/plugin-module-register v0.1.1
	// Keep v0.30.0 to retain Go 1.22 compatibility (newer releases require newer Go).
	golang.org/x/tools v0.30.0
	gopkg.in/yaml.v3 v3.0.1
)

require golang.org/x/sync v0.11.0

require golang.org/x/mod v0.23.0 // indirect
