package gobench

// justlog3 vs log/slog, zap and zerolog, writing one plain message with a
// timestamp to a file. "default" competitors write through to the file on
// every call; "buffered" ones get a bufio.Writer flushed at the end, which is
// what justlog3 does internally. The final flush is inside the timer; fsync is
// not, for any logger. After the timer stops, every run checks that the file
// holds exactly b.N complete lines carrying the message.
import (
	"bufio"
	"bytes"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"testing"

	justlog3 "github.com/OAzizjon/justlog3/go"
	"github.com/rs/zerolog"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

const msg = "user 42 logged in from 10.0.0.1, request id 7f3a9c, took 12ms"

// A date and time, or zap's production epoch seconds.
var timestamp = regexp.MustCompile(`\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}|\b1\d{9}(\.\d+)?\b|\b1\.\d+e\+09\b`)

func open(b *testing.B) (*os.File, string) {
	path := filepath.Join(b.TempDir(), "bench.log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { f.Close() })
	return f, path
}

func writer(b *testing.B, buffered bool) (io.Writer, func() error, string) {
	f, path := open(b)
	if !buffered {
		return f, func() error { return nil }, path
	}
	w := bufio.NewWriterSize(f, 64<<10)
	return w, w.Flush, path
}

// noSync keeps zap's BufferedWriteSyncer from calling File.Sync (an fsync)
// inside the timer when it stops: no other logger here syncs to disk.
type noSync struct{ io.Writer }

func (noSync) Sync() error { return nil }

func run(b *testing.B, parallel bool, log func(), done func() error, path string) {
	b.ReportAllocs()
	b.ResetTimer()
	if parallel {
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				log()
			}
		})
	} else {
		for i := 0; i < b.N; i++ {
			log()
		}
	}
	if err := done(); err != nil {
		b.Fatal(err)
	}
	b.StopTimer()
	verify(b, path)
}

// verify reads the file in chunks (a run can write gigabytes) and checks the
// line count, the trailing newline and the first and last lines.
func verify(b *testing.B, path string) {
	f, err := os.Open(path)
	if err != nil {
		b.Fatal(err)
	}
	defer f.Close()
	var lines int
	var first []byte
	var lastByte byte
	buf := make([]byte, 1<<20)
	tail := make([]byte, 0, 1024)
	for {
		n, err := f.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			if first == nil {
				if i := bytes.IndexByte(chunk, '\n'); i >= 0 {
					first = append([]byte(nil), chunk[:i]...)
				}
			}
			lines += bytes.Count(chunk, []byte{'\n'})
			lastByte = chunk[n-1]
			tail = append(tail, chunk[max(0, n-512):]...)
			if len(tail) > 512 {
				tail = append(tail[:0], tail[len(tail)-512:]...)
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			b.Fatal(err)
		}
	}
	if lines != b.N || lastByte != '\n' {
		b.Fatalf("%s: %d complete lines, want %d", path, lines, b.N)
	}
	trimmed := bytes.TrimSuffix(tail, []byte{'\n'})
	last := trimmed[bytes.LastIndexByte(trimmed, '\n')+1:]
	for _, line := range [][]byte{first, last} {
		if !bytes.Contains(line, []byte(msg)) || !timestamp.Match(line) {
			b.Fatalf("%s: line without the message or a timestamp: %q", path, line)
		}
	}
}

func justlog(b *testing.B, parallel bool) {
	path := filepath.Join(b.TempDir(), "bench.log")
	l, err := justlog3.NewLogger(path)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { l.Close() })
	run(b, parallel, func() { l.Log(msg, justlog3.OffConsole()) }, func() error { l.Flush(); return nil }, path)
}

func slogger(b *testing.B, parallel, buffered bool) {
	w, flush, path := writer(b, buffered)
	if parallel && buffered {
		w = &lockedWriter{w: w}
	}
	l := slog.New(slog.NewTextHandler(w, nil))
	run(b, parallel, func() { l.Info(msg) }, flush, path)
}

func zapper(b *testing.B, parallel, buffered bool) {
	f, path := open(b)
	var ws zapcore.WriteSyncer = noSync{f}
	flush := func() error { return nil }
	if buffered {
		bws := &zapcore.BufferedWriteSyncer{WS: noSync{f}, Size: 64 << 10}
		ws, flush = bws, bws.Stop
	}
	enc := zapcore.NewConsoleEncoder(zap.NewProductionEncoderConfig())
	l := zap.New(zapcore.NewCore(enc, ws, zapcore.InfoLevel))
	run(b, parallel, func() { l.Info(msg) }, flush, path)
}

func zerologger(b *testing.B, parallel, buffered bool) {
	w, flush, path := writer(b, buffered)
	if parallel && buffered {
		w = &lockedWriter{w: w}
	}
	l := zerolog.New(w).With().Timestamp().Logger()
	run(b, parallel, func() { l.Info().Msg(msg) }, flush, path)
}

// bufio.Writer is not safe for concurrent use; slog and zerolog don't lock around Write.
type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

func BenchmarkJustlog3(b *testing.B)                { justlog(b, false) }
func BenchmarkSlogDefault(b *testing.B)             { slogger(b, false, false) }
func BenchmarkSlogBuffered(b *testing.B)            { slogger(b, false, true) }
func BenchmarkZapDefault(b *testing.B)              { zapper(b, false, false) }
func BenchmarkZapBuffered(b *testing.B)             { zapper(b, false, true) }
func BenchmarkZerologDefault(b *testing.B)          { zerologger(b, false, false) }
func BenchmarkZerologBuffered(b *testing.B)         { zerologger(b, false, true) }
func BenchmarkParallelJustlog3(b *testing.B)        { justlog(b, true) }
func BenchmarkParallelSlogBuffered(b *testing.B)    { slogger(b, true, true) }
func BenchmarkParallelZapBuffered(b *testing.B)     { zapper(b, true, true) }
func BenchmarkParallelZerologBuffered(b *testing.B) { zerologger(b, true, true) }
