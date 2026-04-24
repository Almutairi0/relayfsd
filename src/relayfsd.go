package main

import (
	"fmt"
	"log"
	"os"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--config" {
		runConfigWizard()
		return
	}

	initLogger()
	loadConfig()

	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Printf(" Relayfsd started\n")
	fmt.Printf("   Watching : %s (%s)\n", watchTarget(), cfg.WatchSide)
	fmt.Printf("   Sending  : %s (%s)\n", destTarget(), cfg.DestSide)
	fmt.Printf("   Remote   : %s@%s:%s\n", cfg.Username, cfg.IP, cfg.RemoteDir)
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")

	var err error
	switch cfg.WatchSide {
	case "remote":
		err = startRemoteWatcher()
	default:
		err = startLocalWatcher()
	}

	if err != nil {
		logger.Fatalf("FATAL | Watcher failed: %v", err)
	}

	logger.Println("INFO | Done")
}

func watchTarget() string {
	if cfg.WatchSide == "remote" {
		return cfg.RemoteDir
	}
	return cfg.WatchPath
}

func destTarget() string {
	if cfg.DestSide == "local" {
		return cfg.WatchPath
	}
	return cfg.RemoteDir
}

func initLogger() {
	f, err := os.OpenFile("relayfsd.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Fatal(err)
	}
	logger = log.New(f, "", log.LstdFlags)
}

