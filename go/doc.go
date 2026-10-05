// Package justlog3 is the Go port of JustLog3 — the cheetah of logging:
// minimalistic, fast and zero-config.
//
// A Logger prints colored lines to the console, writes them to a .log file
// in batches and, once SetAPIToken has been called, sends them to JustLog3 Cloud.
// It has no dependencies outside the standard library.
//
// # Quick start
//
//	logger, err := justlog3.NewLogger("app.log")
//	if err != nil {
//		log.Fatal(err)
//	}
//	defer justlog3.Shutdown()
//
//	logger.Log("Hi...")
//	logger.Log("debug", justlog3.OffConsole())
//	logger.Log("There is some error!", justlog3.Red())
//	logger.Log("OK", justlog3.Green())
//	logger.Log("There is critical error!", justlog3.Red(), justlog3.Prefix("CRITICAL!!"))
//
// Each line looks like "PREFIX:2026-01-02 15:04:05.000 - message", or
// "PREFIX: message" with WithTime(false).
//
// # Levels
//
// BasicLogger adds Debug, Info, Warning, Error, Success and Critical (and the
// f-variants such as Infof). WithConsoleLevel hides lower levels from the
// console only; every line is still written to the file and sent to the cloud.
//
// # Writing to disk
//
// Lines are buffered and, every WithCycles lines (default 50), handed to a
// background goroutine that writes them to the file, so Log does not wait for
// the disk. If the disk can't keep up, Log writes itself instead of letting the
// buffer grow. The file stays open and is reopened when it is renamed or
// deleted, so log rotation works, on Windows too.
//
// Go has no atexit hook: call Shutdown (or Flush / Close on each logger)
// before the process exits, otherwise the last buffered lines are lost.
//
// # JustLog3 Cloud
//
//	if err := justlog3.SetAPIToken(os.Getenv("JUSTLOG3_TOKEN")); err != nil {
//		log.Fatal(err)
//	}
//
// Every logger keeps its own cloud buffer of up to CloudBufMax lines (the
// oldest are dropped on overflow). A background goroutine sends it as
// text/plain batches of at most WithMaxBatch lines and 512 KB of text when
// WithCloudCycles lines are collected or every WithFlushInterval. Bodies from
// 1 KB are gzip-compressed (Content-Encoding: gzip).
//
//   - HTTP 401/403: the token is rejected and cloud sending stops; files keep
//     being written. Call SetAPIToken again to resume.
//   - No answer (network error, timeout) or HTTP 5xx: the batch stays in the
//     buffer and is resent unchanged with the same X-JustLog-Seq, so the server
//     stores it once. Until then only the flush interval retries, not every Log.
//   - HTTP 429 (rate or daily limit): the lines stay buffered and nothing is
//     sent until Retry-After has passed.
//   - HTTP 413: later batches carry half as many lines.
//   - Any other HTTP error: the batch is dropped with a single warning.
//
// Requests carry the headers Authorization (Bearer token), X-JustLog-Session,
// X-JustLog-Seq, X-JustLog-Logger and X-JustLog-UTC-Offset.
package justlog3
