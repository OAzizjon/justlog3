package justlog3

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type captured struct {
	header http.Header
	body   string
}

// reset clears global state so tests don't see each other's loggers and senders.
func reset(t *testing.T) {
	t.Helper()
	clean := func() {
		Shutdown()
		registryMu.Lock()
		allLoggers = nil
		registryMu.Unlock()
	}
	clean()
	t.Cleanup(clean)
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met in time")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func newServer(t *testing.T, status int) (*httptest.Server, chan captured) {
	t.Helper()
	reqs := make(chan captured, 16)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		reqs <- captured{header: r.Header.Clone(), body: string(b)}
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	return srv, reqs
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestFileFlushOnCycles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app")
	reset(t)
	l, err := NewLogger(path, WithCycles(3), WithTime(false))
	if err != nil {
		t.Fatal(err)
	}
	l.Log("a", OffConsole())
	l.Log("b", OffConsole(), Red())
	if _, err := os.Stat(path + ".log"); !os.IsNotExist(err) {
		t.Fatalf("file written before cycles reached: %v", err)
	}
	l.Log("c", OffConsole(), Green(), Prefix("OK"))
	// The background writer picks the buffer up without Flush.
	want := "GREY: a\nRED: b\nOK: c\n"
	waitFor(t, func() bool { b, _ := os.ReadFile(path + ".log"); return string(b) == want })
}

func TestFilemodeW(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log")
	reset(t)
	os.WriteFile(path, []byte("old\n"), 0o644)
	l, err := NewLogger(path, WithFilemode("w"), WithTime(false))
	if err != nil {
		t.Fatal(err)
	}
	l.Log("new", OffConsole())
	l.Flush()
	if got := readFile(t, path); got != "GREY: new\n" {
		t.Fatalf("got %q", got)
	}
}

func TestBasicLoggerLevels(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log")
	reset(t)
	if _, err := NewBasicLogger(path, WithConsoleLevel("LOUD")); err == nil {
		t.Fatal("expected error for invalid console level")
	}
	b, err := NewBasicLogger(path, WithConsoleLevel("critical"), WithTime(false))
	if err != nil {
		t.Fatal(err)
	}
	b.Info("hi")
	b.Errorf("code %d", 7)
	b.Flush()
	if got := readFile(t, path); got != "INFO: hi\nERROR: code 7\n" {
		t.Fatalf("got %q", got)
	}
	if s := b.Status(); s.LoggerName != "BasicLogger" || s.ConsoleLevel != "CRITICAL" {
		t.Fatalf("bad status %+v", s)
	}
}

func TestCloudSendsBatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "svc.log")
	reset(t)
	srv, reqs := newServer(t, http.StatusOK)
	if err := SetAPIToken("  tok  ", WithURL(srv.URL), WithFlushInterval(time.Hour)); err != nil {
		t.Fatal(err)
	}
	l, _ := NewLogger(path, WithCloudCycles(2), WithTime(false))
	l.Log("a", OffConsole())
	l.Log("b", OffConsole())

	select {
	case r := <-reqs:
		if r.body != "GREY: a\nGREY: b" {
			t.Fatalf("body %q", r.body)
		}
		checks := map[string]string{
			"Authorization":        "Bearer tok",
			"Content-Type":         "text/plain; charset=utf-8",
			"X-Justlog-Seq":        "1",
			"X-Justlog-Logger":     path,
			"X-Justlog-Utc-Offset": "",
		}
		for k, v := range checks {
			if got := r.header.Get(k); got != v {
				t.Errorf("header %s = %q, want %q", k, got, v)
			}
		}
		if len(r.header.Get("X-Justlog-Session")) != 32 {
			t.Errorf("bad session id %q", r.header.Get("X-Justlog-Session"))
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no batch sent")
	}
	waitFor(t, func() bool { l.mu.Lock(); defer l.mu.Unlock(); return len(l.cloudEnds) == 0 })
}

