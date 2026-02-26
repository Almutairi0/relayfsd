package main

import (
	"fmt"
	"log"
	"os"
)

func main() {
	// Check for --config flag before anything else
	if len(os.Args) > 1 && os.Args[1] == "--config" {
		runConfigWizard()
		return
	}

	initLogger()
	loadConfig()

	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Printf(" Relayfsd started\n")
	fmt.Printf("   Watching : %s\n", cfg.WatchPath)
	fmt.Printf("   Remote   : %s@%s:%s\n", cfg.Username, cfg.IP, cfg.RemoteDir)
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")

	if err := startWatcher(); err != nil {
		logger.Fatalf("FATAL | Watcher failed: %v", err)
	}

	logger.Println("INFO | Done")
}

func initLogger() {
	f, err := os.OpenFile("relayfsd.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Fatal(err)
	}
	logger = log.New(f, "", log.LstdFlags)
}
