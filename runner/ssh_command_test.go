package main

import (
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func runSSHLauncher(t *testing.T, stateDirectory string, port int, args ...string) ([]byte, error) {
	t.Helper()
	options := []string{"--", "-p", strconv.Itoa(port)}
	return runLauncher(t, stateDirectory, append(options, args...)...)
}

func runLauncher(t *testing.T, stateDirectory string, args ...string) ([]byte, error) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	arguments := []string{"ssh", "--state-directory", stateDirectory}
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

func TestOpenSSHAgentForwarding(t *testing.T) {
	g, directory := startOpenSSH(t)
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
	output, err := runLauncher(t, directory, "--", "-p", port, `test -z "$SSH_AUTH_SOCK" && printf absent`)
	if err != nil || string(output) != "absent" {
		t.Fatalf("guest did not confirm agent is absent: %v; output: %s", err, output)
	}

	output, err = runLauncher(t, directory, "--forward-agent", "--", "-p", port, "ssh-add -L")
	if err != nil || !strings.Contains(string(output), publicKey) {
		t.Fatalf("forwarded test key unavailable: %v; output: %s", err, output)
	}
}

func serveTestAgent(t *testing.T, path string) string {
	t.Helper()
	key := filepath.Join(t.TempDir(), "agent_key")
	if output, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", key).CombinedOutput(); err != nil {
		t.Fatalf("generate agent key: %v: %s", err, output)
	}

	agent := exec.Command("ssh-agent", "-D", "-a", path)
	if err := agent.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		agent.Process.Kill()
		agent.Wait()
	})
	t.Setenv("SSH_AUTH_SOCK", path)

	deadline := time.Now().Add(5 * time.Second)
	for {
		connection, err := net.Dial("unix", path)
		if err == nil {
			connection.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("agent did not start: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if output, err := exec.Command("ssh-add", key).CombinedOutput(); err != nil {
		t.Fatalf("add agent key: %v: %s", err, output)
	}
	publicKey, err := os.ReadFile(key + ".pub")
	if err != nil {
		t.Fatal(err)
	}
	fields := strings.Fields(string(publicKey))
	return strings.Join(fields[:2], " ")
}
