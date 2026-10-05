package justlog3

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

type answer struct {
	status int
	header map[string]string
}

// newScriptedServer answers requests from script in order (then 202) and
// records them with gzip bodies already unpacked.
func newScriptedServer(t *testing.T, script ...answer) (*httptest.Server, func() []captured) {
	t.Helper()
	var mu sync.Mutex
	var got []captured
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body io.Reader = r.Body
		if r.Header.Get("Content-Encoding") == "gzip" {
			gz, err := gzip.NewReader(r.Body)
			if err != nil {
				t.Errorf("bad gzip body: %v", err)
				return
			}
			body = gz
		}
		b, _ := io.ReadAll(body)
		mu.Lock()
		got = append(got, captured{header: r.Header.Clone(), body: string(b)})
		reply := answer{status: http.StatusAccepted}
		if len(script) > 0 {
			reply, script = script[0], script[1:]
		}
		mu.Unlock()
		for k, v := range reply.header {
			w.Header().Set(k, v)
		}
		w.WriteHeader(reply.status)
	}))
	t.Cleanup(srv.Close)
	return srv, func() []captured {
		mu.Lock()
		defer mu.Unlock()
		return append([]captured(nil), got...)
	}
}

func TestCloudCompressesBigBodies(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log")
	reset(t)
	srv, requests := newScriptedServer(t)
	SetAPIToken("tok", WithURL(srv.URL), WithFlushInterval(time.Hour))
	l, _ := NewLogger(path, WithTime(false), WithCloudCycles(1000))
	l.Log("short", OffConsole())
	cloud.Load().drain(nil)
	for i := 0; i < 50; i++ {
		l.Log(strings.Repeat("x", 100), OffConsole())
	}
	cloud.Load().drain(nil)

	got := requests()
	if len(got) != 2 {
		t.Fatalf("%d requests, want 2", len(got))
	}
	if got[0].header.Get("Content-Encoding") != "" || got[0].body != "GREY: short" {
		t.Fatalf("small batch: encoding %q body %q", got[0].header.Get("Content-Encoding"), got[0].body)
	}
	if got[1].header.Get("Content-Encoding") != "gzip" || strings.Count(got[1].body, "\n") != 49 {
		t.Fatalf("big batch: encoding %q, %d lines", got[1].header.Get("Content-Encoding"), strings.Count(got[1].body, "\n")+1)
	}
}

func TestCloudSplitsBacklogByBytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log")
	reset(t)
	srv, requests := newScriptedServer(t)
	SetAPIToken("tok", WithURL(srv.URL), WithFlushInterval(time.Hour), WithMaxBatch(10_000))
	l, _ := NewLogger(path, WithTime(false), WithCloudCycles(CloudBufMax*2))
	line := strings.Repeat("y", 8<<10)
	for i := 0; i < 200; i++ {
		l.Log(strconv.Itoa(i)+" "+line, OffConsole())
	}
	cloud.Load().drain(nil)

	got := requests()
	if len(got) < 4 {
		t.Fatalf("%d requests for 1.6 MB, want at least 4", len(got))
	}
	total := 0
	for _, r := range got {
		if len(r.body) > maxBatchBytes {
			t.Fatalf("request of %d bytes over the %d cap", len(r.body), maxBatchBytes)
		}
		total += strings.Count(r.body, "\n") + 1
	}
	if total != 200 {
		t.Fatalf("%d lines arrived, want 200", total)
	}
	if n, _ := cloudState(l); n != 0 {
		t.Fatalf("%d lines left", n)
	}
}

