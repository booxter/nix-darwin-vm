package main

import (
	"context"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"
)

func startOpenSSH(t *testing.T) (guest, *disconnectableDialer, string) {
	t.Helper()
	if os.Getenv("NIX_DARWIN_VM_TEST_SSHD") != "1" {
		t.Skip("set NIX_DARWIN_VM_TEST_SSHD=1 outside the Nix sandbox; OpenSSH cannot initialize its Darwin sandbox inside it")
	}
	directory, err := os.MkdirTemp("/tmp", "vm-ssh-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(directory) })
	account, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	dialer := &disconnectableDialer{}
	g := newGuest(configuration{StateDirectory: directory, BootstrapUser: account.Username}, dialer)
	if err := g.prepare(false); err != nil {
		t.Fatal(err)
	}
	hostKey := filepath.Join(directory, "host_key")
	if output, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", hostKey).CombinedOutput(); err != nil {
		t.Fatalf("generate host key: %v: %s", err, output)
	}
	reserved, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := reserved.Addr().String()
	_, port, err := net.SplitHostPort(address)
	if err != nil {
		t.Fatal(err)
	}
	reserved.Close()
	sshd, err := exec.LookPath("sshd")
	if err != nil {
		t.Fatal(err)
	}
	server := exec.Command(sshd,
		"-D", "-e", "-f", "/dev/null",
		"-p", port,
		"-h", hostKey,
		"-o", "ListenAddress=127.0.0.1",
		"-o", "PidFile="+filepath.Join(directory, "pid"),
		"-o", "AuthorizedKeysFile="+filepath.Join(g.directory, "ssh_key.pub"),
		"-o", "StrictModes=no",
		"-o", "PasswordAuthentication=no",
		"-o", "LogLevel=QUIET",
	)
	server.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	server.Stderr = os.Stderr
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		syscall.Kill(-server.Process.Pid, syscall.SIGKILL)
		server.Wait()
	})
	g.address = address
	deadline := time.Now().Add(5 * time.Second)
	for {
		err = g.connect(t.Context(), address)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("connect to sshd: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	return g, dialer, directory
}

type disconnectableDialer struct {
	mutex       sync.Mutex
	connections []net.Conn
}

func (d *disconnectableDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	connection, err := (&net.Dialer{}).DialContext(ctx, network, address)
	if err != nil {
		return nil, err
	}
	d.mutex.Lock()
	d.connections = append(d.connections, connection)
	d.mutex.Unlock()
	return connection, nil
}

func (d *disconnectableDialer) disconnect() {
	d.mutex.Lock()
	defer d.mutex.Unlock()
	for _, connection := range d.connections {
		connection.Close()
	}
}
