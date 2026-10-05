# Changelog

All changes in this project are documented in this file.

## [1.1.0] - 2026-10-04

### Changed
- The default endpoint is now JustLog3 Cloud at `https://jl3-cloud.site/api/logs/`
  (it pointed at a local development server).
- Cloud sending splits a backlog into requests of at most 512 KB / 5,000 lines and
  gzip-compresses bodies from 1 KB, so a buffer that grew during an outage is no
  longer refused as too large (HTTP 413) and lost.
- A batch that got no answer is resent with the same `X-JustLog-Seq`; the server
  stores it once even if the first attempt arrived.
- HTTP 429 keeps the lines buffered and pauses sending for `Retry-After`; 5xx and
  network errors keep the batch and retry after 5 s, 10 s, ... up to 5 minutes.
- HTTP 413 halves the batch size instead of dropping the batch.
- Lines longer than 16 KB are cut before sending (the server keeps 8 KB).

### Fixed
- `httpx` is now a declared dependency (`pip install justlog3` failed on import
  without it); Python 3.8+ is required, as httpx needs it.
- The network error warning printed a placeholder ("...") instead of a message.

## [1.0.0] - 2026-08-15

### Added
- First public release of the `justlog3` library.