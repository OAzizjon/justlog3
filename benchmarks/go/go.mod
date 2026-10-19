module github.com/OAzizjon/justlog3/benchmarks/go

go 1.24

require (
	github.com/OAzizjon/justlog3/go v0.0.0
	github.com/rs/zerolog v1.35.1
	go.uber.org/zap v1.28.0
)

require (
	github.com/mattn/go-colorable v0.1.14 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	go.uber.org/multierr v1.10.0 // indirect
	golang.org/x/sys v0.29.0 // indirect
)

// The library from this repository, not a published version.
replace github.com/OAzizjon/justlog3/go => ../../go
