# Benchmarks

justlog3 compared with `log/slog` (standard library), zap and zerolog.
Measured on 2026-10-19. The module and raw output are in [../benchmarks/](../benchmarks/).

## Setup

- Intel Core Ultra 7 155H (22 threads), 16 GB RAM, SSD, Windows 11; Go 1.27.1.
- Every logger writes the same message with a timestamp to a file:
  `user 42 logged in from 10.0.0.1, request id 7f3a9c, took 12ms`.
- Output formats differ:

  | Logger | Format |
  |---|---|
  | justlog3 | plain line |
  | slog | `TextHandler` (key=value) |
  | zap | console encoder of the production config |
  | zerolog | JSON with `Timestamp()` |

- **Default:** each call writes straight to the `*os.File`, which is how these libraries
  are usually set up.
- **Buffered:** a 64 KB `bufio.Writer` is added (zap: `BufferedWriteSyncer`). For the parallel
  runs it is wrapped in a mutex, because `bufio.Writer` is not safe for concurrent use.
- The final flush is inside the timed part. `go test -bench . -benchtime 2s -count 6`, median
  of the 6 runs.

## Results, time per call (lower is better)

| Logger | ns/op | allocs/op |
|---|---|---|
| **justlog3** | **43** | **0** |
| zerolog, buffered | 181 | 0 |
| zap, buffered | 322 | 2 |
| slog, buffered | 648 | 0 |
| zerolog, default | 2,176 | 0 |
| zap, default | 2,396 | 2 |
| slog, default | 3,008 | 0 |

Parallel, 22 goroutines:

| Logger | ns/op |
|---|---|
| **justlog3** | **88** |
| zerolog, buffered | 302 |
| zap, buffered | 405 |
| slog, buffered | 507 |

On 2026-10-05 the same laptop gave higher numbers for every logger (justlog3 60 ns, zerolog 292,
zap 559, slog 997); the order and the ratios stayed close. justlog3 shows 15–27 bytes per op
with zero allocations: its buffer growing, spread over millions of calls.

### Read before quoting

- **Background writer.** justlog3 hands its buffer to a background goroutine for the disk write,
  so it uses a second core. The competitors write on the calling goroutine. The final `Flush`
  is timed, so no line is left unwritten, but on a single-core machine the gap would be smaller.
- **Unequal formats.** The libraries do different amounts of work per line. zerolog and zap build
  structured records; justlog3 formats one plain line.
- **The default rows** show what an unbuffered file costs on Windows. Most of their time is the
  write system call, not the logger itself.

## Cloud

`BenchmarkFileAndCloud` (logging to a file and to a local server) costs about 185 ns per call in
1.1.0. The run logs millions of lines a second, so the 10,000-line cloud buffer overflows on
purpose. The extra bytes per call are that buffer's churn.

**In 1.0.0 the same benchmark crashed:** a slice-bounds panic in `Log`, followed by a deadlock.
The panic happened whenever the disk writer fell behind with cloud sending on. It is fixed in
1.1.0 (see CHANGELOG).

## 1.1.0 compared with 1.0.0

The package's own benchmarks (`bench_test.go`) were run 6 times for each version, alternating.
`benchstat` finds no significant difference in any of them (p > 0.05 everywhere, run-to-run noise
up to ±50% on this laptop). Allocations stay at 0 per call.

## Reproducing

```sh
go test -run '^$' -bench . -benchtime 2s -count 6 ./...
```

The comparison with slog, zap and zerolog lives in a separate module,
[../benchmarks/go](../benchmarks/go), so that this package stays free of dependencies. Each
benchmark logs `b.N` lines to a fresh file and flushes at the end.

```sh
cd benchmarks/go
go test -run '^$' -bench . -benchtime 2s -count 6 ./...
```
