package justlog3

import (
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var (
	cloudMu sync.Mutex
	cloud   atomic.Pointer[cloudSender]
)

const (
	// maxBatchBytes caps the text of one request (the server takes 1 MB as
	// sent and 4 MB unpacked); a bigger backlog goes out in several requests.
	maxBatchBytes = 512 << 10
	// compressFrom: bodies of at least this many bytes are sent gzip-compressed.
	compressFrom = 1 << 10
	// defaultRetryAfter is used when a 429 carries no usable Retry-After.
	defaultRetryAfter = 60 * time.Second
)

// CloudOption configures SetAPIToken.
type CloudOption func(*cloudConfig)

type cloudConfig struct {
	url           string
	flushInterval time.Duration
	timeout       time.Duration
	maxBatch      int
}

// WithURL overrides the JustLog3 Cloud endpoint (default: DefaultURL).
func WithURL(u string) CloudOption { return func(c *cloudConfig) { c.url = u } }

// WithFlushInterval sets how often buffered lines are sent even if
// cloud cycles are not reached (default: 5s).
func WithFlushInterval(d time.Duration) CloudOption {
	return func(c *cloudConfig) { c.flushInterval = d }
}

// WithTimeout sets the HTTP timeout of a single batch request (default: 5s).
func WithTimeout(d time.Duration) CloudOption { return func(c *cloudConfig) { c.timeout = d } }

// WithMaxBatch sets the maximum number of lines in one request (default: 100).
// A bigger backlog is sent as several requests in a row, so a burst of logs
// never turns into one huge request that runs into the timeout.
func WithMaxBatch(n int) CloudOption { return func(c *cloudConfig) { c.maxBatch = n } }

// SetAPIToken enables sending logs to JustLog3 Cloud. Call it once, before or
// after creating loggers. Calling it again replaces the previous token: the old
// sender delivers what it can and stops, then the new one takes over the buffers.
//
// The token is checked by the server on every batch: on HTTP 401/403 cloud
// sending stops (logs keep being written to files) until SetAPIToken is called again.
func SetAPIToken(token string, opts ...CloudOption) error {
	token = strings.TrimSpace(token)
	if token == "" {
		return errors.New("justlog3: token must be a non-empty string")
	}
	cfg := cloudConfig{url: DefaultURL, flushInterval: 5 * time.Second, timeout: 5 * time.Second, maxBatch: 100}
	for _, opt := range opts {
		opt(&cfg)
	}
	if u, err := url.Parse(cfg.url); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("justlog3: invalid url %q, want http(s)://host/path", cfg.url)
	}
	if cfg.flushInterval <= 0 || cfg.timeout <= 0 {
		return errors.New("justlog3: flush interval and timeout must be positive")
	}
	if cfg.maxBatch < 1 {
		return errors.New("justlog3: max batch must be >= 1")
	}

	cloudMu.Lock()
	defer cloudMu.Unlock()
	if old := cloud.Load(); old != nil {
		old.shutdown()
	}
	cloud.Store(newCloudSender(token, cfg))
	fmt.Println("[JustLog3] Cloud: log sending enabled.")
	return nil
}

type cloudSender struct {
	token         string
	url           string
	flushInterval time.Duration
	timeout       time.Duration
	maxBatch      int
	sessionID     string
	client        *http.Client

	disabled atomic.Bool
	warned   atomic.Bool
	seq      atomic.Uint64
	// batchLines starts at maxBatch and is halved by every HTTP 413.
	batchLines atomic.Int64
	// pausedUntil (unix nanoseconds): after a 429 nothing is sent before Retry-After.
	pausedUntil atomic.Int64

	drainMu  sync.Mutex
	wakeCh   chan struct{}
	stopCh   chan struct{}
	done     chan struct{}
	stopOnce sync.Once
}

func newCloudSender(token string, cfg cloudConfig) *cloudSender {
	c := &cloudSender{
		token:         token,
		url:           cfg.url,
		flushInterval: cfg.flushInterval,
		timeout:       cfg.timeout,
		maxBatch:      cfg.maxBatch,
		sessionID:     newSessionID(),
		client:        &http.Client{Timeout: cfg.timeout},
		wakeCh:        make(chan struct{}, 1),
		stopCh:        make(chan struct{}),
		done:          make(chan struct{}),
	}
	c.batchLines.Store(int64(cfg.maxBatch))
	go c.run()
	return c
}

func (c *cloudSender) paused() bool { return time.Now().UnixNano() < c.pausedUntil.Load() }

// newSessionID returns a random UUID4 in hex form without dashes, like uuid.uuid4().hex.
func newSessionID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 16)
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return hex.EncodeToString(b[:])
}

func (c *cloudSender) wake() {
	select {
	case c.wakeCh <- struct{}{}:
	default:
	}
}

