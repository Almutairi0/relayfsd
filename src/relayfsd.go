package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "--config":
			runConfigWizard()
			return
		case "--ui", "ui":
			runTUI()
			return
		case "--help", "-h", "help":
			printUsage(os.Stdout)
			return
		default:
			// Don't start watching on a typo like --UI.
			fmt.Fprintf(os.Stderr, "relayfsd: unknown option %q\n\n", os.Args[1])
			printUsage(os.Stderr)
			os.Exit(2)
		}
	}

	initLogger()
	logStopSignals()
	loadConfig()
	if err := checkDirection(); err != nil {
		logger.Printf("FATAL | Bad direction: %v", err)
		fmt.Println("❌", err)
		os.Exit(1)
	}
	if err := setupSSHAuth(); err != nil {
		logger.Printf("FATAL | SSH auth setup failed: %v", err)
		fmt.Println("❌", err)
		os.Exit(1)
	}
	// Fail fast if the server was never trusted. This only reads
	// known_hosts; the key itself is checked on every connection.
	if _, _, err := hostKeyConfig(); err != nil {
		logger.Printf("FATAL | Host key check failed: %v", err)
		fmt.Println("❌", err)
		os.Exit(1)
	}
	if err := setupAfterUpload(); err != nil {
		logger.Printf("FATAL | after_upload setup failed: %v", err)
		fmt.Println("❌", err)
		os.Exit(1)
	}

	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Printf(" Relayfsd started\n")
	fmt.Printf("   Watching : %s (%s)\n", watchTarget(), cfg.WatchSide)
	fmt.Printf("   Sending  : %s (%s)\n", destTarget(), cfg.DestSide)
	fmt.Printf("   Remote   : %s@%s:%s\n", cfg.Username, cfg.IP, cfg.RemoteDir)
	fmt.Printf("   Auth     : %s\n", authMethod())
	fmt.Printf("   Hosts    : %s\n", knownHostsPath())
	if afterUploadApplies() && afterUploadAction() != afterUploadKeep {
		fmt.Printf("   After    : %s %s\n", afterUploadAction(), cfg.MoveDir)
	}
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

// logStopSignals logs when the daemon is stopped by Ctrl+C or SIGTERM
// (systemctl stop/restart), so the log, and the UI reading it, show that
// it stopped. Unfinished downloads are cleaned up on the next start.
func logStopSignals() {
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	go func() {
		sig := <-sigs
		logger.Printf("INFO | Stopped (%v)", sig)
		os.Exit(0)
	}()
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: relayfsd [--config | --ui | --help]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  (no option)  watch for new files and transfer them")
	fmt.Fprintln(w, "  --config     set up or change the configuration (line by line)")
	fmt.Fprintln(w, "  --ui, ui     terminal UI: live status and config editor")
	fmt.Fprintln(w, "  --help, -h   show this help")
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
	// 0600: the log records local and remote paths, filenames and the
	// server address, so it isn't left readable by other users.
	f, err := os.OpenFile("relayfsd.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		log.Fatal(err)
	}
	logger = log.New(f, "", log.LstdFlags)
}
