package justlog3

import (
	"fmt"
	"os"
	"sync"
)

const (
	// DefaultURL is the JustLog3 Cloud endpoint used when SetAPIToken gets no WithURL option.
	DefaultURL = "https://jl3-cloud.site/api/logs/"
	// CloudBufMax is the maximum number of lines kept per logger while waiting to be sent.
	CloudBufMax = 10_000
)

const (
	colorRed   = "\033[31m"
	colorGreen = "\033[92m"
	colorGrey  = "\033[90m"
	colorReset = "\033[0m"
)

var (
	registryMu sync.Mutex
	allLoggers []*Logger
)

func registerLogger(l *Logger) {
	registryMu.Lock()
	allLoggers = append(allLoggers, l)
	registryMu.Unlock()
}

func loggersSnapshot() []*Logger {
	registryMu.Lock()
	defer registryMu.Unlock()
	return append([]*Logger(nil), allLoggers...)
}

func printErr(msg string) {
	os.Stdout.WriteString(colorRed + "[JustLog3Error] " + msg + colorReset + "\n")
}

// Shutdown flushes every logger to disk and closes its file, sends what is left
// in the cloud buffers and stops the cloud sender. Call it before the process
// exits, typically as `defer justlog3.Shutdown()` in main.
func Shutdown() {
	for _, l := range loggersSnapshot() {
		if err := l.Close(); err != nil {
			printErr(fmt.Sprintf("Closing '%s': %v", l.filename, err))
		}
	}
	cloudMu.Lock()
	defer cloudMu.Unlock()
	if c := cloud.Swap(nil); c != nil {
		c.shutdown()
	}
}
