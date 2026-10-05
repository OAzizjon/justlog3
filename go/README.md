# JustLog3 for Go

[![Go Reference](https://pkg.go.dev/badge/github.com/OAzizjon/justlog3/go.svg)](https://pkg.go.dev/github.com/OAzizjon/justlog3/go)
[![Go Report Card](https://goreportcard.com/badge/github.com/OAzizjon/justlog3/go)](https://goreportcard.com/report/github.com/OAzizjon/justlog3/go)
[![License: MIT](https://img.shields.io/badge/License-MIT-green.svg)](LICENSE)

**The cheetah of logging**, Go edition. Go port of [JustLog3](https://github.com/OAzizjon/justlog3).

```sh
go get github.com/OAzizjon/justlog3/go
```

Requires Go 1.23 or newer. The package name is `justlog3`.

## Introduction

zap and zerolog are brilliant at building structured records. Most of the time you just want a
line in a file, and you want it now. JustLog3 writes one in **60 ns** with **zero
allocations**. Buffered zerolog takes 292 ns, buffered zap 559 ns, `log/slog` 997 ns. With 22
goroutines hammering it: **141 ns** against 436, 576 and 724.

No encoder config, no sinks, no field builders, no dependencies outside the standard library.
`NewLogger("app.log")` and you are logging. `SetAPIToken(...)` and every line also lands in
JustLog3 Cloud, with search, Telegram alerts and an AI that reads your stack traces.

Need JSON fields and sampling? Use zap, seriously. Need to log? You're in the right place.

## Quick start

```go
import justlog3 "github.com/OAzizjon/justlog3/go"

logger, err := justlog3.NewLogger("app.log")
if err != nil {
	log.Fatal(err)
}
defer justlog3.Shutdown() // flushes files and sends what is left to the cloud

logger.Log("Hi...")
logger.Log("debug", justlog3.OffConsole())
logger.Log("There is some error!", justlog3.Red())
logger.Log("OK", justlog3.Green())
logger.Log("There is critical error!", justlog3.Red(), justlog3.Prefix("CRITICAL!!"))
```

The console shows colored lines; `app.log` gets:

```
GREY:2026-10-05 14:03:12.517 - Hi...
GREY:2026-10-05 14:03:12.517 - debug
RED:2026-10-05 14:03:12.518 - There is some error!
GREEN:2026-10-05 14:03:12.518 - OK
CRITICAL!!:2026-10-05 14:03:12.518 - There is critical error!
```

Timestamps are local time with milliseconds. With `WithTime(false)` a line is just
`PREFIX: message`.

## Levels

```go
logger, _ := justlog3.NewBasicLogger("app.log", justlog3.WithConsoleLevel("INFO"))
logger.Debug("only in the file and cloud")
logger.Info("started")
logger.Successf("processed %d items", 42)
logger.Warning("disk 85% full")
logger.Error("payment failed")
logger.Critical("database is down")
```

`WithConsoleLevel` only filters the console; every line still goes to the file and the cloud.
Levels from lowest: `NOTSET`, `DEBUG`, `INFO`, `WARNING`, `ERROR` / `SUCCESS`, `CRITICAL`.
Every level has an `f` variant (`Infof`, `Errorf`, ...) that formats with `fmt.Sprintf`.

## How lines reach the disk

- Lines are buffered and every `WithCycles(n)` lines (default 50) handed to a background
  goroutine that writes them to the file, so `Log` never waits for the disk. If the disk can't
  keep up, `Log` writes the buffer itself instead of letting memory grow without limit.
- The file stays open and is reopened if it is renamed or deleted, so log rotation works, on
  Windows too.
- Go has no atexit hook, so **always call `justlog3.Shutdown()`** (or `logger.Flush()` /
  `logger.Close()`) before exit, otherwise the last buffered lines are lost.
- Loggers are safe for concurrent use.

## JustLog3 Cloud

```go
if err := justlog3.SetAPIToken(os.Getenv("JUSTLOG3_TOKEN")); err != nil {
	log.Fatal(err)
}
```

Call it once, before or after creating loggers. Every logger keeps a separate cloud buffer
(up to 10,000 lines; the oldest lines are dropped on overflow). A background goroutine sends it
as `text/plain` batches of at most `WithMaxBatch(n)` lines (default 100) and 512 KB of text when
`WithCloudCycles(n)` lines are collected (default 75) or every flush interval (default 5 s).
Bodies from 1 KB are sent gzip-compressed.

- **HTTP 401 / 403:** cloud sending stops; logs keep being written to files. Call
  `SetAPIToken` again with a valid token to resume.
- **No answer (network error, timeout) or HTTP 5xx:** the batch stays in the buffer and is
  resent unchanged with the same `X-JustLog-Seq`, so the server stores it once even if the first
  attempt did arrive. Until then the sender retries only on the flush interval, never once per
  log line.
- **HTTP 429** (rate or daily limit): lines stay buffered and nothing is sent until
  `Retry-After`.
- **HTTP 413:** later batches carry half as many lines.
- **Other HTTP errors:** the batch is dropped with a single warning.

Cloud options: `WithURL(url)` (a self-hosted server), `WithFlushInterval(d)`, `WithTimeout(d)`,
`WithMaxBatch(n)`.

Every request carries `Authorization: Bearer <token>`, `X-JustLog-Session` (random per
process), `X-JustLog-Seq`, `X-JustLog-Logger` (the file name) and `X-JustLog-UTC-Offset`
(seconds east of UTC).

## Logger options

| Option | Default | |
|---|---|---|
| `WithTime(bool)` | `true` | timestamp in every line |
| `WithFilemode("a" \| "w")` | `"a"` | `"w"` truncates the file once on creation |
| `WithCycles(n)` | `50` | lines per disk write |
| `WithCloudCycles(n)` | `75` | waiting lines that wake the cloud sender |
| `WithConsoleLevel(level)` | `"DEBUG"` | `BasicLogger` only |

Per call: `Red()`, `Green()`, `Prefix(p)`, `OffConsole()`. `logger.Status()` returns the
settings, `logger.StatusText()` the same as text.

## Benchmarks

justlog3 1.1.0 against `log/slog`, zap and zerolog, every logger writing the same message with a
timestamp to a file. `go test -bench . -benchtime 2s -count 5`, medians via `benchstat`; the
final flush is timed.

| Logger | ns/op | allocs/op |
|---|---|---|
| **justlog3** | **60** | **0** |
| zerolog, buffered | 292 | 0 |
| zap, buffered | 559 | 2 |
| slog, buffered | 997 | 0 |
| zerolog, default | 3,265 | 0 |
| zap, default | 4,118 | 2 |
| slog, default | 4,952 | 0 |

22 goroutines: justlog3 **141 ns**, zerolog 436, zap 576, slog 724 (all buffered).

Before quoting these numbers:

- justlog3 writes to disk from a background goroutine, so it uses a second core; the others
  write on the calling goroutine.
- The formats differ: zap and zerolog build structured records, justlog3 formats one plain line.
- "Default" means each call writes straight to the `*os.File`, which is how these libraries
  are usually set up. Most of that time is the write system call.

Other scenarios from the package's own `bench_test.go`: no timestamp ~26 ns, file + console
(stdout to NUL) ~390 ns, file + cloud ~185 ns. Console output costs a system call per line; a
real terminal is slower still.

Intel Core Ultra 7 155H, Windows 11, Go 1.27.1, measured 2026-10-05. The laptop's run-to-run
noise is large (up to ±50%). Setup and details: [BENCHMARKS.md](BENCHMARKS.md).

## License

[MIT](LICENSE)
