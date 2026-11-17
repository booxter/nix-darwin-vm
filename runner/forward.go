package main

import (
	"context"
	_ "embed"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

//go:embed prepare-socket.sh
var prepareSocketScript string

func (g guest) forwardSockets(ctx context.Context, cancel context.CancelCauseFunc, stderr io.Writer) error {
	for name, socket := range g.forwardedSockets {
		if err := g.forwardSocket(ctx, cancel, socket, stderr); err != nil {
			return fmt.Errorf("forward socket %q: %w", name, err)
		}
	}
	return nil
}

func (g guest) forwardSocket(ctx context.Context, cancel context.CancelCauseFunc, socket forwardedSocket, stderr io.Writer) error {
	hostPath := socket.HostPath
	if strings.HasPrefix(hostPath, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		hostPath = filepath.Join(home, hostPath[2:])
	}
	info, err := os.Stat(hostPath)
	if err != nil {
		return fmt.Errorf("inspect host socket %q: %w", hostPath, err)
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("host path %q is not a Unix socket", hostPath)
	}

	// SSH sends command text to the guest's shell. Quote the path as one
	// argument ($1); sh -s reads the embedded setup script from stdin.
	command := "/bin/sh -s -- " + shellQuote(socket.GuestPath)
	setup, stopSetup := context.WithTimeout(ctx, 15*time.Second)
	_, err = g.runSSH(setup, g.address, ssh.PublicKeys(g.signer), command, strings.NewReader(prepareSocketScript))
	stopSetup()
	if err != nil {
		return fmt.Errorf("prepare guest socket directory: %w", err)
	}

	// Keep a separate SSH connection alive for the lifetime of this forward.
	client, err := g.sshClient(ctx, g.address, ssh.PublicKeys(g.signer))
	if err != nil {
		return err
	}
	// ListenUnix asks sshd to bind on the guest. Limit the wait for that request;
	// the established tunnel stays open until the VM session ends.
	timer := time.AfterFunc(15*time.Second, func() { client.Close() })
	listener, err := client.ListenUnix(socket.GuestPath)
	timer.Stop()
	if err != nil {
		client.Close()
		return fmt.Errorf("listen on guest socket %q: %w", socket.GuestPath, err)
	}
	context.AfterFunc(ctx, func() {
		client.Close()
		listener.Close()
	})
	go func() {
		for {
			connection, err := listener.Accept()
			if err != nil {
				if ctx.Err() == nil {
					// Activation and guest services may depend on this socket.
					// Stop the VM rather than leave it running without the tunnel.
					cancel(fmt.Errorf("guest socket %q stopped: %w", socket.GuestPath, err))
				}
				return
			}
			go func() {
				if err := proxySocket(ctx, connection, hostPath); err != nil && ctx.Err() == nil {
					fmt.Fprintf(stderr, "forward socket %q: %v\n", socket.GuestPath, err)
				}
			}()
		}
	}()
	return nil
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func proxySocket(ctx context.Context, guest net.Conn, hostPath string) error {
	defer guest.Close()
	host, err := (&net.Dialer{}).DialContext(ctx, "unix", hostPath)
	if err != nil {
		return fmt.Errorf("connect to host socket %q: %w", hostPath, err)
	}
	defer host.Close()
	// Context cancellation alone cannot interrupt io.Copy. Close both sockets
	// so neither direction stays blocked on an idle peer.
	stop := context.AfterFunc(ctx, func() {
		guest.Close()
		host.Close()
	})
	defer stop()

	copied := make(chan struct{})
	go func() {
		defer close(copied)
		io.Copy(host, guest)
		if unix, ok := host.(*net.UnixConn); ok {
			// Pass guest EOF to the host while still allowing its response.
			unix.CloseWrite()
		}
	}()
	io.Copy(guest, host)
	guest.Close()
	host.Close()
	// Wait for the other copy loop before releasing this proxy.
	<-copied
	return nil
}
