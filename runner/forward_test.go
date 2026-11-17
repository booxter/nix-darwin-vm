package main

import (
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestProxySocket(t *testing.T) {
	path := filepath.Join(t.TempDir(), "echo.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		connection, err := listener.Accept()
		if err == nil {
			defer connection.Close()
			io.Copy(connection, connection)
		}
	}()
	client, proxy := net.Pipe()
	defer client.Close()
	client.SetDeadline(time.Now().Add(5 * time.Second))
	go proxySocket(t.Context(), proxy, path)
	if _, err := client.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 5)
	if _, err := io.ReadFull(client, buffer); err != nil || string(buffer) != "hello" {
		t.Fatalf("echo %q: %v", buffer, err)
	}
}

func TestProxySocketMissingHost(t *testing.T) {
	client, proxy := net.Pipe()
	defer client.Close()
	client.SetDeadline(time.Now().Add(5 * time.Second))
	go proxySocket(t.Context(), proxy, filepath.Join(t.TempDir(), "missing.sock"))
	if _, err := client.Read(make([]byte, 1)); err != io.EOF {
		t.Fatalf("missing host socket: got %v, want EOF", err)
	}
}

func TestProxySocketCancellation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "idle.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	listener.SetDeadline(time.Now().Add(5 * time.Second))

	client, proxy := net.Pipe()
	defer client.Close()
	client.SetDeadline(time.Now().Add(5 * time.Second))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- proxySocket(ctx, proxy, path) }()

	host, err := listener.AcceptUnix()
	if err != nil {
		t.Fatal(err)
	}
	defer host.Close()
	host.SetDeadline(time.Now().Add(5 * time.Second))
	// Exchange data before cancelling so both copy loops have started.
	if _, err := client.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(host, make([]byte, 1)); err != nil {
		t.Fatal(err)
	}

	// The host stays open and sends nothing; cancellation must unblock reads.
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("proxy did not stop on cancellation")
	}
	for _, connection := range []net.Conn{client, host} {
		if _, err := connection.Read(make([]byte, 1)); err != io.EOF {
			t.Fatalf("cancelled proxy connection: got %v, want EOF", err)
		}
	}
}

func TestForwardSocketRequiresHostSocket(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	path := filepath.Join(t.TempDir(), "missing.sock")
	err := (guest{}).forwardSocket(ctx, cancel, forwardedSocket{HostPath: path, GuestPath: "/tmp/guest/socket"}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "inspect host socket") {
		t.Fatalf("missing host socket: %v", err)
	}
}

func startEchoSocket(t *testing.T, path string) {
	t.Helper()
	host, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { host.Close() })
	go func() {
		for {
			connection, err := host.Accept()
			if err != nil {
				return
			}
			go func() {
				defer connection.Close()
				io.Copy(connection, connection)
			}()
		}
	}()
}

func checkSocketEcho(t *testing.T, path, message string) {
	t.Helper()
	client, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	client.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := client.Write([]byte(message)); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, len(message))
	if _, err := io.ReadFull(client, buffer); err != nil || string(buffer) != message {
		t.Fatalf("forwarded echo %q: %v", buffer, err)
	}
}

func TestForwardSocketOverOpenSSH(t *testing.T) {
	g, dialer, directory := startOpenSSH(t)
	hostPath := filepath.Join(directory, "host.sock")
	startEchoSocket(t, hostPath)
	guestPath := filepath.Join(directory, "guest", "socket")
	ctx, cancel := context.WithCancelCause(t.Context())
	defer cancel(nil)
	if err := g.forwardSocket(ctx, cancel, forwardedSocket{HostPath: hostPath, GuestPath: guestPath}, io.Discard); err != nil {
		t.Fatal(err)
	}
	checkSocketEcho(t, guestPath, "hello")
	dialer.disconnect()
	select {
	case <-ctx.Done():
		if cause := context.Cause(ctx); cause == nil || !strings.Contains(cause.Error(), "stopped") {
			t.Fatalf("tunnel loss: %v", cause)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("tunnel loss did not cancel session")
	}
	if err := os.Remove(guestPath); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	stale, err := net.ListenUnix("unix", &net.UnixAddr{Name: guestPath, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	stale.SetUnlinkOnClose(false)
	stale.Close()
	next, stop := context.WithCancelCause(t.Context())
	defer stop(nil)
	if err := g.forwardSocket(next, stop, forwardedSocket{HostPath: hostPath, GuestPath: guestPath}, io.Discard); err != nil {
		t.Fatalf("restart with stale socket: %v", err)
	}
	checkSocketEcho(t, guestPath, "again")
}
