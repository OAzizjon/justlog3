package justlog3

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const benchMsg = "user 42 logged in from 10.0.0.1, request id 7f3a9c, took 12ms"

// silenceStdout points os.Stdout at the null device so console cost is the write itself.
func silenceStdout(b *testing.B) {
	null, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		b.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = null
	b.Cleanup(func() { os.Stdout = old; null.Close() })
}

func benchLogger(b *testing.B, opts ...Option) *Logger {
	b.Helper()
	path := filepath.Join(b.TempDir(), "bench.log")
	clean := func() {
		Shutdown()
		registryMu.Lock()
		allLoggers = nil
		registryMu.Unlock()
	}
	clean()
	b.Cleanup(clean)
	l, err := NewLogger(path, opts...)
	if err != nil {
		b.Fatal(err)
	}
	return l
}

func BenchmarkFileOnly(b *testing.B) {
	l := benchLogger(b)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		l.Log(benchMsg, OffConsole())
	}
}

func BenchmarkFileOnlyNoTime(b *testing.B) {
	l := benchLogger(b, WithTime(false))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		l.Log(benchMsg, OffConsole())
	}
}

func BenchmarkFileAndConsole(b *testing.B) {
	silenceStdout(b)
	l := benchLogger(b)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		l.Log(benchMsg, Red())
	}
}

func BenchmarkBasicInfo(b *testing.B) {
	silenceStdout(b)
	benchLogger(b)
	bl, _ := NewBasicLogger(filepath.Join(b.TempDir(), "basic.log"))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		bl.Info(benchMsg)
	}
}

func BenchmarkFileAndCloud(b *testing.B) {
	l := benchLogger(b)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusAccepted)
	}))
	b.Cleanup(srv.Close)
	if err := SetAPIToken("bench", WithURL(srv.URL), WithFlushInterval(time.Hour)); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		l.Log(benchMsg, OffConsole())
	}
}

func BenchmarkParallelFileOnly(b *testing.B) {
	l := benchLogger(b)
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			l.Log(benchMsg, OffConsole())
		}
	})
}
