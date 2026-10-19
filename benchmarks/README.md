# Benchmarks

justlog3 for Python, Go and Node.js against the loggers people usually pick, each writing the
same message with a timestamp to a file. justlog3 is benchmarked from the source in this
repository, not from a published release. The competitors are installed as dependencies of these
benchmark folders only; they never become dependencies of the justlog3 packages.

Measured on 2026-10-19 on one laptop: Intel Core Ultra 7 155H (22 threads), 16 GB RAM, SSD,
Windows 11. Raw output of these runs: [results/2026-10-19/](results/2026-10-19/).

Lower is better in every table.

## Python: microseconds per call

Python 3.14.6, loguru 0.7.3. 50,000 calls per run, 7 runs, the libraries alternate within each
round. Median, with the min–max range in brackets.

| Scenario | justlog3 | logging | loguru |
|---|---|---|---|
| File only | **5.4** (5.3–5.6) | 16.2 (16.0–16.5) | 17.2 (17.0–17.5) |
| File + console | **7.1** (6.7–16.1) | 22.9 (21.6–47.5) | 23.3 (22.2–47.0) |
| File only, 8 threads | **6.9** (6.7–10.9) | 26.2 (24.6–32.2) | 26.5 (25.1–29.1) |
| File + cloud (local server) | 7.3 (6.5–7.5) | – | – |

justlog3 is 3.0× faster than `logging` writing to a file and 3.8× faster with 8 threads.
Sending to the cloud adds about 1.9 µs per call.

```sh
cd benchmarks/python
python -m pip install -r requirements.txt
python bench.py            # --n 50000 --runs 7 --only justlog3
```

## Go: nanoseconds per call

Go 1.27.1, zap 1.28.0, zerolog 1.35.1. `go test -bench . -benchtime 2s -count 6`, median of the
6 runs.

| Logger | ns/op | allocs/op |
|---|---|---|
| **justlog3** | **43** | **0** |
| zerolog, buffered | 181 | 0 |
| zap, buffered | 322 | 2 |
| slog, buffered | 648 | 0 |
| zerolog, default | 2,176 | 0 |
| zap, default | 2,396 | 2 |
| slog, default | 3,008 | 0 |

With 22 goroutines: justlog3 **88 ns/op**, zerolog 302, zap 405, slog 507 (all buffered).

"Default" writes straight to the `*os.File` on every call, the way these libraries are usually
set up. "Buffered" adds a 64 KB `bufio.Writer` (zap: `BufferedWriteSyncer`), wrapped in a mutex
for the parallel runs. justlog3 shows a few bytes per op (15–27 B) with zero allocations: that is
its buffer growing, spread over millions of calls.

```sh
cd benchmarks/go
go test -run '^$' -bench . -benchtime 2s -count 6 ./...
```

## Node.js: microseconds per call

Node 24.19.0, pino 9.6.0, winston 3.17.0. 50,000 calls per run, 7 runs, a fresh process for every
run, the libraries alternate within each round. Median, with the min–max range in brackets.

| Scenario | justlog3 | pino (async) | pino (sync) | winston | `fs.appendFileSync` |
|---|---|---|---|---|---|
| File | **4.2** (3.3–5.0) | 6.0 (5.5–6.7) | 5.7 (5.1–6.1) | 5.3 (5.1–5.7) | 139 (124–189) |
| File + console | **6.2** (5.5–7.0) | 7.0 (6.9–8.4) | 6.6 (5.8–7.8) | 6.4 (6.1–7.1) | 145 (136–198) |

In Node.js the gap is small: justlog3 is about 1.3× faster than winston and 1.4× faster than pino
writing to a file, and 3–13% faster with console output on. `fs.appendFileSync`, which
opens and closes the file on every line, is the naive baseline.

```sh
cd js && npm ci && npm run build && cd ../benchmarks/node
npm ci
node bench.mjs > results.md
```

## Read before quoting

- **Timed to the end.** The final flush or close is inside the timed part and `fsync` is outside
  it. The scripts check after every run that all lines reached the file, so buffering can't hide
  unwritten lines. The Go table above predates two fixes to its script (the line check, and no
  fsync when buffered zap stops); a re-run with both kept the same order, see
  [results/2026-10-19/README.md](results/2026-10-19/README.md).
- **Formats differ.** justlog3 writes one plain line (`INFO:2026-10-19 10:12:01.517 - message`).
  zap, zerolog and pino build structured records; slog writes key=value. Python's three libraries
  use formats as close as they allow.
- **Batching.** In Python and Node justlog3 writes 50 lines per disk write, on the calling
  thread. In Go every 50 lines wake a background goroutine that writes whatever has piled up by
  then in one call, so batches vary in size and the write can use a second core; on a
  single-core machine the gap would be smaller.
- **"Console" means the null device.** stdout goes to `NUL` (`/dev/null` elsewhere). A real
  terminal is much slower for every library and would hide the difference.
- **No `fsync`.** These are page-cache writes, not durability guarantees.
- **Power matters.** A verified Go re-run on battery came out 3–4× slower single-threaded for
  every logger. With 22 goroutines justlog3 slowed 3.4× and the others about 2×, so that gap
  narrowed from 3.4× to about 2×: see
  [results/2026-10-19/README.md](results/2026-10-19/README.md).
- **One laptop.** Absolute numbers move with power mode and background load. On 2026-10-05 the
  same laptop gave higher numbers for every library (Python file only: justlog3 9.8 µs, `logging`
  27.9 µs), while the ratios stayed close (2.8× then, 3.0× now). Compare libraries within one run,
  not across machines or days.