func (c *cloudSender) run() {
	defer close(c.done)
	timer := time.NewTimer(c.flushInterval)
	defer timer.Stop()
	failed := false
	for {
		// After a failed request only the timer retries: otherwise every Log
		// past cloud cycles would wake us into another request to a dead server.
		wakeCh := c.wakeCh
		if failed {
			wakeCh = nil
		}
		select {
		case <-c.stopCh:
			return
		case <-wakeCh:
		case <-timer.C:
		}
		if !c.disabled.Load() {
			failed = !c.drain(c.stopCh)
		}
		timer.Reset(c.flushInterval)
	}
}

func (c *cloudSender) shutdown() {
	c.stopOnce.Do(func() {
		close(c.stopCh)
		select {
		case <-c.done:
		case <-time.After(c.timeout + time.Second):
		}
		if !c.disabled.Load() {
			c.drain(nil)
		}
		c.client.CloseIdleConnections()
	})
}

// drain sends the backlog of every logger and reports false if sending has to
// wait: a request got no answer or a 5xx, or the server asked to pause (429).
// It returns early once stop is closed, so Shutdown can take over.
func (c *cloudSender) drain(stop <-chan struct{}) bool {
	c.drainMu.Lock()
	defer c.drainMu.Unlock()
	ok := true
	for _, l := range loggersSnapshot() {
		// Send only the backlog present now, so steady logging can't keep us on one logger.
		l.mu.Lock()
		backlogEnd := l.cloudHead + uint64(len(l.cloudBuf))
		l.mu.Unlock()
		for {
			if c.disabled.Load() {
				return ok
			}
			if c.paused() {
				return false
			}
			select {
			case <-stop:
				return ok
			default:
			}
			l.mu.Lock()
			pending := len(l.cloudEnds) > 0 && l.cloudHead < backlogEnd
			l.mu.Unlock()
			if !pending {
				break
			}
			if !c.drainLogger(l) {
				ok = false
				break
			}
		}
	}
	return ok
}

// drainLogger sends the oldest lines of l that fit into one request (at most
// batchLines lines and maxBatchBytes of text) and reports whether the next
// request may follow right away.
func (c *cloudSender) drainLogger(l *Logger) bool {
	l.mu.Lock()
	k := min(len(l.cloudEnds), int(c.batchLines.Load()))
	if k == 0 {
		l.mu.Unlock()
		return false
	}
	head := l.cloudHead
	// Lines ending past the byte cap stay for the next request; one line always goes.
	k = max(1, sort.Search(k, func(i int) bool { return l.cloudEnds[i]-head > maxBatchBytes }))
	sentEnd := l.cloudEnds[k-1]
	var seq uint64
	if r := l.cloudRetry; r.owner == c && r.head == head {
		// The previous attempt timed out but may have reached the server:
		// resend exactly the same batch under the same seq so it can be deduplicated.
		sentEnd, seq = r.end, r.seq
		k = sort.Search(len(l.cloudEnds), func(i int) bool { return l.cloudEnds[i] >= sentEnd }) + 1
	} else {
		seq = c.seq.Add(1)
	}
	l.cloudRetry = cloudRetry{}
	// No copy: Log only appends past these bytes or reslices, and drains are
	// serialized, so they stay unchanged while we read them.
	body := l.cloudBuf[:sentEnd-head-1]
	l.mu.Unlock()

	var bb *batchBody
	var payload io.Reader
	compressed := len(body) >= compressFrom
	if compressed {
		packed, err := gzipBytes(body)
		if err != nil {
			c.warnOnce(fmt.Sprintf("Cloud batch could not be compressed: %v", err))
			return false
		}
		payload = bytes.NewReader(packed) // owns its bytes: GetBody and ContentLength are set for it
	}
	req, err := http.NewRequest(http.MethodPost, c.url, payload)
	if err != nil {
		c.warnOnce(fmt.Sprintf("Cloud request could not be built: %v", err))
		return false
	}
	if compressed {
		req.Header.Set("Content-Encoding", "gzip")
	} else {
		bb = &batchBody{data: body}
		defer bb.detach()
		req.Body, req.GetBody, req.ContentLength = bb.reader(), bb.getBody, int64(len(body))
	}
	offset := ""
	if l.withTime {
		_, off := time.Now().Zone()
		offset = strconv.Itoa(off)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "text/plain; charset=utf-8")
	req.Header.Set("X-JustLog-Session", c.sessionID)
	req.Header.Set("X-JustLog-Seq", strconv.FormatUint(seq, 10))
	req.Header.Set("X-JustLog-Logger", l.filename)
	req.Header.Set("X-JustLog-UTC-Offset", offset)

	resp, err := c.client.Do(req)
	if bb != nil {
		// The transport may still be reading the body in the background (e.g. the
		// server answered early); cut it off before the buffer can be reused.
		bb.detach()
	}
	keepForRetry := func() {
		l.mu.Lock()
		l.cloudRetry = cloudRetry{owner: c, head: head, end: sentEnd, seq: seq}
		l.mu.Unlock()
	}
	if err != nil {
		// Network error: the batch stays in the buffer and goes out next time.
		keepForRetry()
		c.warnOnce(fmt.Sprintf("Cloud request failed (no answer), the batch is kept and will be resent: %v", err))
		return false
	}
	io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	resp.Body.Close()

	switch status := resp.StatusCode; {
	case status == http.StatusTooManyRequests:
		// Rate or daily limit: nothing was stored, the lines stay buffered
		// (up to CloudBufMax per logger) and sending waits for Retry-After.
		wait := retryAfter(resp.Header.Get("Retry-After"))
		c.pausedUntil.Store(time.Now().Add(wait).UnixNano())
		c.warnOnce(fmt.Sprintf("Cloud limit reached (HTTP 429), sending resumes in %s; lines stay buffered.", wait.Round(time.Second)))
		return false
	case status >= 500:
		// The server failed; same batch, same seq next time.
		keepForRetry()
		c.warnOnce(fmt.Sprintf("Cloud server error (HTTP %d), the batch is kept and will be resent.", status))
		return false
	case status == http.StatusRequestEntityTooLarge && k > 1:
		// Too big for this server: half as many lines per request from now on.
		c.batchLines.Store(int64(max(1, k/2)))
		return true
	}

	// Stored, or refused for good: either way these lines are done.
	// An overflow during the request may already have dropped part of them.
	l.mu.Lock()
	if sentEnd > l.cloudHead {
		k := 0
		for k < len(l.cloudEnds) && l.cloudEnds[k] <= sentEnd {
			k++
		}
		// The body is detached and drains are serialized, so nothing reads
		// these bytes now and they may be moved.
		l.cloudBuf = dropFront(l.cloudBuf, int(sentEnd-l.cloudHead))
		l.cloudEnds = dropFront(l.cloudEnds, k)
		l.cloudHead = sentEnd
	}
	l.mu.Unlock()

	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		c.disabled.Store(true)
		printErr(fmt.Sprintf("API token rejected by the server (HTTP %d). Cloud sending stopped; logs keep being written to files. Check the token and call SetAPIToken() again.", resp.StatusCode))
	case resp.StatusCode >= 400:
		c.warnOnce(fmt.Sprintf("Cloud rejected batch #%d (HTTP %d), batch lost.", seq, resp.StatusCode))
	case resp.Header.Get("X-JustLog-Lines-Rejected") != "":
		c.warnOnce(fmt.Sprintf("Daily cloud limit reached: %s lines of batch #%d were not stored.", resp.Header.Get("X-JustLog-Lines-Rejected"), seq))
	default:
		c.warned.Store(false)
	}
	return true
}