func TestCloudTokenRejected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log")
	reset(t)
	srv, reqs := newServer(t, http.StatusUnauthorized)
	SetAPIToken("bad", WithURL(srv.URL), WithFlushInterval(time.Hour))
	l, _ := NewLogger(path, WithCloudCycles(1))
	l.Log("a", OffConsole())
	<-reqs
	waitFor(t, func() bool { return cloud.Load().disabled.Load() })

	l.Log("b", OffConsole())
	l.mu.Lock()
	n := len(l.cloudEnds)
	l.mu.Unlock()
	if n != 0 {
		t.Fatalf("lines buffered for cloud after token was rejected: %d", n)
	}
	l.Flush()
	if got := readFile(t, path); !strings.Contains(got, " b\n") {
		t.Fatalf("file logging must continue, got %q", got)
	}
}

func TestCloudUnreachableKeepsBuffer(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log")
	reset(t)
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	SetAPIToken("tok", WithURL(url), WithFlushInterval(time.Hour), WithTimeout(time.Second))
	l, _ := NewLogger(path)
	l.Log("a", OffConsole())
	cloud.Load().drain(nil)
	l.mu.Lock()
	n := len(l.cloudEnds)
	l.mu.Unlock()
	if n != 1 {
		t.Fatalf("batch must stay in buffer on network error, have %d lines", n)
	}
}

func cloudState(l *Logger) (lines int, body string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.cloudEnds), string(l.cloudBuf)
}

func TestTimestampFormat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log")
	reset(t)
	l, _ := NewLogger(path)
	l.Log("x", OffConsole())
	l.Flush()
	got := readFile(t, path)
	if !regexp.MustCompile(`^GREY:\d{4}-\d\d-\d\d \d\d:\d\d:\d\d\.\d{3} - x\n$`).MatchString(got) {
		t.Fatalf("bad line %q", got)
	}
}

func TestConcurrentLogging(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log")
	reset(t)
	l, _ := NewLogger(path, WithCycles(7), WithTime(false))
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 1000; i++ {
				l.Log("line", OffConsole())
			}
		}()
	}
	wg.Wait()
	l.Close()
	if n := strings.Count(readFile(t, path), "GREY: line\n"); n != 8000 {
		t.Fatalf("got %d lines, want 8000", n)
	}
}

func TestCloudOverflowAndMultiline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log")
	reset(t)
	srv, _ := newServer(t, http.StatusOK)
	SetAPIToken("tok", WithURL(srv.URL), WithFlushInterval(time.Hour))
	l, _ := NewLogger(path, WithTime(false), WithCloudCycles(CloudBufMax*2))
	l.Log("first\nline", OffConsole())
	for i := 0; i < CloudBufMax; i++ {
		l.Log(strconv.Itoa(i), OffConsole())
	}
	lines, body := cloudState(l)
	if lines != CloudBufMax {
		t.Fatalf("got %d lines, want %d", lines, CloudBufMax)
	}
	if !strings.HasPrefix(body, "GREY: 0\n") || !strings.HasSuffix(body, "GREY: 9999\n") {
		t.Fatalf("wrong lines kept: %q ... %q", body[:20], body[len(body)-20:])
	}
}

func TestCloudKeepsLinesLoggedDuringSend(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log")
	reset(t)
	entered, release := make(chan string), make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		entered <- string(b)
		<-release
	}))
	t.Cleanup(srv.Close)
	SetAPIToken("tok", WithURL(srv.URL), WithFlushInterval(time.Hour))
	l, _ := NewLogger(path, WithTime(false), WithCloudCycles(2))
	l.Log("a\nmulti", OffConsole())
	l.Log("b", OffConsole())
	if got := <-entered; got != "GREY: a\nmulti\nGREY: b" {
		t.Fatalf("body %q", got)
	}
	l.Log("c", OffConsole()) // arrives while the batch is in flight
	close(release)
	waitFor(t, func() bool { n, _ := cloudState(l); return n == 1 })
	if _, body := cloudState(l); body != "GREY: c\n" {
		t.Fatalf("left %q", body)
	}
	go func() { <-entered }()
}

