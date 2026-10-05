# Benchmarks

justlog3 1.1.0 compared with the standard `logging` module and loguru 0.7.3.
Measured on 2026-10-05.

## Setup

- Intel Core Ultra 7 155H (22 threads), 16 GB RAM, SSD, Windows 11; Python 3.14.6.
- Every library writes the same message with a timestamp to a log file:
  `user 42 logged in from 10.0.0.1, request id 7f3a9c, took 12ms`.
  Formats are as close as the libraries allow (`LEVEL:YYYY-MM-DD HH:MM:SS.mmm - message`).
- Libraries use their defaults: justlog3 `Logger(path)` (50 lines per disk write),
  `logging.FileHandler`, `loguru.logger.add(path)`.
- 50,000 calls per run, 7 runs. The libraries alternate within each round, so they share the same
  machine noise. Results are the median, with the min–max range in brackets.
- The final flush or close is inside the timed part, so buffering can't hide unwritten lines. Every
  run checks that all 50,000 lines reached the file.
- "Console" means stdout redirected to the null device. A real terminal is much slower and would
  hide the difference between the libraries.
- "Cloud" runs a fake JustLog3 server in a separate process on the same machine.

## Results, microseconds per call (lower is better)

| Scenario | justlog3 | logging | loguru |
|---|---|---|---|
| File only | **9.8** (9.0–11.3) | 27.9 (23.0–31.8) | 29.6 (23.7–32.2) |
| File + console | **12.5** (11.0–13.5) | 35.3 (32.5–38.2) | 37.8 (36.0–41.2) |
| File only, 8 threads | **11.8** (11.4–12.9) | 49.4 (47.4–49.9) | 50.4 (48.8–51.4) |
| File + cloud | 13.5 (12.5–14.0) | – | – |

- **File only:** justlog3 is about 2.8x faster. `logging` and loguru hand every record to the
  file object, while justlog3 writes 50 lines at a time.
- **8 threads:** the gap grows to about 4x.
- **Cloud:** sending to the cloud adds about 3.7 µs per call. Lines go to the server from a
  background thread, compressed, in batches.

## 1.1.0 compared with 1.0.0

The two versions were measured in alternating processes. The difference stays within the noise
(about ±10%) in every scenario, including the cloud one, so the 1.1.0 sending changes (batching,
gzip, retries) don't slow down `log()`.

## Where the time goes

A cProfile of 100,000 file-only calls:

- **57%** of the time is spent opening and closing the log file. Every disk write
  (every 50 lines) reopens it.
- **11%** is the timestamp (`datetime.now().isoformat()`).
- The remaining time is the `log()` call itself.

The Go version keeps the file open and follows log rotation instead. Doing the same in Python
would be the biggest single speed-up, roughly 2x for file-only logging.

## Reproducing

The benchmark script lives outside the package so that loguru is not a dependency. Each scenario
times `log()` / `info()` in a loop on a fresh temporary file and includes the final flush.