func TestCloud429KeepsLinesAndWaits(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log")
	reset(t)
	srv, requests := newScriptedServer(t, answer{status: http.StatusTooManyRequests, header: map[string]string{"Retry-After": "120"}})
	SetAPIToken("tok", WithURL(srv.URL), WithFlushInterval(time.Hour))
	l, _ := NewLogger(path, WithTime(false), WithCloudCycles(1000))
	l.Log("a", OffConsole())
	l.Log("b", OffConsole())

	if cloud.Load().drain(nil) {
		t.Fatal("drain after a 429 must report that sending waits")
	}
	cloud.Load().drain(nil) // still paused: no request

	if n := len(requests()); n != 1 {
		t.Fatalf("%d requests, want 1", n)
	}
	if n, _ := cloudState(l); n != 2 {
		t.Fatalf("%d lines buffered, want 2", n)
	}
	cloud.Load().pausedUntil.Store(0)
	cloud.Load().drain(nil)
	if n, _ := cloudState(l); n != 0 {
		t.Fatalf("%d lines left after the pause", n)
	}
}

func TestCloud413HalvesTheBatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log")
	reset(t)
	srv, requests := newScriptedServer(t, answer{status: http.StatusRequestEntityTooLarge})
	SetAPIToken("tok", WithURL(srv.URL), WithFlushInterval(time.Hour))
	l, _ := NewLogger(path, WithTime(false), WithCloudCycles(1000))
	for i := 0; i < 10; i++ {
		l.Log(strconv.Itoa(i), OffConsole())
	}
	cloud.Load().drain(nil)

	var sizes []int
	for _, r := range requests() {
		sizes = append(sizes, strings.Count(r.body, "\n")+1)
	}
	if len(sizes) != 3 || sizes[0] != 10 || sizes[1] != 5 || sizes[2] != 5 {
		t.Fatalf("batch sizes %v, want [10 5 5]", sizes)
	}
}

func TestBackpressureWithCloudKeepsEveryLine(t *testing.T) {
	// When the disk writer lags, Log takes the disk buffer itself once it
	// reaches maxPendingBytes; the same line must still reach the cloud buffer.
	path := filepath.Join(t.TempDir(), "app.log")
	reset(t)
	srv, _ := newScriptedServer(t)
	SetAPIToken("tok", WithURL(srv.URL), WithFlushInterval(time.Hour))
	l, _ := NewLogger(path, WithTime(false), WithCycles(1), WithCloudCycles(CloudBufMax*2))
	l.mu.Lock()
	l.writeQueued = true // the writer goroutine "has been woken" but never takes the buffer
	l.mu.Unlock()

	line := strings.Repeat("p", 64<<10)
	const n = 20 // 20 x 64 KB: crosses maxPendingBytes (1 MB) once
	for i := 0; i < n; i++ {
		l.Log(line, OffConsole())
	}

	lines, body := cloudState(l)
	if lines != n || len(body) != n*(len("GREY: ")+len(line)+1) {
		t.Fatalf("cloud buffer has %d lines / %d bytes, want %d lines", lines, len(body), n)
	}
	l.Flush()
	if got := strings.Count(readFile(t, path), "\n"); got != n {
		t.Fatalf("file has %d lines, want %d", got, n)
	}
}

func TestCloudServerErrorResendsSameSeq(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log")
	reset(t)
	srv, requests := newScriptedServer(t, answer{status: http.StatusServiceUnavailable})
	SetAPIToken("tok", WithURL(srv.URL), WithFlushInterval(time.Hour))
	l, _ := NewLogger(path, WithTime(false), WithCloudCycles(1000))
	l.Log("a", OffConsole())
	cloud.Load().drain(nil)
	if n, _ := cloudState(l); n != 1 {
		t.Fatalf("batch must stay after a 5xx, have %d lines", n)
	}
	cloud.Load().drain(nil)

	got := requests()
	if len(got) != 2 || got[0].body != got[1].body || got[0].header.Get("X-JustLog-Seq") != got[1].header.Get("X-JustLog-Seq") {
		t.Fatalf("retry differs: %+v", got)
	}
	if n, _ := cloudState(l); n != 0 {
		t.Fatalf("%d lines left", n)
	}
}
