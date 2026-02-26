package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
)

const configFile = "data.json"

type Config struct {
	IP            string `json:"ip"`
	Username      string `json:"username"`
	Password      string `json:"password"`
	RemoteDir     string `json:"remote_dir"`
	WatchPath     string `json:"watch_path"`
	Notifications struct {
		Discord struct {
			Enabled    bool   `json:"enabled"`
			WebhookURL string `json:"webhook_url"`
		} `json:"discord"`
	} `json:"notifications"`
}

var cfg Config

// loadConfig loads data.json and exits if it fails or is missing.
func loadConfig() {
	f, err := os.Open(configFile)
	if err != nil {
		fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
		fmt.Println("⚠️  No configuration found!")
		fmt.Println()
		fmt.Println("Before using Relayfsd, you need to set")
		fmt.Println("up your server details by running:")
		fmt.Println()
		fmt.Println("  ./relayfsd --config")
		fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
		os.Exit(1)
	}
	defer f.Close()

	if err := json.NewDecoder(f).Decode(&cfg); err != nil {
		log.Fatalf("Failed to parse %s: %v", configFile, err)
	}
}

// loadConfigSilent tries to load config without exiting on failure.
// Used by the config wizard to show existing values.
func loadConfigSilent() Config {
	var existing Config
	f, err := os.Open(configFile)
	if err != nil {
		return existing // return empty config, no existing file
	}
	defer f.Close()
	_ = json.NewDecoder(f).Decode(&existing)
	return existing
}

// saveConfig writes the current cfg to data.json.
func saveConfig() error {
	f, err := os.OpenFile(configFile, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	return enc.Encode(cfg)
}
