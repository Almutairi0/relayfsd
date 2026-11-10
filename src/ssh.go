package main

import (
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// sshAuth holds the auth methods built once at startup by setupSSHAuth.
var sshAuth []ssh.AuthMethod

// authMethod returns the configured auth method, defaulting to "password"
// so configs written before key support keep working unchanged.
func authMethod() string {
	if cfg.AuthMethod == "" {
		return "password"
	}
	return cfg.AuthMethod
}

// setupSSHAuth validates the auth config and prepares sshAuth.
// Called at startup so misconfiguration fails fast with a clear message
// instead of surfacing later as a vague connection error.
func setupSSHAuth() error {
	switch authMethod() {
	case "password":
		if cfg.Password == "" {
			return errors.New("auth_method is \"password\" but no password is set (run --config)")
		}
		sshAuth = []ssh.AuthMethod{ssh.Password(cfg.Password)}
		return nil

	case "key":
		signer, err := loadPrivateKey(cfg.KeyPath, cfg.KeyPassphrase)
		if err != nil {
			return err
		}
		sshAuth = []ssh.AuthMethod{ssh.PublicKeys(signer)}
		return nil

	default:
		return fmt.Errorf("unknown auth_method %q (expected \"password\" or \"key\")", cfg.AuthMethod)
	}
}

// loadPrivateKey reads and parses a private key file, decrypting it with
// passphrase if the key is encrypted.
func loadPrivateKey(path, passphrase string) (ssh.Signer, error) {
	if path == "" {
		return nil, errors.New("auth_method is \"key\" but key_path is empty (run --config)")
	}
	path = expandHome(path)

	pemBytes, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("could not read SSH key %s: %w", path, err)
	}

	signer, err := ssh.ParsePrivateKey(pemBytes)
	var missing *ssh.PassphraseMissingError
	if errors.As(err, &missing) {
		if passphrase == "" {
			return nil, fmt.Errorf("SSH key %s is encrypted but no key_passphrase is set (run --config)", path)
		}
		signer, err = ssh.ParsePrivateKeyWithPassphrase(pemBytes, []byte(passphrase))
		if errors.Is(err, x509.IncorrectPasswordError) {
			return nil, fmt.Errorf("wrong passphrase for SSH key %s", path)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("could not parse SSH key %s: %w", path, err)
	}
	return signer, nil
}

// expandHome turns a leading "~/" into the user's home directory.
func expandHome(path string) string {
	if path == "~" || strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(path, "~"))
		}
	}
	return path
}

func newSSHClient() (*ssh.Client, error) {
	hostKeyCallback, hostKeyAlgos, err := hostKeyConfig()
	if err != nil {
		reportHostKeyError(err)
		return nil, err
	}

	sshCfg := &ssh.ClientConfig{
		User:              cfg.Username,
		Auth:              sshAuth,
		HostKeyCallback:   hostKeyCallback,
		HostKeyAlgorithms: hostKeyAlgos,
		Timeout:           10 * time.Second,
	}

	conn, err := ssh.Dial("tcp", sshAddr(), sshCfg)
	if err != nil {
		err = classifyDialError(err)
		if reportHostKeyError(err) {
			return nil, err
		}
		if strings.Contains(err.Error(), "unable to authenticate") {
			return nil, fmt.Errorf("SSH authentication failed for %s@%s using %s auth: %s: %w",
				cfg.Username, cfg.IP, authMethod(), authHint(), err)
		}
		return nil, fmt.Errorf("SSH connection failed: %w", err)
	}

	hostKeyAccepted()
	return conn, nil
}

// authHint suggests the most likely fix for an authentication failure.
func authHint() string {
	if authMethod() == "key" {
		return "check that the public key is in ~/.ssh/authorized_keys on the server"
	}
	return "check the username and password"
}

// remoteReachable does a cheap TCP check on the SSH port (no login),
// used to detect whether the remote device is online.
func remoteReachable() bool {
	conn, err := net.DialTimeout("tcp", sshAddr(), 2*time.Second)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}
