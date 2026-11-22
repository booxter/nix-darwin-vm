//go:build integration

package main

import (
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"
)

func startIntegrationEchoServer(t *testing.T, path string) <-chan struct{} {
	t.Helper()
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	closed := make(chan struct{})
	firstClose := sync.OnceFunc(func() { close(closed) })
	go serveEcho(listener, firstClose)
	return closed
}

func serveEcho(listener net.Listener, onClose func()) {
	for {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		go func() {
			defer conn.Close()
			io.Copy(conn, conn)
			onClose()
		}()
	}
}

func (v *integrationVM) socketServers() (<-chan struct{}, string) {
	v.t.Helper()
	// Keep socket addresses short while retaining VM state under TMPDIR for CI.
	directory, err := os.MkdirTemp("/tmp", "vm-sockets-")
	if err != nil {
		v.t.Fatal(err)
	}
	v.t.Cleanup(func() { os.RemoveAll(directory) })
	server := func(name string) <-chan struct{} {
		path := filepath.Join(directory, name)
		closed := startIntegrationEchoServer(v.t, path)
		if err := os.Symlink(path, filepath.Join(v.directory, name)); err != nil {
			v.t.Fatal(err)
		}
		return closed
	}
	activation := server("activation.sock")
	server("vm.sock")
	return activation, serveTestAgent(v.t, filepath.Join(directory, "agent.sock"))
}

func (v *integrationVM) socketCommand(name, message string) string {
	return fmt.Sprintf("printf %s | /run/current-system/sw/bin/socat - UNIX-CONNECT:/Users/%s/vm-test-sockets/%s.sock", message, v.fixtures.User, name)
}

func (v *integrationVM) expectActivationSocketRevoked(closed <-chan struct{}) {
	v.t.Helper()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		v.t.Fatal("the connection opened by the activation script is still open")
	}
	if output, err := v.runSSH(v.socketCommand("activation", "stale"), nil); err == nil {
		v.t.Fatalf("activation socket still accepts connections after activation: %s", output)
	}
}

func (v *integrationVM) expectSocketRecovery() {
	v.t.Helper()
	command := v.socketCommand("vm", "live")
	v.expectSSH(command, "live")
	boot := v.ssh("sysctl -n kern.boottime", nil)
	v.stopSocketTransport()
	deadline := time.Now().Add(2 * time.Minute)
	for {
		output, err := v.runSSH(command, nil)
		if err == nil && output == "live" {
			break
		}
		if time.Now().After(deadline) {
			v.t.Fatalf("socket forwarding did not recover: %v\n%s\n%s", err, output, v.process.snapshot())
		}
		time.Sleep(time.Second)
	}
	if current := v.ssh("sysctl -n kern.boottime", nil); current != boot {
		v.t.Fatalf("guest boot changed during forwarding recovery: %s -> %s", boot, current)
	}
}

func (v *integrationVM) stopSocketTransport() {
	v.t.Helper()
	// The runner's direct SSH children are its forwarding transports. Restrict
	// fault injection to this runner, leaving other VM tests and SSH sessions alone.
	if err := exec.Command("/usr/bin/pkill", "-KILL", "-P", strconv.Itoa(v.process.pid), "-x", "ssh").Run(); err != nil {
		v.t.Fatalf("kill socket transport for runner %d: %v", v.process.pid, err)
	}
}
