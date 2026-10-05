package main

import (
	"log"
	"os"

	justlog3 "github.com/OAzizjon/justlog3/go"
)

func main() {
	defer justlog3.Shutdown()

	if token := os.Getenv("JUSTLOG3_TOKEN"); token != "" {
		if err := justlog3.SetAPIToken(token); err != nil {
			log.Fatal(err)
		}
	}

	logger, err := justlog3.NewLogger("app.log")
	if err != nil {
		log.Fatal(err)
	}
	logger.Log("Hi...")
	logger.Log("debug", justlog3.OffConsole())
	logger.Log("There is some error!", justlog3.Red())
	logger.Log("OK", justlog3.Green())
	logger.Log("There is critical error!", justlog3.Red(), justlog3.Prefix("CRITICAL!!"))

	basic, err := justlog3.NewBasicLogger("basic.log", justlog3.WithConsoleLevel("INFO"))
	if err != nil {
		log.Fatal(err)
	}
	basic.Debug("hidden from console, still in the file")
	basic.Info("service started")
	basic.Successf("processed %d items", 42)
	basic.Warning("disk is 90% full")
}
