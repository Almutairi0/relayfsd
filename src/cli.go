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
	currentAuth := defaultAuthMethod(existing)
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
		cfg.KeyPassphrase = promptPassword(reader, "Key passphrase (leave empty if none)", existing.KeyPassphrase)
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
		cfg.Password = promptPassword(reader, "SSH password", existing.Password)
	}

	fmt.Println()
	fmt.Println("Which side do you want to watch for new files?")
	fmt.Println("  1) local  - watch your local folder (default)")
	fmt.Println("  2) remote - watch the remote server folder")
	currentWatch := existing.WatchSide
	if currentWatch != "local" && currentWatch != "remote" {
		currentWatch = "local"
	}
	fmt.Printf("Choice [%s]: ", currentWatch)
	cfg.WatchSide = readSide(reader, "local", "remote", currentWatch)

	fmt.Println()
	fmt.Println("Where should new files be sent?")
	fmt.Println("  1) remote - upload to remote server")
	fmt.Println("  2) local  - download to local folder")
	// Enter keeps the current choice; if that's no longer possible, it
	// picks the side that isn't watched.
	currentDest := existing.DestSide
	if (currentDest != "local" && currentDest != "remote") || currentDest == cfg.WatchSide {
		currentDest = defaultDest(cfg.WatchSide)
	}
	fmt.Printf("Choice [%s]: ", currentDest)
	cfg.DestSide = readSide(reader, "remote", "local", currentDest)
	if cfg.DestSide == cfg.WatchSide {
		other := map[string]string{"local": "remote", "remote": "local"}[cfg.WatchSide]
		fmt.Printf("⚠️  Watching %s and sending to %s isn't supported; files will be sent to %s.\n",
			cfg.WatchSide, cfg.DestSide, other)
		cfg.DestSide = other
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

// readSide reads a "1) first / 2) second" answer. Enter, or anything it
// doesn't understand, keeps current.
func readSide(reader *bufio.Reader, first, second, current string) string {
	input, _ := reader.ReadString('\n')
	switch strings.TrimSpace(input) {
	case "1", first:
		return first
	case "2", second:
		return second
	}
	return current
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
// When stdin isn't a terminal (input is piped), it reads the next line from
// reader like the other prompts, since reader may already hold that line.
func promptPassword(reader *bufio.Reader, label string, current string) string {
	if current != "" {
		fmt.Printf("%s [current: ****]: ", label)
	} else {
		fmt.Printf("%s: ", label)
	}

	var input string
	if term.IsTerminal(int(syscall.Stdin)) {
		raw, err := term.ReadPassword(int(syscall.Stdin))
		fmt.Println() // move to next line after hidden input
		if err == nil {
			input = string(raw)
		}
	} else {
		input, _ = reader.ReadString('\n')
	}

	input = strings.TrimSpace(input)
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

	report := hostKeyReportFor(status, key)
	if report.Title != "" {
		fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
		fmt.Println(report.Title)
		fmt.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	}
	for _, line := range report.Lines {
		fmt.Println(line)
	}
	if !report.CanTrust {
		return
	}

	fmt.Println()
	accepted := []string{"y", "yes"}
	if report.NeedYes {
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

// defaultAuthMethod picks the auth method to offer for an existing config:
// its own setting, else "password" for configs from before key support,
// else "key".
func defaultAuthMethod(existing Config) string {
	switch {
	case existing.AuthMethod != "":
		return existing.AuthMethod
	case existing.Password != "":
		return "password"
	default:
		return "key"
	}
}

// hostKeyReport is what to tell the user about the server's host key.
// The --config wizard prints it and the terminal UI renders it.
type hostKeyReport struct {
	Title    string   // banner for warnings, empty otherwise
	Lines    []string // body; empty strings are blank lines
	CanTrust bool     // the user may choose to trust the key
	NeedYes  bool     // trusting requires typing "yes", not just "y"
	Danger   bool     // the key changed or was revoked
}

func hostKeyReportFor(status hostKeyStatus, key ssh.PublicKey) hostKeyReport {
	var r hostKeyReport
	switch status {
	case hostKeyTrusted:
		r.Lines = []string{fmt.Sprintf("✅ Server key already trusted (%s %s)", key.Type(), ssh.FingerprintSHA256(key))}

	case hostKeyMismatch, hostKeyNoCommon, hostKeyRevoked:
		r.Title = "🚨 WARNING: THE SERVER'S IDENTITY HAS CHANGED"
		r.Danger = true
		switch status {
		case hostKeyMismatch:
			r.Lines = append(r.Lines,
				"The server now presents:",
				fmt.Sprintf("  %s  %s", key.Type(), ssh.FingerprintSHA256(key)),
				fmt.Sprintf("but %s has a different key for %s.", knownHostsPath(), cfg.IP))
		case hostKeyRevoked:
			r.Lines = append(r.Lines,
				"The server presents:",
				fmt.Sprintf("  %s  %s", key.Type(), ssh.FingerprintSHA256(key)),
				fmt.Sprintf("which is marked @revoked in %s.", knownHostsPath()))
		case hostKeyNoCommon:
			r.Lines = append(r.Lines,
				fmt.Sprintf("The server offers none of the key types %s has for %s.", knownHostsPath(), cfg.IP))
		}
		r.Lines = append(r.Lines,
			"",
			"Someone may be intercepting the connection (man-in-the-middle).",
			"It is also expected if you reinstalled the server or its SSH keys.",
			"If you're sure the change is legitimate, remove the old key with:",
			"  "+removeCommand(),
			"and run --config again. Relayfsd will not connect until then.")

	case hostKeyUnknown, hostKeyUnverifiable:
		if status == hostKeyUnverifiable {
			r.Title = "⚠️  THIS SERVER IS ALREADY LISTED, BUT RELAYFSD CAN'T CHECK IT"
			r.NeedYes = true
			r.Lines = append(r.Lines,
				fmt.Sprintf("%s lists %s only through @cert-authority or a key", knownHostsPath(), cfg.IP),
				"Relayfsd can't read, so it can't tell whether the key below is the",
				"one ssh trusts. Only accept it after comparing the fingerprint on the",
				"server itself; if it doesn't match, someone may be intercepting the connection.",
				"")
		}
		r.CanTrust = true
		r.Lines = append(r.Lines,
			"The server presented this host key:",
			fmt.Sprintf("  %s  %s", key.Type(), ssh.FingerprintSHA256(key)),
			"",
			"To verify it, run this on the server itself (not over the network)",
			"and check that the fingerprint matches:",
			fmt.Sprintf("  ssh-keygen -lf /etc/ssh/ssh_host_%s_key.pub", hostKeyFileName(key.Type())))
	}
	return r
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
