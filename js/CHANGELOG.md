# Changelog

All changes in this project are documented in this file.

## [1.0.0] - 2026-10-18

First release of the JavaScript/TypeScript client, with the line format and cloud protocol of
the Python and Go libraries.

### Added
- `Logger` and `BasicLogger` (`debug` … `critical`, `consoleLevel` filters the console only).
- File writes in batches of `cycles` lines (50 by default), `flush()`, and a flush on exit.
  A failed disk write prints the error and the unwritten lines instead of throwing.
- `log(error)` writes the stack trace; other objects are written on one line via
  `util.inspect`. Console colours only on a TTY.
- `setApiToken()`: batches every 5 s or at 75 waiting lines, up to 5,000 lines and 512 KB per
  request, gzip from 1 KB, up to 10,000 unsent lines per logger.
- Retries with the same sequence number after no answer or HTTP 5xx; `Retry-After` on 429;
  401/403 and redirects stop cloud sending with a warning; 413 halves the batch.
- A `beforeExit` hook sends what is waiting on a normal exit; `shutdown()` for signal handlers
  and `process.exit()`. Replacing the token lets the old sender finish first.
- ES modules, CommonJS and TypeScript types; Node.js 18+, no runtime dependencies.
