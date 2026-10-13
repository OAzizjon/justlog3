# JustLog3

[![CI](https://github.com/OAzizjon/justlog3/actions/workflows/ci.yml/badge.svg)](https://github.com/OAzizjon/justlog3/actions/workflows/ci.yml)
[![PyPI](https://img.shields.io/pypi/v/justlog3.svg)](https://pypi.org/project/justlog3/)
[![Go Reference](https://pkg.go.dev/badge/github.com/OAzizjon/justlog3/go.svg)](https://pkg.go.dev/github.com/OAzizjon/justlog3/go)
[![License: MIT](https://img.shields.io/badge/License-MIT-green.svg)](LICENSE)

**The cheetah of logging.** Minimalistic, fast, zero-config loggers for Python and Go.

## Introduction

Logging should cost you nothing and configure itself. Most loggers make you pay twice: once in
setup, once on every call. JustLog3 skips both. One line creates a logger, lines go to disk fifty
at a time, and your program keeps running while they do. In Python that is about **3× faster**
than `logging` and loguru; in Go, **60 ns and zero allocations** per line.

Turn on [JustLog3 Cloud](https://jl3-cloud.site) with one more line and every log line also
lands in a dashboard with search, sessions, Telegram alerts and an AI assistant that reads your
stack traces.

## Libraries

| | Install | Docs |
|---|---|---|
| **Python** 3.8+ | `pip install justlog3` | [python/README.md](python/README.md) |
| **Go** 1.23+ | `go get github.com/OAzizjon/justlog3/go` | [go/README.md](go/README.md) |

Both write the same line format, `PREFIX:2026-10-05 14:03:12.517 - message`, and speak the same
cloud protocol: batched, gzip-compressed, retried with the same sequence number so nothing is
stored twice.

## Benchmarks

| | justlog3 | Competitors |
|---|---|---|
| Python, file | **9.8 µs/call** | `logging` 27.9, loguru 29.6 |
| Python, 8 threads | **11.8 µs/call** | `logging` 49.4, loguru 50.4 |
| Go, file | **60 ns/op** | zerolog 292, zap 559, slog 997 (all buffered) |
| Go, 22 goroutines | **141 ns/op** | zerolog 436, zap 576, slog 724 |

Setup, caveats and how to reproduce: [python/BENCHMARKS.md](python/BENCHMARKS.md),
[go/BENCHMARKS.md](go/BENCHMARKS.md).

## Contributing

Bug reports, ideas and pull requests are welcome. Small, well-scoped tasks are labelled
[good first issue](https://github.com/OAzizjon/justlog3/labels/good%20first%20issue); questions
and show-and-tell go to [Discussions](https://github.com/OAzizjon/justlog3/discussions).

Tests: `go test ./...` in `go/`, `python test/test_cloud.py` in `python/` (needs `httpx`). CI runs
both on Linux, Windows and macOS.

If JustLog3 saves you time, a star helps other developers find it.

## License

[MIT](LICENSE)
