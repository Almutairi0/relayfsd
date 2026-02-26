package main

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"time"
)

var logger *log.Logger

func notifyDiscord(message string) {
	d := cfg.Notifications.Discord
	if !d.Enabled {
		return
	}
	if d.WebhookURL == "" {
		logger.Println("WARN | Discord notifications enabled but webhook_url is missing")
		return
	}

	payload, _ := json.Marshal(map[string]string{"content": message})
	client := &http.Client{Timeout: 5 * time.Second}

	resp, err := client.Post(d.WebhookURL, "application/json", bytes.NewReader(payload))
	if err != nil {
		logger.Printf("ERROR | Discord notification error: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 && resp.StatusCode != 204 {
		logger.Printf("ERROR | Discord notification failed: %d", resp.StatusCode)
	}
}
