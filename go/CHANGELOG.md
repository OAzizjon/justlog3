# Changelog

All changes in this project are documented in this file.

## [1.1.0] - 2026-10-04

### Changed
- The default endpoint is now JustLog3 Cloud at `https://jl3-cloud.site/api/logs/`
  (it pointed at a local development server).
- Batches are capped at 512 KB of text as well as `WithMaxBatch` lines, and bodies from 1 KB are
  gzip-compressed, so a backlog after an outage no longer hits the server's size limit (HTTP 413).
- HTTP 5xx keeps the batch and resends it with the same `X-JustLog-Seq`, like a network error.
- HTTP 429 keeps the lines buffered and pauses sending until `Retry-After` instead of dropping them.
- HTTP 413 halves the number of lines per batch instead of dropping the batch.
- A `X-JustLog-Lines-Rejected` answer (daily limit reached mid-batch) prints one warning.

### Fixed
- `Log` panicked with "slice bounds out of range" (and the next `Shutdown` deadlocked) when the
  disk writer fell 1 MB behind while cloud sending was on: the line was copied to the cloud buffer
  after the disk buffer had already been swapped out. Heavy logging with `SetAPIToken`, e.g.
  `BenchmarkFileAndCloud`, triggered it. The line is now copied first.

## [1.0.0] - 2026-10-04

### Added
- First public release of the Go port of `justlog3`: `Logger`, `BasicLogger`, buffered file
  writing and JustLog3 Cloud sending (`SetAPIToken`), with no dependencies outside the standard library.
