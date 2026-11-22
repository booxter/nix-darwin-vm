package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	_ "embed"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

//go:embed prepare-socket.sh
var prepareSocketScript string

func (g guest) startForwards(ctx context.Context, sockets map[string]forwardedSocket, stderr io.Writer) (*socketTunnel, error) {
	if len(sockets) == 0 {
		return &socketTunnel{}, nil
	}
	tunnel, err := g.startSSHMaster(ctx, stderr)
	if err != nil {
		return nil, err
	}

	for name, socket := range sockets {
		if err := tunnel.forward(ctx, socket); err != nil {
			tunnel.stop()
			return nil, fmt.Errorf("forward socket %q: %w", name, err)
		}
	}
	return tunnel, nil
}

func (s *socketTunnel) forward(ctx context.Context, socket forwardedSocket) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	// Reuse the same mapping when the VM reconnects or boots again.
	digest := sha256.Sum256([]byte(socket.GuestPath))
	mapping := fmt.Sprintf("%x", digest[:10])
	hostLink, err := s.linkHostSocket(socket.HostPath, mapping)
	if err != nil {
		return err
	}
	listener, err := s.prepareGuestListener(ctx, socket.GuestPath, mapping)
	if err != nil {
		return err
	}

	// The mux response acknowledges sshd's bind before activation can proceed.
	request := s.controlCommand(ctx, "forward", "-R", listener+":"+hostLink)
	if output, err := request.CombinedOutput(); err != nil {
		return fmt.Errorf("bind guest socket: %w: %s", err, output)
	}
	return nil
}

func (s *socketTunnel) prepareGuestListener(ctx context.Context, path, mapping string) (string, error) {
	generation := rand.Text()[:12]
	pathArg := "'" + strings.ReplaceAll(path, "'", "'\\''") + "'"
	setup := s.command(ctx, s.destination, "/bin/sh -s -- "+pathArg+" "+mapping+" "+generation)
	setup.Stdin = strings.NewReader(prepareSocketScript)
	var stderr bytes.Buffer
	setup.Stderr = &stderr
	output, err := setup.Output()
	if err != nil {
		return "", fmt.Errorf("prepare guest socket: %w: %s", err, &stderr)
	}

	// Only the remote listener field comes from the guest. It must not supply
	// delimiters or expansions in the forwarding specification.
	listener := string(output)
	if !filepath.IsAbs(listener) || filepath.Base(listener) != generation || strings.ContainsAny(listener, ":[]%\\\x00\r\n") {
		return "", fmt.Errorf("unexpected guest listener path %q", listener)
	}
	return listener, nil
}

func (s *socketTunnel) linkHostSocket(path, mapping string) (string, error) {
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		path = filepath.Join(home, path[2:])
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSocket == 0 {
		return "", fmt.Errorf("host path %q is not a Unix socket", path)
	}

	// Give OpenSSH a short socket path without ':' so it can parse -R guest:host.
	link := filepath.Join(s.directory, mapping)
	if err := os.Symlink(path, link); err != nil {
		return "", err
	}
	return link, nil
}
