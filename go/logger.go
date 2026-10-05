package justlog3

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Option configures NewLogger and NewBasicLogger.
type Option func(*config)

type config struct {
	withTime     bool
	filemode     string
	cycles       int
	cloudCycles  int
	consoleLevel string
}

func defaultConfig() config {
	return config{withTime: true, filemode: "a", cycles: 50, cloudCycles: 75, consoleLevel: "DEBUG"}
}

// WithTime turns the timestamp in every line on or off (default: on).
func WithTime(on bool) Option { return func(c *config) { c.withTime = on } }

// WithFilemode sets "a" (append, default) or "w" (truncate the file once on creation).
func WithFilemode(mode string) Option { return func(c *config) { c.filemode = mode } }

// WithCycles sets how many lines are buffered before they are written to disk (default: 50).
func WithCycles(n int) Option { return func(c *config) { c.cycles = n } }

// WithCloudCycles sets how many lines are buffered before the cloud sender is woken up (default: 75).
func WithCloudCycles(n int) Option { return func(c *config) { c.cloudCycles = n } }

// WithConsoleLevel sets the minimum level printed to the console by a BasicLogger:
// NOTSET, DEBUG, INFO, WARNING, ERROR / SUCCESS, CRITICAL (default: DEBUG).
// Lines below it are still written to the file and sent to the cloud. Plain Logger ignores it.
func WithConsoleLevel(level string) Option { return func(c *config) { c.consoleLevel = level } }

// LogOption changes how a single Log call looks. It is a plain value (not a
// func) so that Log does not allocate.
type LogOption struct{ o logOpts }

type logOpts struct {
	red, green, offConsole bool
	prefix                 string
}

// Red prints the line in red with the RED prefix.
func Red() LogOption { return LogOption{logOpts{red: true}} }

// Green prints the line in green with the GREEN prefix.
func Green() LogOption { return LogOption{logOpts{green: true}} }

// Prefix replaces the default RED / GREEN / GREY prefix.
func Prefix(p string) LogOption { return LogOption{logOpts{prefix: p}} }

// OffConsole writes the line only to the file and the cloud, not to the console.
func OffConsole() LogOption { return LogOption{logOpts{offConsole: true}} }

const (
	// Disk buffers that grew larger than this are not kept for reuse.
	maxReusedBuf = 4 << 20
	// Console scratch buffers that grew larger than this are not kept for reuse.
	maxReusedScratch = 64 << 10
	// If the writer goroutine falls this far behind, Log writes to disk itself
	// (backpressure), so a slow disk can't make the buffer grow without limit.
	maxPendingBytes = 1 << 20
)

// How often the open file is checked against its path, to follow log rotation.
var rotationCheck = time.Second

var scratchPool = sync.Pool{New: func() any { b := make([]byte, 0, 512); return &b }}

// Logger writes colored lines to the console and buffered lines to a .log file,
// and to JustLog3 Cloud once SetAPIToken has been called. It is safe for concurrent use.
//
// Every WithCycles lines the buffer is handed to a background goroutine that
// writes it to disk, so Log itself does not wait for the disk. The file stays
// open between writes and is reopened if it is renamed or deleted (log
// rotation); Close or Shutdown releases it.
type Logger struct {
	kind        string
	filename    string
	withTime    bool
	filemode    string
	cycles      int
	cloudCycles int

	mu          sync.Mutex
	buf         []byte // lines waiting for disk
	bufLines    int
	spare       []byte // previous buf, reused after it has been written
	writeQueued bool   // the writer goroutine has been woken and has not taken buf yet
	writeCh     chan struct{}

	// Lines waiting for the cloud, each followed by '\n'. Offsets are absolute
	// positions in the logger's cloud stream: cloudBuf[0] is at cloudHead and
	// cloudEnds[i] is where line i ends (after its '\n').
	cloudBuf       []byte
	cloudEnds      []uint64
	cloudHead      uint64
	cloudRetry     cloudRetry // batch to resend after a network error
	cloudBufWarned bool

	fileMu      sync.Mutex // serializes disk writes; taken while holding mu to keep their order
	file        *os.File
	fileChecked time.Time // last rotation check
}

// NewLogger creates a logger writing to filename (".log" is appended if missing;
// an empty name means "app.log").
func NewLogger(filename string, opts ...Option) (*Logger, error) {
	cfg := defaultConfig()
	for _, opt := range opts {
		opt(&cfg)
	}
	return newLogger("Logger", filename, cfg)
}

