package main

import (
	"bufio"
	"fmt"
	"os"
	"slices"
	"strings"
	"syscall"

	"golang.org/x/crypto/ssh"
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

	cfg.IP = promptRequired(reader, "Remote server IP", existing.IP)
	cfg.Username = promptString(reader, "SSH username", existing.Username)
	cfg.KnownHostsPath = existing.KnownHostsPath // only set by editing data.json
	promptHostKey(reader)

	fmt.Println()
	fmt.Println("How should Relayfsd log in to the server?")
	fmt.Println("  1) key      - SSH key pair (recommended)")
	fmt.Println("  2) password - SSH password")
	currentAuth := existing.AuthMethod
	if currentAuth == "" {
		if existing.Password != "" {
			currentAuth = "password" // config from before key support
		} else {
			currentAuth = "key"
		}
	}
	fmt.Printf("Choice [%s]: ", currentAuth)

	authInput, _ := reader.ReadString('\n')
	switch strings.TrimSpace(authInput) {
	case "1", "key":
		cfg.AuthMethod = "key"
	case "2", "password":
		cfg.AuthMethod = "password"
	default:
		cfg.AuthMethod = currentAuth
	}

	if cfg.AuthMethod == "key" {
		defaultKey := existing.KeyPath
		if defaultKey == "" {
			defaultKey = "~/.ssh/id_ed25519"
		}
		cfg.KeyPath = promptString(reader, "Path to private key", defaultKey)
		cfg.KeyPassphrase = promptPassword("Key passphrase (leave empty if none)", existing.KeyPassphrase)
		// Password is intentionally not carried over: switching to key auth
		// removes the stored plaintext password from data.json.

		if _, err := loadPrivateKey(cfg.KeyPath, cfg.KeyPassphrase); err != nil {
			fmt.Printf("⚠️  %v\n", err)
			fmt.Println("   Generate a key and install it on the server with:")
			fmt.Println("     ssh-keygen -t ed25519")
			fmt.Printf("     ssh-copy-id %s@%s\n", cfg.Username, cfg.IP)
			fmt.Println("   The config will still be saved; fix the key before starting Relayfsd.")
		}
	} else {
		cfg.Password = promptPassword("SSH password", existing.Password)
	}

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

	if afterUploadApplies() {
		promptAfterUpload(reader, existing)
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

// promptAfterUpload asks what to do with local files after upload.
// Choosing delete requires typing "yes" so it can't be picked by accident.
func promptAfterUpload(reader *bufio.Reader, existing Config) {
	current := existing.AfterUpload
	if current == "" {
		current = afterUploadKeep
	}

	fmt.Println()
	fmt.Println("What should happen to local files after a successful upload?")
	fmt.Println("  1) keep   - leave them where they are (default)")
	fmt.Println("  2) move   - move them to another folder")
	fmt.Println("  3) delete - delete them to free up space")
	fmt.Printf("Choice [%s]: ", current)

	input, _ := reader.ReadString('\n')
	switch strings.TrimSpace(input) {
	case "1", "keep":
		cfg.AfterUpload = afterUploadKeep
	case "2", "move":
		cfg.AfterUpload = afterUploadMove
	case "3", "delete":
		cfg.AfterUpload = afterUploadDelete
	default:
		cfg.AfterUpload = current
	}

	switch cfg.AfterUpload {
	case afterUploadMove:
		for {
			cfg.MoveDir = promptString(reader, "Folder to move uploaded files to (outside the watch folder)", existing.MoveDir)
			err := validateMoveDir(cfg.MoveDir)
			if err == nil {
				break
			}
			fmt.Printf("⚠️  %v\n", err)
			existing.MoveDir = ""
		}

	case afterUploadDelete:
		fmt.Println()
		fmt.Println("⚠️  Local files will be permanently deleted once their upload is verified.")
		fmt.Println("   Files that change during upload are never deleted.")
		fmt.Print("Type 'yes' to confirm: ")
		confirm, _ := reader.ReadString('\n')
		if strings.TrimSpace(strings.ToLower(confirm)) != "yes" {
			fmt.Println("Not confirmed, keeping files instead.")
			cfg.AfterUpload = afterUploadKeep
		}
	}
}

// promptHostKey fetches the server's host key, shows its fingerprint and
// asks the user to trust it. Nothing is sent to the server but the
// connection attempt itself, so no credentials can leak to an impostor.
func promptHostKey(reader *bufio.Reader) {
	fmt.Println()
	fmt.Printf("Checking the identity of %s...\n", cfg.IP)

	key, status, err := inspectHostKey()
	if err != nil {
		fmt.Printf("⚠️  %v\n", err)
		fmt.Println("   The config will still be saved, but Relayfsd won't connect until")
		fmt.Println("   the server is trusted. Run --config again once it's reachable.")
		return
	}

	switch status {
	case hostKeyTrusted:
		fmt.Printf("✅ Server key already trusted (%s %s)\n", key.Type(), ssh.FingerprintSHA256(key))

	case hostKeyMismatch, hostKeyNoCommon, hostKeyRevoked:
		fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
		fmt.Println("🚨 WARNING: THE SERVER'S IDENTITY HAS CHANGED")
		fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
		switch status {
		case hostKeyMismatch:
			fmt.Printf("The server now presents:\n  %s  %s\n", key.Type(), ssh.FingerprintSHA256(key))
			fmt.Printf("but %s has a different key for %s.\n", knownHostsPath(), cfg.IP)
		case hostKeyRevoked:
			fmt.Printf("The server presents:\n  %s  %s\n", key.Type(), ssh.FingerprintSHA256(key))
			fmt.Printf("which is marked @revoked in %s.\n", knownHostsPath())
		case hostKeyNoCommon:
			fmt.Printf("The server offers none of the key types %s has for %s.\n", knownHostsPath(), cfg.IP)
		}
		fmt.Println()
		fmt.Println("Someone may be intercepting the connection (man-in-the-middle).")
		fmt.Println("It is also expected if you reinstalled the server or its SSH keys.")
		fmt.Println("If you're sure the change is legitimate, remove the old key with:")
		fmt.Printf("  %s\n", removeCommand())
		fmt.Println("and run --config again. Relayfsd will not connect until then.")

	case hostKeyUnknown, hostKeyUnverifiable:
		if status == hostKeyUnverifiable {
			fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
			fmt.Println("⚠️  THIS SERVER IS ALREADY LISTED, BUT RELAYFSD CAN'T CHECK IT")
			fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
			fmt.Printf("%s lists %s only through @cert-authority or a key\n", knownHostsPath(), cfg.IP)
			fmt.Println("Relayfsd can't read, so it can't tell whether the key below is the")
			fmt.Println("one ssh trusts. Only accept it after comparing the fingerprint on the")
			fmt.Println("server itself; if it doesn't match, someone may be intercepting the connection.")
			fmt.Println()
		}
		fmt.Println("The server presented this host key:")
		fmt.Printf("  %s  %s\n", key.Type(), ssh.FingerprintSHA256(key))
		fmt.Println()
		fmt.Println("To verify it, run this on the server itself (not over the network)")
		fmt.Println("and check that the fingerprint matches:")
		fmt.Printf("  ssh-keygen -lf /etc/ssh/ssh_host_%s_key.pub\n", hostKeyFileName(key.Type()))
		fmt.Println()
		accepted := []string{"y", "yes"}
		if status == hostKeyUnverifiable {
			fmt.Print("Type 'yes' to trust this key: ")
			accepted = []string{"yes"}
		} else {
			fmt.Print("Trust this server? (y/N): ")
		}

		answer, _ := reader.ReadString('\n')
		answer = strings.ToLower(strings.TrimSpace(answer))
		if !slices.Contains(accepted, answer) {
			fmt.Println("Not trusted. Relayfsd won't connect until you trust the server.")
			return
		}
		if err := trustHostKey(key); err != nil {
			fmt.Printf("❌ Could not save the key: %v\n", err)
			return
		}
		fmt.Printf("✅ Saved to %s\n", knownHostsPath())
	}
}

// promptRequired is promptString for values that can't be left empty.
func promptRequired(reader *bufio.Reader, label string, current string) string {
	for {
		if current != "" {
			fmt.Printf("%s [%s]: ", label, current)
		} else {
			fmt.Printf("%s: ", label)
		}
		input, err := reader.ReadString('\n')
		input = strings.TrimSpace(input)
		switch {
		case input != "":
			return input
		case current != "":
			return current
		case err != nil: // end of input, asking again won't help
			fmt.Printf("\n❌ %s is required.\n", label)
			os.Exit(1)
		}
		fmt.Printf("⚠️  %s is required.\n", label)
	}
}

// hostKeyFileName maps a key type to the name OpenSSH uses for the
// server's key file, e.g. ssh_host_ed25519_key.pub.
func hostKeyFileName(keyType string) string {
	switch keyType {
	case ssh.KeyAlgoED25519:
		return "ed25519"
	case ssh.KeyAlgoRSA:
		return "rsa"
	case ssh.KeyAlgoECDSA256, ssh.KeyAlgoECDSA384, ssh.KeyAlgoECDSA521:
		return "ecdsa"
	}
	return "*"
}
