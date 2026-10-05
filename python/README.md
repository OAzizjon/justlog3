# JustLog3

[![PyPI](https://img.shields.io/pypi/v/justlog3.svg)](https://pypi.org/project/justlog3/)
[![Python](https://img.shields.io/pypi/pyversions/justlog3.svg)](https://pypi.org/project/justlog3/)
[![License: MIT](https://img.shields.io/badge/License-MIT-green.svg)](LICENSE)

**The cheetah of logging.** Minimalistic, fast, zero-config.

```sh
pip install justlog3
```

## Introduction

Your logger should not be the slowest line in your program. And yet `logging` hands every single
record to the file object, and loguru does the same with nicer colors. JustLog3 doesn't: it
collects lines and writes them fifty at a time. A call costs **9.8 µs** where `logging` needs
27.9 µs and loguru 29.6 µs. Put eight threads on it and the gap grows to **four times**.

No handlers. No formatters. No `dictConfig` copied from a five-year-old answer. One line creates a
logger, one line logs. One more line and every log line also lands in
[JustLog3 Cloud](#justlog3-cloud): search, sessions, Telegram alerts, an AI that reads your
stack traces.

Need structured JSON, twelve sinks and a plugin system? Those libraries exist, and they are
good. JustLog3 is for when you just want to log.

## Quick start

```python
from justlog3 import Logger

logger = Logger('app.log')

logger.log('Hi...')
logger.log('debug', off_console=True)
logger.log('There is some error!', red=True)
logger.log('OK', green=True)
logger.log('There is critical error!', red=True, custom_prefix='CRITICAL!!')
```

The console shows colored lines; `app.log` gets:

```
GREY:2026-10-05 14:03:12.517 - Hi...
GREY:2026-10-05 14:03:12.517 - debug
RED:2026-10-05 14:03:12.518 - There is some error!
GREEN:2026-10-05 14:03:12.518 - OK
CRITICAL!!:2026-10-05 14:03:12.518 - There is critical error!
```

Timestamps are local time with milliseconds. With `with_time=False` a line is just
`PREFIX: message`.

## Levels

```python
from justlog3 import BasicLogger

logger = BasicLogger('app.log', console_level='INFO')
logger.debug('cache warmed')         # file and cloud only: below INFO
logger.info('server started')        # green
logger.success('migration done')     # green
logger.warning('disk 85% full')      # red
logger.error('payment failed')       # red
logger.critical('database is down')  # red
```

`console_level` only filters the console. Every line still goes to the file and the cloud.
Levels from lowest: `NOTSET`, `DEBUG`, `INFO`, `WARNING`, `ERROR` / `SUCCESS`, `CRITICAL`.

## How lines reach the disk

- `log()` appends the line to a buffer. Every `cycles` lines (default 50) the buffer is
  written to the file in one call. `logger.flush()` writes it immediately.
- **Normal exit:** every logger is flushed by an `atexit` hook.
- **SIGTERM** (`kill`, `docker stop`, systemd): JustLog3 installs a handler that turns the
  signal into a normal exit, so the `atexit` flush runs. On Linux and macOS the signal is also
  held back while a write is in progress, so a write is never cut in half. Pass
  `off_sigmask=True` to a logger to skip that, or call `off_signal_handler()` right after the
  import, before creating any logger, to keep your own SIGTERM handler.
- **`kill -9` or a power cut** loses what is still in the buffer: at most `cycles - 1` lines.
  Lower `cycles` if that matters more than speed.
- If the disk write fails (disk full, file locked), JustLog3 prints the error and the unwritten
  lines to the console instead of raising.
- Loggers are thread-safe.

## JustLog3 Cloud

```python
import os
from justlog3 import Logger, set_api_token

set_api_token(os.environ['JUSTLOG3_TOKEN'])  # the token from your dashboard
logger = Logger('app.log')
logger.log('This line goes to the file and to the cloud')
```

Call `set_api_token()` once, before or after creating loggers. A background thread sends the
lines; `log()` never waits for the network.

- Lines are sent every `flush_interval` seconds (default 5), or sooner once `cloud_cycles`
  lines (default 75) are waiting.
- A request carries at most 5,000 lines and 512 KB of text. A backlog after an outage goes out
  in several requests. Bodies from 1 KB are gzip-compressed.
- Every logger keeps up to 10,000 unsent lines. Beyond that the oldest lines are dropped, with a
  single warning.
- **No answer or HTTP 5xx:** the batch stays buffered and is resent with the same sequence
  number, so the server stores it once even if the first attempt did arrive. The wait between
  attempts starts at 5 s and doubles up to 5 minutes.
- **HTTP 429** (rate or daily limit): nothing is sent until `Retry-After`; lines stay buffered.
- **HTTP 401 / 403:** cloud sending stops and files keep being written. Call
  `set_api_token()` again with a valid token.
- **HTTP 413:** later batches carry half as many lines.
- **Exit:** the remaining lines are sent before the process ends. This waits a few seconds at
  most, bounded by `timeout`.

`set_api_token(token, *, url=..., flush_interval=5.0, timeout=5.0)`: `url` points to a
self-hosted server, `timeout` is the per-request timeout in seconds.

Every request carries `Authorization: Bearer <token>`, `X-JustLog-Session` (random per
process), `X-JustLog-Seq`, `X-JustLog-Logger` (the file name) and `X-JustLog-UTC-Offset`
(seconds east of UTC).

## API

### `Logger(filename='app.log', *, with_time=True, filemode='a', cycles=50, off_sigmask=False, cloud_cycles=75)`

| Parameter | Default | |
|---|---|---|
| `filename` | `'app.log'` | `.log` is added if missing |
| `with_time` | `True` | timestamp in every line |
| `filemode` | `'a'` | `'w'` empties the file once, on creation |
| `cycles` | `50` | lines per disk write |
| `off_sigmask` | `False` | don't hold back SIGTERM during writes (Linux, macOS) |
| `cloud_cycles` | `75` | waiting lines that wake the cloud sender |

**`logger.log(message='[no text]', *, red=False, green=False, custom_prefix=None, off_console=False)`**
writes one line. The prefix is `RED`, `GREEN` or `GREY` unless `custom_prefix` is given;
`red` wins over `green`. `off_console=True` keeps the line out of the console only.

**`logger.flush()`** writes the buffer now. **`logger.status()`** returns the settings as a
dict, **`logger.status_text()`** as text.

### `BasicLogger(..., console_level='DEBUG')`

Same parameters as `Logger`, plus `console_level`. Methods: `debug`, `info`, `success`,
`warning`, `error`, `critical`.

### Functions

- `set_api_token(token, *, url, flush_interval=5.0, timeout=5.0)` turns on cloud sending.
  Raises `ValueError` for an empty token.
- `off_signal_handler()` keeps JustLog3 from installing its SIGTERM handler. Call it before
  creating the first logger.

## Benchmarks

justlog3 1.1.0 against the standard `logging` module and loguru 0.7.3, writing the same message
with a timestamp to a file. Median of 7 runs × 50,000 calls; the final flush is timed.

| Scenario | justlog3 | logging | loguru |
|---|---|---|---|
| File only | **9.8 µs** | 27.9 µs | 29.6 µs |
| File + console | **12.5 µs** | 35.3 µs | 37.8 µs |
| File only, 8 threads | **11.8 µs** | 49.4 µs | 50.4 µs |
| File + cloud | 13.5 µs | – | – |

Intel Core Ultra 7 155H, Windows 11, Python 3.14.6, measured 2026-10-05. The console was
redirected to the null device; a real terminal is slower for every library. Setup, ranges and
caveats: [BENCHMARKS.md](BENCHMARKS.md).

## Requirements

Python 3.8 or newer. `httpx` (for cloud sending) is installed with the package.

## License

[MIT](LICENSE)