func newLogger(kind, filename string, cfg config) (*Logger, error) {
	if filename == "" {
		filename = "app.log"
	}
	if !strings.HasSuffix(filename, ".log") {
		filename += ".log"
	}
	if cfg.filemode != "a" && cfg.filemode != "w" {
		return nil, fmt.Errorf("justlog3: invalid filemode %q, use \"a\" or \"w\"", cfg.filemode)
	}
	if cfg.cycles < 1 || cfg.cloudCycles < 1 {
		return nil, errors.New("justlog3: cycles and cloud cycles must be >= 1")
	}
	if cfg.filemode == "w" {
		f, err := os.Create(filename)
		if err != nil {
			return nil, fmt.Errorf("justlog3: truncate %s: %w", filename, err)
		}
		f.Close()
	}
	l := &Logger{
		kind:        kind,
		filename:    filename,
		withTime:    cfg.withTime,
		filemode:    cfg.filemode,
		cycles:      cfg.cycles,
		cloudCycles: cfg.cloudCycles,
		writeCh:     make(chan struct{}, 1),
	}
	go l.writer()
	registerLogger(l)
	return l, nil
}

// Log writes one line. Without options it is grey with the GREY prefix.
func (l *Logger) Log(message string, opts ...LogOption) {
	var o logOpts
	for _, opt := range opts {
		o.red = o.red || opt.o.red
		o.green = o.green || opt.o.green
		o.offConsole = o.offConsole || opt.o.offConsole
		if opt.o.prefix != "" {
			o.prefix = opt.o.prefix
		}
	}
	l.log(message, o)
}

func (l *Logger) log(message string, o logOpts) {
	var prefix, color string
	switch {
	case o.red:
		prefix, color = "RED", colorRed
	case o.green:
		prefix, color = "GREEN", colorGreen
	default:
		prefix, color = "GREY", colorGrey
	}
	if o.prefix != "" {
		prefix = o.prefix
	}
	var tsArr [32]byte
	var ts []byte
	if l.withTime {
		ts = appendTimestamp(tsArr[:0])
	}

	if !o.offConsole {
		sp := scratchPool.Get().(*[]byte)
		s := append((*sp)[:0], color...)
		s = appendLine(s, prefix, ts, message)
		s = append(s, colorReset+"\n"...)
		os.Stdout.Write(s)
		if cap(s) <= maxReusedScratch {
			*sp = s
			scratchPool.Put(sp)
		}
	}

	c := cloud.Load()
	var out []byte
	signal, wake := false, false
	l.mu.Lock()
	start := len(l.buf)
	l.buf = appendLine(l.buf, prefix, ts, message)
	l.buf = append(l.buf, '\n')
	l.bufLines++
	// Copy the line to the cloud first: the backpressure branch below may
	// swap l.buf for an empty buffer, and l.buf[start:] would then panic.
	if c != nil && !c.disabled.Load() {
		l.cloudBuf = append(l.cloudBuf, l.buf[start:]...)
		l.cloudEnds = append(l.cloudEnds, l.cloudHead+uint64(len(l.cloudBuf)))
		if len(l.cloudEnds) > CloudBufMax {
			l.dropCloudLocked(len(l.cloudEnds) - CloudBufMax)
		}
		wake = len(l.cloudEnds) >= l.cloudCycles
	}
	if l.bufLines >= l.cycles {
		switch {
		case !l.writeQueued:
			l.writeQueued = true
			signal = true
		case len(l.buf) >= maxPendingBytes:
			out = l.takeLocked()
		}
	}
	l.mu.Unlock()

	if signal {
		select {
		case l.writeCh <- struct{}{}:
		default:
		}
	}
	if out != nil {
		l.writeOut(out)
	}
	if wake {
		c.wake()
	}
}

// appendLine appends "<prefix>:<ts> - <message>", or "<prefix>: <message>" when ts is nil.
func appendLine(dst []byte, prefix string, ts []byte, message string) []byte {
	dst = append(dst, prefix...)
	dst = append(dst, ':')
	if ts != nil {
		dst = append(dst, ts...)
		dst = append(dst, " -"...)
	}
	dst = append(dst, ' ')
	return append(dst, message...)
}

// writer writes the disk buffer in the background. Whatever has piled up
// since the last write goes out in one syscall.
func (l *Logger) writer() {
	for range l.writeCh {
		l.mu.Lock()
		l.writeQueued = false
		if len(l.buf) == 0 {
			l.mu.Unlock()
			continue
		}
		out := l.takeLocked()
		l.mu.Unlock()
		l.writeOut(out)
	}
}

// dropCloudLocked removes the k oldest lines from the cloud buffer. Bytes are
// only resliced, never overwritten, so a batch being sent stays intact.
func (l *Logger) dropCloudLocked(k int) {
	newHead := l.cloudEnds[k-1]
	l.cloudBuf = l.cloudBuf[newHead-l.cloudHead:]
	l.cloudEnds = l.cloudEnds[k:]
	l.cloudHead = newHead
	if !l.cloudBufWarned {
		l.cloudBufWarned = true
		printErr(fmt.Sprintf("Cloud buffer for '%s' overflowed (>%d), oldest lines dropped.", l.filename, CloudBufMax))
	}
}

