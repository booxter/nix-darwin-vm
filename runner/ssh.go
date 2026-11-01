package main

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

type sshDialer interface {
	DialContext(context.Context, string, string) (net.Conn, error)
}

//go:embed install-public-key.sh
var installPublicKey string

func (g guest) connect(ctx context.Context, address string) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	_, err := g.runSSH(ctx, address, ssh.PublicKeys(g.signer), "true", nil)
	return err
}

func (g guest) bootstrap(ctx context.Context, address string) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	// An interrupted bootstrap may have installed the key before saving its marker.
	err := g.connect(ctx, address)
	var keyError *knownhosts.KeyError
	if err == nil || errors.As(err, &keyError) {
		return err
	}

	publicKey := bytes.NewReader(ssh.MarshalAuthorizedKey(g.signer.PublicKey()))
	if _, err := g.runSSH(ctx, address, ssh.Password(g.password), installPublicKey, publicKey); err != nil {
		return fmt.Errorf("install SSH key: %w", err)
	}
	return g.connect(ctx, address)
}

func (g guest) runSSH(ctx context.Context, address string, auth ssh.AuthMethod, command string, stdin io.Reader) ([]byte, error) {
	checkHostKey, err := g.hostKeyCallback()
	if err != nil {
		return nil, err
	}
	connection, err := g.dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, err
	}
	defer connection.Close()

	// Bound the entire exchange, including a server that accepts TCP but
	// never sends an SSH banner. Cancellation closes the connection immediately.
	deadline, _ := ctx.Deadline()
	if err := connection.SetDeadline(deadline); err != nil {
		return nil, err
	}
	stop := context.AfterFunc(ctx, func() { connection.Close() })
	defer stop()
	config := &ssh.ClientConfig{
		User:            g.user,
		Auth:            []ssh.AuthMethod{auth},
		HostKeyCallback: checkHostKey,
	}
	transport, channels, requests, err := ssh.NewClientConn(connection, address, config)
	if err != nil {
		return nil, fmt.Errorf("SSH handshake: %w", err)
	}
	client := ssh.NewClient(transport, channels, requests)
	defer client.Close()
	session, err := client.NewSession()
	if err != nil {
		return nil, err
	}
	defer session.Close()
	var stdout, stderr bytes.Buffer
	session.Stdin = stdin
	session.Stdout = &stdout
	session.Stderr = &stderr
	if err := session.Run(command); err != nil {
		return nil, fmt.Errorf("SSH command: %w: %s", err, &stderr)
	}
	return stdout.Bytes(), nil
}

func (g guest) hostKeyCallback() (ssh.HostKeyCallback, error) {
	path := filepath.Join(g.directory, "known_hosts")
	check, err := knownhosts.New(path)
	if err != nil {
		return nil, err
	}
	return func(_ string, remote net.Addr, key ssh.PublicKey) error {
		// Pin the VM's identity independently of its DHCP address. This alias
		// is also used by the OpenSSH launcher.
		err := check("vm:22", remote, key)
		var keyError *knownhosts.KeyError
		if !errors.As(err, &keyError) || len(keyError.Want) != 0 {
			return err
		}
		file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		defer file.Close()
		_, err = io.WriteString(file, knownhosts.Line([]string{"vm"}, key)+"\n")
		return err
	}, nil
}
