# JustLog3

[![CI](https://github.com/OAzizjon/justlog3/actions/workflows/ci.yml/badge.svg)](https://github.com/OAzizjon/justlog3/actions/workflows/ci.yml)
[![npm](https://img.shields.io/npm/v/justlog3.svg)](https://www.npmjs.com/package/justlog3)
[![License: MIT](https://img.shields.io/badge/License-MIT-green.svg)](LICENSE)

**The cheetah of logging.** Minimalistic, fast, zero-config.

```sh
npm install justlog3
```

## Introduction

Your logger shouldn't be the slowest line in your program. And yet most loggers hand every
single record to the file system, one at a time. JustLog3 doesn't: it collects lines and writes
them fifty at a time. A call appends to a buffer and returns.

No handlers. No formatters. No config object copied from a five-year-old answer. One line creates
a logger, one line logs. One more line and every log line also lands in
[JustLog3 Cloud](#justlog3-cloud): search, sessions, Telegram alerts, an AI that reads your
stack traces. TypeScript types are included.

Need structured JSON, twelve sinks and a plugin system? Those libraries exist, and they are
good. JustLog3 is for when you just want to log.

## Quick start

```typescript
import { Logger } from 'justlog3';

const logger = new Logger('app.log');

logger.log('Hi...');
logger.log('debug', { offConsole: true });
logger.log('There is some error!', { red: true });
logger.log('OK', { green: true });
logger.log('There is a critical error!', { red: true, customPrefix: 'CRITICAL!!' });
```

The console shows colored lines; `app.log` gets:

```
GREY:2026-10-05 14:03:12.517 - Hi...
GREY:2026-10-05 14:03:12.517 - debug
RED:2026-10-05 14:03:12.518 - There is some error!
GREEN:2026-10-05 14:03:12.518 - OK
CRITICAL!!:2026-10-05 14:03:12.518 - There is a critical error!
```

Timestamps are local time with milliseconds. With `withTime: false` a line is just
`PREFIX: message`.

Pass an `Error` and the line carries its stack trace; other objects are written on one line,
the way `util.inspect` shows them:

```typescript
try {
  await chargeCard(order);
} catch (error) {
  logger.log(error, { red: true });   // RED:... - Error: card declined\n    at chargeCard (...)
}
```

## Levels

```typescript
import { BasicLogger } from 'justlog3';

const logger = new BasicLogger('app.log', { consoleLevel: 'INFO' });
logger.debug('cache warmed');         // file and cloud only: below INFO
logger.info('server started');        // green
logger.success('migration done');     // green
logger.warning('disk 85% full');       // red
logger.error('payment failed');         // red
logger.critical('database is down');  // red
```

`consoleLevel` only filters the console. Every line still goes to the file and the cloud.
Levels from lowest: `NOTSET`, `DEBUG`, `INFO`, `WARNING`, `ERROR` / `SUCCESS`, `CRITICAL`.

## How lines reach the disk and the cloud

- `log()` appends the line to a buffer. Every `cycles` lines (default 50) the buffer is
  written to the file in one call. `logger.flush()` writes it immediately. With `file: false`
  nothing is written to disk; lines still reach the console and the cloud.
- **Normal exit:** a `beforeExit` hook flushes every logger and sends the lines still waiting
  for the cloud.
- **`kill -9` or a power cut** loses what is still in the buffer: at most `cycles - 1` lines.
  Lower `cycles` if that matters more than speed.
- If the disk write fails (disk full, file locked), JustLog3 prints the error and the unwritten
  lines to the console instead of raising.
- `log()` is synchronous, so any number of concurrent async tasks can share one logger.

## JustLog3 Cloud

```typescript
import { Logger, setApiToken } from 'justlog3';

setApiToken(process.env.JUSTLOG3_TOKEN);  // the token from your dashboard
const logger = new Logger('app.log');
logger.log('This line goes to the file and to the cloud');
```

Call `setApiToken()` once, before or after creating loggers. A background interval sends the
lines; `log()` never waits for the network. Calling it again with another token first lets the
old sender finish, so a batch already on its way is not stored twice.

- Lines are sent every `flushIntervalMs` milliseconds (default 5000), or sooner once
  `cloudCycles` lines (default 75) are waiting.
- A request carries at most `maxBatchLines` lines (default 5000) and 512 KB of text; a single
  longer line goes alone. A backlog after an outage goes out in several requests. Bodies from
  1 KB are gzip-compressed.
- Every logger keeps up to 10,000 unsent lines. Beyond that the oldest lines are dropped, with a
  single warning.
- **No answer or HTTP 5xx:** the batch stays buffered and is resent with the same sequence
  number, so the server stores it once even if the first attempt did arrive. The wait between
  attempts starts at `flushIntervalMs` (5 s) and doubles up to 5 minutes.
- **HTTP 429** (rate or daily limit): nothing is sent until `Retry-After`; lines stay buffered.
- **HTTP 401 / 403:** cloud sending stops and files keep being written. Call
  `setApiToken()` again with a valid token.
- **HTTP 413:** later batches carry half as many lines; a single line that is still too big is
  dropped with a warning.
- **Redirects (HTTP 3xx)** are not followed: cloud sending stops with a warning that names the
  new address. Set `url` to it.
- **Exit:** the remaining lines are sent before the process ends, unless a 429 asked to wait.
  This waits a few seconds at most, bounded by `timeoutMs`.

`setApiToken(token, { url, flushIntervalMs = 5000, timeoutMs = 5000, maxBatchLines = 5000 })`:
`url` points to a self-hosted server, `timeoutMs` is the per-request timeout in milliseconds.

Every request carries `Authorization: Bearer <token>`, `X-JustLog-Session` (random per
`setApiToken()` call), `X-JustLog-Seq`, `X-JustLog-Logger` (the file name) and `X-JustLog-UTC-Offset`
(seconds east of UTC).

## API

### `new Logger(filename = 'app.log', { withTime = true, filemode = 'a', cycles = 50, cloudCycles = 75, file = true })`

| Parameter | Default | |
|---|---|---|
| `filename` | `'app.log'` | `.log` is added if missing |
| `withTime` | `true` | timestamp in every line |
| `filemode` | `'a'` | `'a'` appends; `'w'` empties the file once, on creation |
| `cycles` | `50` | lines per disk write |
| `cloudCycles` | `75` | waiting lines that wake the cloud sender |
| `file` | `true` | write to disk; set `false` for serverless/edge |

**`logger.log(message = '[no text]', { red = false, green = false, customPrefix, offConsole = false })`**
writes one line. The prefix is `RED`, `GREEN` or `GREY` unless `customPrefix` is given;
`red` wins over `green`. `offConsole: true` keeps the line out of the console only.

**`logger.flush()`** writes the buffer now. **`logger.status()`** returns the settings as an
object.

### `new BasicLogger(filename = 'app.log', { consoleLevel = 'DEBUG', ... })`

Same parameters as `Logger`, plus `consoleLevel`. Methods: `debug`, `info`, `success`,
`warning`, `error`, `critical`.

### Functions

- `setApiToken(token, { url, flushIntervalMs = 5000, timeoutMs = 5000, maxBatchLines = 5000 })`
  turns on cloud sending. `url` defaults to JustLog3 Cloud. Throws for an empty token.
- `shutdown(): Promise<void>` flushes every logger, sends what is left to the cloud and ends
  cloud sending. A normal exit does not need it; call it in a `SIGTERM` handler or before
  `process.exit()`.

## Runtimes

- **Node.js** 18 or newer; CI tests 18, 20 and 22 on Linux and Windows.
- **Bun** and **Deno** (`npm:justlog3`) should run it through their Node.js compatibility
  layer, but CI does not test them yet. Deno needs `--allow-write` for files and
  `--allow-net` for the cloud.
- Set `file: false` for serverless or edge runtimes where disk access is limited or absent.

## Shutdown

No signal handlers are installed. In a container or under systemd, add your own `SIGTERM`
handler:

```typescript
process.on('SIGTERM', async () => {
  await shutdown();
  process.exit(0);
});
```

On a normal exit a `beforeExit` hook flushes the files and sends what is waiting; if your own
`beforeExit` listener logs more, those lines go out on the next `beforeExit`. `process.exit()`
does not emit `beforeExit`, so `await shutdown()` yourself before a manual exit. `shutdown()`
ends cloud sending for the process.

## Benchmarks

justlog3 against pino 9.6.0 and winston 3.17.0, every logger writing the same message with a
timestamp to a file. 7 runs × 50,000 calls, a fresh process per run; the final flush and close
are timed. Median microseconds per call, lower is better.

| Scenario | justlog3 | pino (async) | pino (sync) | winston |
|---|---|---|---|---|
| File | **4.2** | 6.0 | 5.7 | 5.3 |
| File + console | **6.2** | 7.0 | 6.6 | 6.4 |

Intel Core Ultra 7 155H, Windows 11, Node 24.19.0, measured 2026-10-19. The console was the null
device. pino writes structured JSON, justlog3 a plain line. Script, ranges and caveats:
[benchmarks/](https://github.com/OAzizjon/justlog3/tree/main/benchmarks).

## Requirements

Node.js 18 or newer. No runtime dependencies are required.

## License

[MIT](LICENSE)