type tsCache struct {
	sec  int64
	text []byte // "2006-01-02 15:04:05."
}

var tsCur atomic.Pointer[tsCache]

// appendTimestamp appends the local time as "2006-01-02 15:04:05.000".
// The part up to the seconds is formatted once per second and shared.
func appendTimestamp(dst []byte) []byte {
	now := time.Now()
	sec := now.Unix()
	c := tsCur.Load()
	if c == nil || c.sec != sec {
		c = &tsCache{sec: sec, text: now.AppendFormat(make([]byte, 0, 24), "2006-01-02 15:04:05.")}
		tsCur.Store(c)
	}
	ms := now.Nanosecond() / int(time.Millisecond)
	dst = append(dst, c.text...)
	return append(dst, byte('0'+ms/100), byte('0'+ms/10%10), byte('0'+ms%10))
}

// takeLocked detaches the disk buffer and locks fileMu before mu is released,
// so buffers reach the disk in the order they were taken.
func (l *Logger) takeLocked() []byte {
	out := l.buf
	l.buf = l.spare[:0]
	l.spare = nil
	l.bufLines = 0
	l.fileMu.Lock()
	return out
}

// writeOut writes a buffer taken by takeLocked and unlocks fileMu.
func (l *Logger) writeOut(out []byte) {
	var err error
	if len(out) > 0 {
		err = l.writeFileLocked(out)
	}
	l.fileMu.Unlock()
	if err != nil {
		printErr(fmt.Sprintf(": %v\nFailed to write logs to disk, most likely because it is blocked/full\nError time: %s",
			err, time.Now().Format("2006-01-02 15:04:05.000")))
		fmt.Printf("Unwritten logs: %q\n", out)
	}
	if cap(out) == 0 || cap(out) > maxReusedBuf {
		return
	}
	l.mu.Lock()
	if l.spare == nil {
		l.spare = out[:0]
	}
	l.mu.Unlock()
}

func (l *Logger) writeFileLocked(p []byte) error {
	if l.file != nil && time.Since(l.fileChecked) >= rotationCheck {
		l.fileChecked = time.Now()
		if !l.fileStillAtPath() {
			l.file.Close()
			l.file = nil
		}
	}
	if l.file == nil {
		f, err := openAppend(l.filename)
		if err != nil {
			return err
		}
		l.file = f
		l.fileChecked = time.Now()
	}
	if _, err := l.file.Write(p); err != nil {
		l.file.Close()
		l.file = nil // reopen on the next write
		return err
	}
	return nil
}

// fileStillAtPath reports whether the open file is still the one at l.filename,
// that is, it has not been renamed or deleted by log rotation.
func (l *Logger) fileStillAtPath() bool {
	open, err := l.file.Stat()
	if err != nil {
		return false
	}
	cur, err := os.Stat(l.filename)
	return err == nil && os.SameFile(open, cur)
}

// Flush writes the buffered lines to disk and returns once they, and any
// write already in progress, are on disk.
func (l *Logger) Flush() {
	l.mu.Lock()
	out := l.takeLocked() // even when empty: waits for a write in progress
	l.mu.Unlock()
	l.writeOut(out)
}

// Close flushes the logger and closes its file. The logger stays usable:
// the next write opens the file again.
func (l *Logger) Close() error {
	l.Flush()
	l.fileMu.Lock()
	defer l.fileMu.Unlock()
	if l.file == nil {
		return nil
	}
	err := l.file.Close()
	l.file = nil
	return err
}

// Status describes a logger's configuration.
type Status struct {
	LoggerName   string
	Filename     string
	WithTime     bool
	Filemode     string
	Cycles       int
	CloudCycles  int
	ConsoleLevel string // only set for BasicLogger
	OS           string
}

// Status returns the logger's configuration.
func (l *Logger) Status() Status {
	return Status{
		LoggerName:  l.kind,
		Filename:    l.filename,
		WithTime:    l.withTime,
		Filemode:    l.filemode,
		Cycles:      l.cycles,
		CloudCycles: l.cloudCycles,
		OS:          runtime.GOOS,
	}
}

// StatusText returns Status as human-readable text.
func (l *Logger) StatusText() string {
	return statusText(l.Status())
}

func statusText(s Status) string {
	timeLine := "Logger logs without time"
	if s.WithTime {
		timeLine = "Logger logs with time"
	}
	text := fmt.Sprintf("%s\nFilename: %s\n%s\nFilemode: %s\nCycles: %d\nCloud cycles: %d\nOS: %s",
		s.LoggerName, s.Filename, timeLine, s.Filemode, s.Cycles, s.CloudCycles, s.OS)
	if s.ConsoleLevel != "" {
		text += "\nConsole level: " + s.ConsoleLevel
	}
	return text
}
