package justlog3_test

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"

	justlog3 "github.com/OAzizjon/justlog3/go"
)

// ExampleNewLogger demonstrates creating a logger and writing colored file-only entries.
func ExampleNewLogger() {
	tempDir, err := os.MkdirTemp("", "justlog3-example-")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(tempDir)

	path := filepath.Join(tempDir, "logger")
	logger, err := justlog3.NewLogger(path, justlog3.WithTime(false))
	if err != nil {
		panic(err)
	}

	logger.Log("Hi...", justlog3.OffConsole())
	logger.Log("There is some error!", justlog3.Red(), justlog3.OffConsole())
	justlog3.Shutdown()

	b, err := os.ReadFile(path + ".log")
	if err != nil {
		panic(err)
	}
	fmt.Print(string(b))

	// Output:
	// GREY: Hi...
	// RED: There is some error!
}

// ExampleLogger_Log shows colored log lines with custom prefixes.
func ExampleLogger_Log() {
	tempDir, err := os.MkdirTemp("", "justlog3-example-")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(tempDir)

	path := filepath.Join(tempDir, "log")
	logger, err := justlog3.NewLogger(path, justlog3.WithTime(false))
	if err != nil {
		panic(err)
	}

	logger.Log("OK", justlog3.Green(), justlog3.OffConsole())
	logger.Log("disk is full", justlog3.Red(), justlog3.Prefix("CRITICAL!!"), justlog3.OffConsole())
	justlog3.Shutdown()

	b, err := os.ReadFile(path + ".log")
	if err != nil {
		panic(err)
	}
	fmt.Print(string(b))

	// Output:
	// GREEN: OK
	// CRITICAL!!: disk is full
}

// ExampleNewBasicLogger demonstrates a leveled logger that still writes all entries to disk.
func ExampleNewBasicLogger() {
	tempDir, err := os.MkdirTemp("", "justlog3-example-")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(tempDir)

	path := filepath.Join(tempDir, "basic")
	logger, err := justlog3.NewBasicLogger(path, justlog3.WithTime(false), justlog3.WithConsoleLevel("CRITICAL"))
	if err != nil {
		panic(err)
	}

	logger.Info("service started")
	logger.Warningf("disk is %d%% full", 90)
	logger.Debug("cache hit")
	justlog3.Shutdown()

	b, err := os.ReadFile(path + ".log")
	if err != nil {
		panic(err)
	}
	fmt.Print(string(b))

	// Output:
	// INFO: service started
	// WARNING: disk is 90% full
	// DEBUG: cache hit
}

// ExampleSetAPIToken demonstrates sending queued logs to the cloud and reading the request body.
func ExampleSetAPIToken() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			panic(err)
		}
		fmt.Println("received: " + string(body))
		w.WriteHeader(http.StatusAccepted)
	}))

	if err := justlog3.SetAPIToken("YOUR_TOKEN", justlog3.WithURL(srv.URL)); err != nil {
		panic(err)
	}

	tempDir, err := os.MkdirTemp("", "justlog3-example-")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(tempDir)

	path := filepath.Join(tempDir, "cloud")
	logger, err := justlog3.NewLogger(path, justlog3.WithTime(false))
	if err != nil {
		panic(err)
	}

	logger.Log("first", justlog3.OffConsole())
	logger.Log("second", justlog3.OffConsole())
	justlog3.Shutdown()
	srv.Close()

	// Output:
	// [JustLog3] Cloud: log sending enabled.
	// received: GREY: first
	// GREY: second
}
