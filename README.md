# JustLog3

[![CI](https://github.com/OAzizjon/justlog3/actions/workflows/ci.yml/badge.svg)](https://github.com/OAzizjon/justlog3/actions/workflows/ci.yml)
[![PyPI](https://img.shields.io/pypi/v/justlog3.svg)](https://pypi.org/project/justlog3/)
[![Go Reference](https://pkg.go.dev/badge/github.com/OAzizjon/justlog3/go.svg)](https://pkg.go.dev/github.com/OAzizjon/justlog3/go)
[![npm](https://img.shields.io/npm/v/justlog3.svg)](https://www.npmjs.com/package/justlog3)
[![License: MIT](https://img.shields.io/badge/License-MIT-green.svg)](LICENSE)

**The cheetah of logging.** Minimalistic, fast, zero-config loggers for Python, Go and
JavaScript/TypeScript.

## Introduction

Logging should cost you nothing and configure itself. Most loggers make you pay twice: once in
setup, once on every call. JustLog3 skips both. One line creates a logger, lines go to disk fifty
at a time, and your program keeps running while they do. In Python that is about **3× faster**
than `logging` and loguru; in Go, **43 ns and zero allocations** per line.

Turn on [JustLog3 Cloud](https://jl3-cloud.site) with one more line and every log line also
lands in a dashboard with search, sessions, Telegram alerts and an AI assistant that reads your
stack traces.

## Libraries

| | Install | Docs |
|---|---|---|
| **Python** 3.8+ | `pip install justlog3` | [python/README.md](python/README.md) |
| **Go** 1.23+ | `go get github.com/OAzizjon/justlog3/go` | [go/README.md](go/README.md) |
| **Node.js** 18+, TypeScript | `npm install justlog3` | [js/README.md](js/README.md) |

All three write the same line format, `PREFIX:2026-10-05 14:03:12.517 - message`, and speak the same
cloud protocol: batched, gzip-compressed, retried with the same sequence number so nothing is
stored twice.

## Benchmarks

| | justlog3 | Competitors |
|---|---|---|
| Python, file | **5.4 µs/call** | `logging` 16.2, loguru 17.2 |
| Python, 8 threads | **6.9 µs/call** | `logging` 26.2, loguru 26.5 |
| Go, file | **43 ns/op** | zerolog 181, zap 322, slog 648 (all buffered) |
| Go, 22 goroutines | **88 ns/op** | zerolog 302, zap 405, slog 507 (all buffered) |
| Node.js, file | **4.2 µs/call** | winston 5.3, pino 5.7 (sync) / 6.0 (async) |

Measured 2026-10-19 on one laptop. The scripts, raw output, setup and caveats are in
[benchmarks/](benchmarks/): run them on your own machine before quoting the numbers.

## Contributing

Bug reports, ideas and pull requests are welcome. Small, well-scoped tasks are labelled
[good first issue](https://github.com/OAzizjon/justlog3/labels/good%20first%20issue); questions
and show-and-tell go to [Discussions](https://github.com/OAzizjon/justlog3/discussions).

Tests: `go test ./...` in `go/`, `python test/test_cloud.py` in `python/` (needs `httpx`),
`npm install && npm test` in `js/`. CI runs all three on Linux and Windows, and Go on macOS too.

If JustLog3 saves you time, a star helps other developers find it.

## License

[MIT](LICENSE)
