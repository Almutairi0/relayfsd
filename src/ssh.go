package main

import (
	"fmt"
	"net"
	"time"

	"golang.org/x/crypto/ssh"
)

func newSSHClient() (*ssh.Client, error) {
	sshCfg := &ssh.ClientConfig{
		User: cfg.Username,
		Auth: []ssh.AuthMethod{
			ssh.Password(cfg.Password),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	}

	conn, err := ssh.Dial("tcp", net.JoinHostPort(cfg.IP, "22"), sshCfg)
	if err != nil {
		return nil, fmt.Errorf("SSH connection failed: %w", err)
	}

	return conn, nil
}

// remoteReachable does a cheap TCP check on the SSH port (no login),
// used to detect whether the remote device is online.
func remoteReachable() bool {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(cfg.IP, "22"), 2*time.Second)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}
