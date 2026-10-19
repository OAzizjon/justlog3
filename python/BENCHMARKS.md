# Benchmarks

justlog3 1.1.2 compared with the standard `logging` module and loguru 0.7.3.
Measured on 2026-10-19. The script and raw output are in [../benchmarks/](../benchmarks/).

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
| File only | **5.4** (5.3–5.6) | 16.2 (16.0–16.5) | 17.2 (17.0–17.5) |
| File + console | **7.1** (6.7–16.1) | 22.9 (21.6–47.5) | 23.3 (22.2–47.0) |
| File only, 8 threads | **6.9** (6.7–10.9) | 26.2 (24.6–32.2) | 26.5 (25.1–29.1) |
| File + cloud | 7.3 (6.5–7.5) | – | – |

- **File only:** justlog3 is about 3x faster. `logging` and loguru hand every record to the
  file object, while justlog3 writes 50 lines at a time.
- **8 threads:** the gap grows to about 3.8x.
- **Cloud:** sending to the cloud adds about 1.9 µs per call. Lines go to the server from a
  background thread, compressed, in batches.
- **Compared with 2026-10-05:** the same laptop then gave higher numbers for every library
  (file only: justlog3 9.8 µs, `logging` 27.9 µs, loguru 29.6 µs). The ratios stayed close
  (2.8x then, 3.0x now), so compare libraries within one run, not across days.

## 1.1.0 compared with 1.0.0

The two versions were measured in alternating processes. The difference stays within the noise
(about ±10%) in every scenario, including the cloud one, so the 1.1.0 sending changes (batching,
gzip, retries) don't slow down `log()`.

## Where the time goes

A cProfile of 100,000 file-only calls (1.1.0, 2026-10-05):

- **57%** of the time is spent opening and closing the log file. Every disk write
  (every 50 lines) reopens it.
- **11%** is the timestamp (`datetime.now().isoformat()`).
- The remaining time is the `log()` call itself.

The Go version keeps the file open and follows log rotation instead. Doing the same in Python
would be the biggest single speed-up, roughly 2x for file-only logging.

## Reproducing

The script is [../benchmarks/python/bench.py](../benchmarks/python/bench.py), outside the package
so that loguru is not a dependency. Each scenario times `log()` / `info()` in a loop on a fresh
temporary file and includes the final flush.

```sh
cd benchmarks/python
python -m pip install -r requirements.txt
python bench.py
```