// gzipBytes compresses a batch; logs typically shrink 5-10x.
func gzipBytes(data []byte) ([]byte, error) {
	var out bytes.Buffer
	out.Grow(len(data) / 4)
	w := gzip.NewWriter(&out)
	if _, err := w.Write(data); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// retryAfter reads a Retry-After header given in seconds (the form JustLog3 sends).
func retryAfter(value string) time.Duration {
	seconds, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || seconds < 1 {
		return defaultRetryAfter
	}
	return time.Duration(seconds) * time.Second
}

func (c *cloudSender) warnOnce(msg string) {
	if c.warned.CompareAndSwap(false, true) {
		printErr(msg)
	}
}

// cloudRetry remembers a batch whose request failed without an answer.
type cloudRetry struct {
	owner *cloudSender
	head  uint64 // cloudHead when it was sent; if lines were dropped since, it is no longer the same batch
	end   uint64
	seq   uint64
}

var errBatchDetached = errors.New("justlog3: batch body no longer available")

// batchBody serves a batch that aliases a logger's cloud buffer. After detach,
// reads fail instead of touching memory the logger may reuse.
type batchBody struct {
	mu       sync.Mutex
	data     []byte
	detached bool
}

func (b *batchBody) detach() {
	b.mu.Lock()
	b.detached, b.data = true, nil
	b.mu.Unlock()
}

func (b *batchBody) reader() io.ReadCloser { return &batchReader{b: b} }

func (b *batchBody) getBody() (io.ReadCloser, error) { return b.reader(), nil }

type batchReader struct {
	b   *batchBody
	off int
}

func (r *batchReader) Read(p []byte) (int, error) {
	r.b.mu.Lock()
	defer r.b.mu.Unlock()
	if r.b.detached {
		return 0, errBatchDetached
	}
	if r.off >= len(r.b.data) {
		return 0, io.EOF
	}
	n := copy(p, r.b.data[r.off:])
	r.off += n
	return n, nil
}

func (r *batchReader) Close() error { return nil }

// dropFront removes the first n elements of s. When the rest is no longer than
// what was removed it is moved to the front, so the array is reused instead of
// creeping forward and being reallocated; each element is moved O(1) times on average.
func dropFront[T any](s []T, n int) []T {
	rest := len(s) - n
	if rest <= n {
		copy(s, s[n:])
		return s[:rest]
	}
	return s[n:]
}
