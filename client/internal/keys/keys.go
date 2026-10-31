// Package keys loads the SSH signer used to authenticate to agents.
package keys

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"

	"github.com/example/stack-man/client/internal/config"
)

// Result is a loaded signer plus an optional closer (the ssh-agent socket).
type Result struct {
	Signer ssh.Signer
	Closer io.Closer
}

// Load returns the configured signer. With use_ssh_agent and SSH_AUTH_SOCK
// set, the private key file is never read.
func Load(cfg *config.Config) (*Result, error) {
	if cfg.UseSSHAgent {
		if sock := os.Getenv("SSH_AUTH_SOCK"); sock != "" {
			signer, closer, err := fromAgent(sock)
			if err == nil {
				return &Result{Signer: signer, Closer: closer}, nil
			}
			// Fall through to the key file if the agent is unusable.
		}
	}
	signer, err := fromFile(cfg.KeyPath())
	if err != nil {
		return nil, err
	}
	return &Result{Signer: signer}, nil
}

func fromAgent(sock string) (ssh.Signer, io.Closer, error) {
	conn, err := net.Dial("unix", sock)
	if err != nil {
		return nil, nil, fmt.Errorf("keys: connect ssh-agent: %w", err)
	}
	client := agent.NewClient(conn)
	signers, err := client.Signers()
	if err != nil {
		conn.Close()
		return nil, nil, fmt.Errorf("keys: list ssh-agent keys: %w", err)
	}
	if len(signers) == 0 {
		conn.Close()
		return nil, nil, errors.New("keys: ssh-agent has no keys")
	}
	return signers[0], conn, nil
}

func fromFile(path string) (ssh.Signer, error) {
	if path == "" {
		return nil, errors.New("keys: no private_key_path configured")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("keys: read %s: %w", path, err)
	}
	signer, err := ssh.ParsePrivateKey(data)
	if err != nil {
		var passphraseErr *ssh.PassphraseMissingError
		if errors.As(err, &passphraseErr) {
			return nil, fmt.Errorf("keys: %s is passphrase-protected; use ssh-agent or an unencrypted key", path)
		}
		return nil, fmt.Errorf("keys: parse %s: %w", path, err)
	}
	return signer, nil
}
