package main

import (
	"fmt"
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

	conn, err := ssh.Dial("tcp", fmt.Sprintf("%s:22", cfg.IP), sshCfg)
	if err != nil {
		return nil, fmt.Errorf("SSH connection failed: %w", err)
	}

	return conn, nil
}