func TestCloudSplitsBacklogIntoBatches(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log")
	reset(t)
	srv, reqs := newServer(t, http.StatusAccepted)
	SetAPIToken("tok", WithURL(srv.URL), WithFlushInterval(time.Hour), WithMaxBatch(2))
	l, _ := NewLogger(path, WithTime(false), WithCloudCycles(100))
	for _, m := range []string{"1", "2", "3", "4", "5"} {
		l.Log(m, OffConsole())
	}
	Shutdown()
	var got []string
	for len(reqs) > 0 {
		r := <-reqs
		got = append(got, r.body+" #"+r.header.Get("X-JustLog-Seq"))
	}
	want := []string{"GREY: 1\nGREY: 2 #1", "GREY: 3\nGREY: 4 #2", "GREY: 5 #3"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestCloudRetryKeepsSeqAndBody(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log")
	reset(t)
	var calls atomic.Int32
	reqs := make(chan captured, 8)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		reqs <- captured{header: r.Header.Clone(), body: string(b)}
		if calls.Add(1) == 1 {
			time.Sleep(300 * time.Millisecond) // longer than the client timeout
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(srv.Close)
	SetAPIToken("tok", WithURL(srv.URL), WithFlushInterval(time.Hour), WithTimeout(100*time.Millisecond))
	l, _ := NewLogger(path, WithTime(false), WithCloudCycles(100))
	l.Log("a", OffConsole())
	cloud.Load().drain(nil) // times out
	l.Log("b", OffConsole())
	cloud.Load().drain(nil) // retry: same batch, same seq
	cloud.Load().drain(nil) // then the new line under a new seq

	first, retry, next := <-reqs, <-reqs, <-reqs
	if first.body != "GREY: a" || retry.body != first.body || next.body != "GREY: b" {
		t.Fatalf("bodies %q %q %q", first.body, retry.body, next.body)
	}
	s1, s2, s3 := first.header.Get("X-JustLog-Seq"), retry.header.Get("X-JustLog-Seq"), next.header.Get("X-JustLog-Seq")
	if s1 != "1" || s2 != "1" || s3 != "2" {
		t.Fatalf("seqs %s %s %s, want 1 1 2", s1, s2, s3)
	}
}

func TestShutdownSendsRest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log")
	reset(t)
	srv, reqs := newServer(t, http.StatusOK)
	SetAPIToken("tok", WithURL(srv.URL), WithFlushInterval(time.Hour))
	l, _ := NewLogger(path)
	l.Log("last words", OffConsole())
	Shutdown()
	select {
	case r := <-reqs:
		if !strings.HasSuffix(r.body, " last words") {
			t.Fatalf("body %q", r.body)
		}
	default:
		t.Fatal("Shutdown did not send the remaining lines")
	}
	if got := readFile(t, path); !strings.HasSuffix(got, " last words\n") {
		t.Fatalf("Shutdown did not flush the file, got %q", got)
	}
}

func TestFileFollowsRotation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log")
	reset(t)
	old := rotationCheck
	rotationCheck = 0
	t.Cleanup(func() { rotationCheck = old })

	l, _ := NewLogger(path, WithTime(false))
	l.Log("before", OffConsole())
	l.Flush()
	// The file is open; renaming it must work (on Windows too) and the next write must go to a new file.
	if err := os.Rename(path, path+".1"); err != nil {
		t.Fatalf("rename of an open log failed: %v", err)
	}
	l.Log("after", OffConsole())
	l.Flush()
	if got := readFile(t, path+".1"); got != "GREY: before\n" {
		t.Fatalf("rotated file %q", got)
	}
	if got := readFile(t, path); got != "GREY: after\n" {
		t.Fatalf("new file %q", got)
	}
}

func TestNoRetryStormWhenServerFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log")
	reset(t)
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		conn, _, _ := w.(http.Hijacker).Hijack()
		conn.Close() // no answer: a network error for the client
	}))
	t.Cleanup(srv.Close)
	SetAPIToken("tok", WithURL(srv.URL), WithFlushInterval(time.Hour))
	l, _ := NewLogger(path, WithCloudCycles(1))
	for i := 0; i < 200; i++ {
		l.Log("x", OffConsole())
		time.Sleep(time.Millisecond)
	}
	if n := calls.Load(); n > 2 {
		t.Fatalf("%d requests after a failure, want retries only on the flush interval", n)
	}
}

func TestSetAPITokenValidatesURL(t *testing.T) {
	reset(t)
	for _, u := range []string{"", "127.0.0.1:8000/api/logs/", "ftp://host/x", "http://"} {
		if err := SetAPIToken("tok", WithURL(u)); err == nil {
			t.Errorf("url %q accepted", u)
		}
	}
}
