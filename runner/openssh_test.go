package main

import (
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func startOpenSSH(t *testing.T) (guest, string) {
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
	g := newGuest(configuration{StateDirectory: directory, BootstrapUser: account.Username}, &net.Dialer{})
	if err := g.prepare(false); err != nil {
		t.Fatal(err)
	}
	g.address = launchOpenSSH(t, g, directory)
	waitForOpenSSH(t, g)
	return g, directory
}

func launchOpenSSH(t *testing.T, g guest, directory string) string {
	t.Helper()
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
		// Keep the test account's shell configuration from supplying its own agent.
		"-o", "SetEnv=ZDOTDIR="+directory,
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
	return address
}

func waitForOpenSSH(t *testing.T, g guest) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		err := g.connect(t.Context(), g.address)
		if err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("connect to sshd: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
