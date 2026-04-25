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

	fmt.Println()
	fmt.Println("Which side do you want to watch for new files?")
	fmt.Println("  1) local  - watch your local folder (default)")
	fmt.Println("  2) remote - watch the remote server folder")
	fmt.Print("Choice [")
	if existing.WatchSide != "" {
		fmt.Print(existing.WatchSide)
	} else {
		fmt.Print("local")
	}
	fmt.Print("]: ")

	watchInput, _ := reader.ReadString('\n')
	watchInput = strings.TrimSpace(watchInput)
	switch watchInput {
	case "2", "remote":
		cfg.WatchSide = "remote"
	case "1", "local", "":
		cfg.WatchSide = "local"
	default:
		if existing.WatchSide != "" {
			cfg.WatchSide = existing.WatchSide
		} else {
			cfg.WatchSide = "local"
		}
	}

	fmt.Println()
	fmt.Println("Where should new files be sent?")
	fmt.Println("  1) remote - upload to remote server (default)")
	fmt.Println("  2) local  - download to local folder")
	fmt.Print("Choice [")
	if existing.DestSide != "" {
		fmt.Print(existing.DestSide)
	} else {
		fmt.Print("remote")
	}
	fmt.Print("]: ")

	destInput, _ := reader.ReadString('\n')
	destInput = strings.TrimSpace(destInput)
	switch destInput {
	case "2", "local":
		cfg.DestSide = "local"
	case "1", "remote", "":
		cfg.DestSide = "remote"
	default:
		if existing.DestSide != "" {
			cfg.DestSide = existing.DestSide
		} else {
			cfg.DestSide = "remote"
		}
	}
	fmt.Println()
	if cfg.WatchSide == "local" {
		cfg.WatchPath = promptString(reader, "Local folder to watch (e.g. /home/user/torrents)", existing.WatchPath)
		cfg.RemoteDir = promptString(reader, "Remote directory to send files to (e.g. /home/user/downloads)", existing.RemoteDir)
	} else {
		cfg.RemoteDir = promptString(reader, "Remote directory to watch (e.g. /home/user/downloads)", existing.RemoteDir)
		cfg.WatchPath = promptString(reader, "Local folder to save files to (e.g. /home/user/downloads)", existing.WatchPath)
	}
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
	fmt.Println("  ./relayfsd")
	fmt.Println()
	fmt.Println("To update your config at any time, run:")
	fmt.Println("  ./relayfsd --config")
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
