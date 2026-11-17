package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

func runSSHLauncher(t *testing.T, stateDirectory string, port int, args ...string) ([]byte, error) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	arguments := []string{"ssh", "--state-directory", stateDirectory, "--", "-p", strconv.Itoa(port)}
	command := exec.CommandContext(t.Context(), executable, append(arguments, args...)...)
	return command.CombinedOutput()
}

func TestSSHLauncherConnectsAndForwardsCommands(t *testing.T) {
	config := tartFixture(t)
	config.BootstrapUser = "builder"
	t.Setenv("NIX_DARWIN_VM_TEST_IP", "127.0.0.1")
	session := startTestVM(t, config)
	port := config.dialer.listener.Addr().(*net.TCPAddr).Port

	output, err := runSSHLauncher(t, config.StateDirectory, port, "sw_vers", "-productVersion")
	if err != nil || strings.TrimSpace(string(output)) != "27.2" {
		t.Fatalf("SSH command failed: %v; output: %s", err, output)
	}
	output, err = runSSHLauncher(t, config.StateDirectory, port, "exit", "23")
	var exitError *exec.ExitError
	if !errors.As(err, &exitError) || exitError.ExitCode() != 23 {
		t.Fatalf("got %v, want remote exit status 23; output: %s", err, output)
	}

	session.stop(t)
	output, err = runSSHLauncher(t, config.StateDirectory, port, "true")
	if err == nil || !strings.Contains(string(output), "VM is not running") {
		t.Fatalf("stopped VM was not rejected: %v; output: %s", err, output)
	}
}

func TestSSHLauncherRejectsChangedHostKey(t *testing.T) {
	config := tartFixture(t)
	t.Setenv("NIX_DARWIN_VM_TEST_IP", "127.0.0.1")
	session := startTestVM(t, config)
	otherServer := newSSHTestServer(t, config.StateDirectory)
	port := otherServer.listener.Addr().(*net.TCPAddr).Port

	output, err := runSSHLauncher(t, config.StateDirectory, port, "true")
	var exitError *exec.ExitError
	if !errors.As(err, &exitError) || exitError.ExitCode() != 255 ||
		!strings.Contains(string(output), "Host key verification failed") {
		t.Fatalf("changed host key was not rejected: %v; output: %s", err, output)
	}
	session.stop(t)
}

func TestSSHLauncherDoesNotPrepareMissingVM(t *testing.T) {
	tartFixture(t)
	stateDirectory := filepath.Join(t.TempDir(), "missing")
	output, err := runSSHLauncher(t, stateDirectory, 22, "true")
	if err == nil || !strings.Contains(string(output), "VM is not prepared") {
		t.Fatalf("missing VM was not rejected: %v; output: %s", err, output)
	}
	if _, err := os.Stat(stateDirectory); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("SSH launcher created VM state: %v", err)
	}
}

func TestSSHArguments(t *testing.T) {
	checkRun(t, []string{"ssh", "--help"}, 0, "state-directory")
	checkRun(t, []string{"ssh"}, 2, "--state-directory is required")
	t.Setenv("SSH_AUTH_SOCK", "")
	checkRun(t, []string{"ssh", "--state-directory", t.TempDir(), "--forward-agent"}, 2, "requires SSH_AUTH_SOCK")
}

func TestSSHLauncherForwardsAgentOnRequest(t *testing.T) {
	g, _, directory := startOpenSSH(t)
	fakeCommands(t)
	t.Setenv("NIX_DARWIN_VM_TEST_IP", "127.0.0.1")
	if err := saveFakeVM(directory, fakeVM{Name: vmName, State: "running"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(g.directory, "provisioned"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	_, port, err := net.SplitHostPort(g.address)
	if err != nil {
		t.Fatal(err)
	}
	publicKey := serveTestAgent(t, filepath.Join(directory, "agent socket"))
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	for _, forward := range []bool{false, true} {
		args := []string{"ssh", "--state-directory", directory}
		if forward {
			args = append(args, "--forward-agent")
		}
		args = append(args, "--", "-p", port, "ssh-add", "-L")
		command := exec.CommandContext(t.Context(), executable, args...)
		output, err := command.CombinedOutput()
		if forward && err != nil {
			t.Fatalf("forwarded agent unavailable: %v; output: %s", err, output)
		}
		if strings.Contains(string(output), publicKey) != forward {
			t.Fatalf("forward=%v: unexpected agent identities: %s", forward, output)
		}
	}
}

func serveTestAgent(t *testing.T, path string) string {
	t.Helper()
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	keyring := agent.NewKeyring()
	if err := keyring.Add(agent.AddedKey{PrivateKey: privateKey}); err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	go func() {
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer connection.Close()
				agent.ServeAgent(keyring, connection)
			}()
		}
	}()
	t.Setenv("SSH_AUTH_SOCK", path)
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey())))
}
