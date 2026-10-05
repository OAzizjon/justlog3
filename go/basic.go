package justlog3

import (
	"fmt"
	"strings"
)

var levels = map[string]int{
	"NOTSET":   0,
	"DEBUG":    1,
	"INFO":     2,
	"WARNING":  3,
	"ERROR":    4,
	"SUCCESS":  4,
	"CRITICAL": 5,
}

const (
	levelDebug    = 1
	levelInfo     = 2
	levelWarning  = 3
	levelError    = 4
	levelCritical = 5
)

// BasicLogger is a Logger with levels: Debug, Info, Warning, Error, Success
// and Critical. Each level has its own prefix and color; the console level
// only decides what is printed, every line still goes to the file and the cloud.
type BasicLogger struct {
	*Logger
	consoleLevel string
	minLevel     int
}

// NewBasicLogger creates a leveled logger. Use WithConsoleLevel to hide
// low-level lines from the console; they still go to the file and the cloud.
func NewBasicLogger(filename string, opts ...Option) (*BasicLogger, error) {
	cfg := defaultConfig()
	for _, opt := range opts {
		opt(&cfg)
	}
	level := strings.ToUpper(cfg.consoleLevel)
	minLevel, ok := levels[level]
	if !ok {
		return nil, fmt.Errorf("justlog3: invalid console level %q, valid options are: NOTSET, DEBUG, INFO, WARNING, ERROR / SUCCESS, CRITICAL", cfg.consoleLevel)
	}
	l, err := newLogger("BasicLogger", filename, cfg)
	if err != nil {
		return nil, err
	}
	return &BasicLogger{Logger: l, consoleLevel: level, minLevel: minLevel}, nil
}

// Debug logs a grey line with the DEBUG prefix.
func (b *BasicLogger) Debug(message string) {
	b.log(message, logOpts{prefix: "DEBUG", offConsole: b.minLevel > levelDebug})
}

// Info logs a green line with the INFO prefix.
func (b *BasicLogger) Info(message string) {
	b.log(message, logOpts{green: true, prefix: "INFO", offConsole: b.minLevel > levelInfo})
}

// Warning logs a red line with the WARNING prefix.
func (b *BasicLogger) Warning(message string) {
	b.log(message, logOpts{red: true, prefix: "WARNING", offConsole: b.minLevel > levelWarning})
}

// Error logs a red line with the ERROR prefix.
func (b *BasicLogger) Error(message string) {
	b.log(message, logOpts{red: true, prefix: "ERROR", offConsole: b.minLevel > levelError})
}

// Success logs a green line with the SUCCESS prefix. It has the same level as Error.
func (b *BasicLogger) Success(message string) {
	b.log(message, logOpts{green: true, prefix: "SUCCESS", offConsole: b.minLevel > levelError})
}

// Critical logs a red line with the CRITICAL prefix.
func (b *BasicLogger) Critical(message string) {
	b.log(message, logOpts{red: true, prefix: "CRITICAL", offConsole: b.minLevel > levelCritical})
}

// Debugf is like Debug with fmt.Sprintf formatting.
func (b *BasicLogger) Debugf(format string, args ...any) { b.Debug(fmt.Sprintf(format, args...)) }

// Infof is like Info with fmt.Sprintf formatting.
func (b *BasicLogger) Infof(format string, args ...any) { b.Info(fmt.Sprintf(format, args...)) }

// Warningf is like Warning with fmt.Sprintf formatting.
func (b *BasicLogger) Warningf(format string, args ...any) { b.Warning(fmt.Sprintf(format, args...)) }

// Errorf is like Error with fmt.Sprintf formatting.
func (b *BasicLogger) Errorf(format string, args ...any) { b.Error(fmt.Sprintf(format, args...)) }

// Successf is like Success with fmt.Sprintf formatting.
func (b *BasicLogger) Successf(format string, args ...any) { b.Success(fmt.Sprintf(format, args...)) }

// Criticalf is like Critical with fmt.Sprintf formatting.
func (b *BasicLogger) Criticalf(format string, args ...any) { b.Critical(fmt.Sprintf(format, args...)) }

// Status returns the logger's configuration, including the console level.
func (b *BasicLogger) Status() Status {
	s := b.Logger.Status()
	s.ConsoleLevel = b.consoleLevel
	return s
}

// StatusText returns Status as human-readable text.
func (b *BasicLogger) StatusText() string {
	return statusText(b.Status())
}
