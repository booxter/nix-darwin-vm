package main

import (
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func listenUnix(t *testing.T, path string) *net.UnixListener {
	t.Helper()
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	listener.SetDeadline(time.Now().Add(10 * time.Second))
	return listener
}

func dialUnix(t *testing.T, path string) *net.UnixConn {
	t.Helper()
	connection, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { connection.Close() })
	connection.SetDeadline(time.Now().Add(10 * time.Second))
	return connection
}

func acceptUnix(t *testing.T, listener *net.UnixListener) *net.UnixConn {
	t.Helper()
	connection, err := listener.AcceptUnix()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { connection.Close() })
	connection.SetDeadline(time.Now().Add(10 * time.Second))
	return connection
}

func checkHalfClose(t *testing.T, sender, receiver *net.UnixConn) {
	t.Helper()
	if err := sender.CloseWrite(); err != nil {
		t.Fatal(err)
	}
	if _, err := receiver.Read(make([]byte, 1)); err != io.EOF {
		t.Fatalf("read after half-close: %v", err)
	}
	if _, err := receiver.Write([]byte("reply")); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, len("reply"))
	if _, err := io.ReadFull(sender, buffer); err != nil || string(buffer) != "reply" {
		t.Fatalf("reply after half-close %q: %v", buffer, err)
	}
}

// This test is run in a subprocess so killing it leaves both socket peers alive.
func TestOpenSSHForwardSubprocess(t *testing.T) {
	address := os.Getenv("NIX_DARWIN_VM_TEST_FORWARD_ADDRESS")
	if address == "" {
		t.Skip("subprocess only")
	}
	directory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	g := guest{
		directory: filepath.Join(directory, "credentials"),
		address:   address,
		user:      os.Getenv("NIX_DARWIN_VM_TEST_FORWARD_USER"),
	}
	sockets := map[string]forwardedSocket{
		"service": {HostPath: "host.sock", GuestPath: filepath.Join(directory, "guest.sock")},
	}
	tunnel, err := g.startForwards(t.Context(), sockets, os.Stderr)
	if err != nil {
		t.Fatal(err)
	}
	defer tunnel.stop()
	<-tunnel.done
}

func TestOpenSSHForwardClosesWhenRunnerDies(t *testing.T) {
	g, directory := startOpenSSH(t)
	host := listenUnix(t, filepath.Join(directory, "host.sock"))
	child := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestOpenSSHForwardSubprocess$")
	child.Dir = directory
	child.Env = append(os.Environ(),
		"NIX_DARWIN_VM_TEST_FORWARD_ADDRESS="+g.address,
		"NIX_DARWIN_VM_TEST_FORWARD_USER="+g.user,
	)
	child.Stderr = os.Stderr
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		child.Process.Kill()
		child.Wait()
	})

	path := filepath.Join(directory, "guest.sock")
	deadline := time.Now().Add(10 * time.Second)
	var client net.Conn
	for {
		var err error
		client, err = net.Dial("unix", path)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("subprocess did not establish the forward")
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Cleanup(func() { client.Close() })
	held := acceptUnix(t, host)
	if err := child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if _, err := held.Read(make([]byte, 1)); err != io.EOF {
		t.Fatalf("held connection after runner death: %v", err)
	}
}

func TestOpenSSHForwardSocket(t *testing.T) {
	g, directory := startOpenSSH(t)
	hostPath := filepath.Join(directory, "host :%socket")
	guestPath := filepath.Join(directory, "guest space", "socket:%one")
	listener := listenUnix(t, hostPath)
	sockets := map[string]forwardedSocket{"test": {HostPath: hostPath, GuestPath: guestPath}}
	tunnel, err := g.startForwards(t.Context(), sockets, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	defer tunnel.stop()

	guest := dialUnix(t, guestPath)
	host := acceptUnix(t, listener)
	checkHalfClose(t, host, guest)

	guest = dialUnix(t, guestPath)
	host = acceptUnix(t, listener)
	checkHalfClose(t, guest, host)

	dialUnix(t, guestPath)
	held := acceptUnix(t, listener)
	tunnel.stop()
	if _, err := held.Read(make([]byte, 1)); err != io.EOF {
		t.Fatalf("connection after stop: %v", err)
	}

	restarted, err := g.startForwards(t.Context(), sockets, io.Discard)
	if err != nil {
		t.Fatalf("restart forward: %v", err)
	}
	defer restarted.stop()
	dialUnix(t, guestPath)
}

func TestOpenSSHForwardSocketCollision(t *testing.T) {
	g, directory := startOpenSSH(t)
	hostPath := filepath.Join(directory, "host.sock")
	listenUnix(t, hostPath)
	guestPath := filepath.Join(directory, "existing.sock")
	listenUnix(t, guestPath)
	_, err := g.startForwards(t.Context(), map[string]forwardedSocket{
		"test": {HostPath: hostPath, GuestPath: guestPath},
	}, io.Discard)
	if err == nil {
		t.Fatal("forward succeeded over existing socket")
	}
	dialUnix(t, guestPath)

	existingDir := filepath.Join(directory, "existing-dir")
	if err := os.Mkdir(existingDir, 0755); err != nil {
		t.Fatal(err)
	}
	_, err = g.startForwards(t.Context(), map[string]forwardedSocket{
		"test": {HostPath: hostPath, GuestPath: existingDir},
	}, io.Discard)
	if err == nil {
		t.Fatal("forward succeeded over existing directory")
	}
	entries, err := os.ReadDir(existingDir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("existing directory was disturbed: %v, %v", entries, err)
	}
}
