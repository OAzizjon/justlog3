# Raw output, 2026-10-19

One laptop: Intel Core Ultra 7 155H, 16 GB RAM, SSD, Windows 11.

| File | What it is |
|---|---|
| `python.md` | `python bench.py` |
| `node.md` | `node bench.mjs` |
| `go.txt` | `go test -bench . -benchtime 2s -count 6`. The tables in [../../README.md](../../README.md) come from this run. |
| `go-verified-battery.txt` | The same command after two fixes to `bench_test.go`, run later the same day on battery |

The two fixes: every run now checks, after the timer, that the file holds exactly `b.N` complete
lines and that the first and last of them carry the message and a timestamp; and buffered zap no longer calls `File.Sync` (an
fsync) when it stops. The timed part is otherwise the same: the old run did one fsync per
benchmark for buffered zap, spread over millions of calls.

The first three runs didn't record the power state. Single-threaded, the battery run is 3–4×
slower for every logger (justlog3 129 ns/op, zerolog 613, zap 1,104, slog 2,344 buffered), and the
order stays the same. With 22 goroutines justlog3 slowed 3.4× and the others about 2×: justlog3
299 ns/op against zerolog 632, zap 829, slog 1,054, a gap of about 2× instead of 3.4×.
