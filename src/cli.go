package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"syscall"

	"golang.org/x/term"
)

func runConfigWizard() {
	reader := bufio.NewReader(os.Stdin)

	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Println("        Relayfsd Configuration")
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Println("Press Enter to keep your current value.")
	fmt.Println()

	// Load existing config if it exists so we can show current values
	existing := loadConfigSilent()

	cfg.IP = promptString(reader, "Remote server IP", existing.IP)
	cfg.Username = promptString(reader, "SSH username", existing.Username)
	cfg.Password = promptPassword("SSH password", existing.Password)
	cfg.RemoteDir = promptString(reader, "Remote directory (e.g. /home/user/downloads)", existing.RemoteDir)
	cfg.WatchPath = promptString(reader, "Local folder to watch (e.g. /home/user/torrents)", existing.WatchPath)

	fmt.Println()
	fmt.Print("Enable Discord notifications? (y/n) [")
	if existing.Notifications.Discord.Enabled {
		fmt.Print("y")
	} else {
		fmt.Print("n")
	}
	fmt.Print("]: ")

	discordInput, _ := reader.ReadString('\n')
	discordInput = strings.TrimSpace(discordInput)

	switch discordInput {
	case "y", "Y":
		cfg.Notifications.Discord.Enabled = true
	case "n", "N":
		cfg.Notifications.Discord.Enabled = false
	default:
		// Keep existing
		cfg.Notifications.Discord.Enabled = existing.Notifications.Discord.Enabled
	}

	if cfg.Notifications.Discord.Enabled {
		cfg.Notifications.Discord.WebhookURL = promptString(
			reader,
			"Discord webhook URL",
			existing.Notifications.Discord.WebhookURL,
		)
	}

	if err := saveConfig(); err != nil {
		fmt.Printf("\n❌ Failed to save config: %v\n", err)
		os.Exit(1)
	}

	fmt.Println()
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Println("✅ Configuration saved to data.json")
	fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	fmt.Println()
	fmt.Println("You can now run the program normally:")
	fmt.Println("  ./torrentsync")
	fmt.Println()
	fmt.Println("To update your config at any time, run:")
	fmt.Println("  ./torrentsync --config")
	fmt.Println()
}

// promptString shows a prompt with the current value and reads input.
// If the user just presses Enter, the current value is kept.
func promptString(reader *bufio.Reader, label string, current string) string {
	if current != "" {
		fmt.Printf("%s [%s]: ", label, current)
	} else {
		fmt.Printf("%s: ", label)
	}

	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(input)

	if input == "" {
		return current
	}
	return input
}

// promptPassword reads a password without echoing it to the terminal.
func promptPassword(label string, current string) string {
	if current != "" {
		fmt.Printf("%s [current: ****]: ", label)
	} else {
		fmt.Printf("%s: ", label)
	}

	// Read password without echo
	raw, err := term.ReadPassword(int(syscall.Stdin))
	fmt.Println() // move to next line after hidden input
	if err != nil {
		// Fallback: read normally if terminal doesn't support it
		reader := bufio.NewReader(os.Stdin)
		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(input)
		if input == "" {
			return current
		}
		return input
	}

	input := strings.TrimSpace(string(raw))
	if input == "" {
		return current
	}
	return input
}
