# Node.js logging benchmark

Results from 2026-10-19 are in [../README.md](../README.md#nodejs-microseconds-per-call).

Requires Node.js 18 or newer. Build the local package from the repository root:

```sh
cd js
npm ci
npm run build
cd ../benchmarks/node
```

Install the independent benchmark dependencies and run:

```sh
npm ci && node bench.mjs
```

On Windows PowerShell with script execution disabled, use `npm.cmd`.

The script imports `../../js/dist/index.js`, never an installed justlog3 release.
Pino and winston dependencies belong only to this private benchmark package.
The output header records Node, OS, CPU model and total RAM. Progress goes to
stderr; stdout contains the header and Markdown results table, so it can be saved
with `node bench.mjs > results.md`.

Every case makes **50,000 calls**, repeated **7 times**, with all libraries
interleaved in each round and the starting case rotated. Each sample uses a fresh
child process and a fresh temporary file. No warm-up sample is discarded; results
include JIT warm-up within the logging loop. Import, logger construction and file
opening (where asynchronous) are outside the timer. Logging and final flushing,
closing and console draining are inside it. The naive baseline opens and closes
the file on every `fs.appendFileSync` call. Files are removed after verification.

Every record contains the message
`user 42 logged in from 10.0.0.1, request id 7f3a9c, took 12ms`
and a newly generated timestamp. Justlog3 uses its default 50-line buffer and
`Logger.log` with `offConsole: true` for file-only output, followed by `flush()`
and `shutdown()`. Pino uses `pino.destination` with both `sync: false` and
`sync: true`, then `flush`/`flushSync` and destination close. Winston uses a File
transport, timestamp formatting, logger finish and file-stream close. Its File
transport receives an owned `fs.WriteStream` so the timer can await the actual
file descriptor closing. This stream option is supported by the pinned winston
3 version but deprecated for winston 4; a setup warning may appear on stderr.

The file + console cases enable justlog3 console output, pino multistream to file
and stdout, winston's Console transport, or `console.log` for the naive baseline.
Child stdout is an actual descriptor for `os.devNull` (`NUL` on Windows,
`/dev/null` on Unix); output is not monkey-patched or piped through the parent.
This measures console formatting and writes to a null device, not terminal
rendering. Pino retains its native JSON record format; justlog3 retains its prefix
and local timestamp. The payload is identical, but serialized byte counts differ.

After every run, outside the timer, the script checks exactly 50,000
newline-terminated records and checks every record for the message and timestamp.
Missing lines, write errors, worker failures and timeouts abort with a nonzero
exit status. The table reports median and min–max microseconds per call across
the seven samples. These are local filesystem/page-cache measurements, without
`fsync`; machine load, storage and Node version affect the results.
