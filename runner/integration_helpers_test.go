//go:build integration

package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

var integrationConfig = flag.String("integration-config", "", "path to the real VM test fixtures")

// Keep cancellation across scenarios so an interrupted suite starts no more VMs.
var integrationContext, _ = signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)

type integrationFixture struct {
	Run, SSH, System, Message, NixVersion string
}

type integrationFixtures struct {
	Initial, Updated           integrationFixture
	User, StateDirectory, Tart string
}

type integrationVM struct {
	t         *testing.T
	ctx       context.Context
	cancel    context.CancelFunc
	directory string
	fixtures  integrationFixtures
	process   *integrationProcess
	runs      int
}

func newIntegrationVM(t *testing.T) *integrationVM {
	t.Helper()
	if integrationContext.Err() != nil {
		t.Skip("integration suite cancelled")
	}
	if *integrationConfig == "" {
		t.Fatal("run real VM scenarios with nix run .#test-vm")
	}
	data, err := os.ReadFile(*integrationConfig)
	if err != nil {
		t.Fatal(err)
	}
	var fixtures integrationFixtures
	if err := json.Unmarshal(data, &fixtures); err != nil {
		t.Fatal(err)
	}
	directory, err := os.MkdirTemp("", "nix-darwin-vm-integration-")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(integrationContext, 20*time.Minute)
	v := &integrationVM{t: t, ctx: ctx, cancel: cancel, directory: directory, fixtures: fixtures}
	t.Logf("VM state and logs: %s", directory)
	t.Cleanup(v.cleanup)
	return v
}

type integrationProcess struct {
	input  io.WriteCloser
	output *terminalOutput
	done   chan struct{}
	err    error
}

func (v *integrationVM) start(fixture integrationFixture, interactive bool) {
	v.t.Helper()
	v.runs++
	log, err := os.OpenFile(filepath.Join(v.directory, fmt.Sprintf("run-%d.log", v.runs)), os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		v.t.Fatal(err)
	}
	command := exec.CommandContext(v.ctx, fixture.Run)
	if interactive {
		command = exec.CommandContext(v.ctx, "script", "-q", "/dev/null", fixture.Run, "--interactive")
	}
	command.Dir = v.directory
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error {
		return command.Process.Signal(syscall.SIGTERM)
	}
	command.WaitDelay = 15 * time.Second
	p := &integrationProcess{output: &terminalOutput{changed: make(chan struct{}, 1)}, done: make(chan struct{})}
	if interactive {
		p.input, err = command.StdinPipe()
		if err != nil {
			log.Close()
			v.t.Fatal(err)
		}
	}
	command.Stdout = io.MultiWriter(log, p.output)
	command.Stderr = command.Stdout
	if err := command.Start(); err != nil {
		log.Close()
		v.t.Fatal(err)
	}
	v.process = p
	go func() {
		p.err = command.Wait()
		log.Close()
		close(p.done)
	}()
	p.wait(v.t, "Ready: guest configuration activated", 1, 15*time.Minute)
}

func (p *integrationProcess) snapshot() string {
	p.output.Lock()
	defer p.output.Unlock()
	return strings.ReplaceAll(p.output.buffer.String(), "\r", "")
}

func (p *integrationProcess) wait(t *testing.T, text string, count int, timeout time.Duration) {
	t.Helper()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		output := p.snapshot()
		if strings.Count(output, text) >= count {
			return
		}
		select {
		case <-p.output.changed:
		case <-p.done:
			t.Fatalf("VM exited waiting for %q: %v\n%s", text, p.err, p.snapshot())
		case <-integrationContext.Done():
			t.Fatal("integration suite cancelled")
		case <-timer.C:
			t.Fatalf("timed out waiting for %q\n%s", text, output)
		}
	}
}

func (p *integrationProcess) write(t *testing.T, text string) {
	t.Helper()
	if _, err := io.WriteString(p.input, text); err != nil {
		t.Fatal(err)
	}
}

func (p *integrationProcess) expectExit(t *testing.T, code int) {
	t.Helper()
	select {
	case <-p.done:
		actual := 0
		if p.err != nil {
			actual = exitCode(p.err)
		}
		if actual != code {
			t.Fatalf("runner exit status %d, expected %d: %v\n%s", actual, code, p.err, p.snapshot())
		}
	case <-time.After(time.Minute):
		t.Fatal("runner did not exit")
	}
}

func (v *integrationVM) ssh(command string, input io.Reader) string {
	v.t.Helper()
	ctx, cancel := context.WithTimeout(v.ctx, 30*time.Second)
	defer cancel()
	client := exec.CommandContext(ctx, v.fixtures.Initial.SSH, command)
	client.Dir = v.directory
	client.Stdin = input
	output, err := client.CombinedOutput()
	if err != nil {
		v.t.Fatalf("guest command failed: %v\n%s", err, output)
	}
	return strings.TrimSpace(string(output))
}

func (v *integrationVM) expectSSH(command, expected string) {
	v.t.Helper()
	if output := v.ssh(command, nil); output != expected {
		v.t.Fatalf("guest returned %q, expected %q", output, expected)
	}
}

func (v *integrationVM) verify(fixture integrationFixture) {
	v.t.Helper()
	v.expectSSH("id -un", v.fixtures.User)
	v.expectSSH("cat /etc/vm-test", fixture.Message)
	v.expectSSH("readlink /run/current-system", fixture.System)
	v.expectSSH("/run/current-system/sw/bin/nix --version", "nix (Nix) "+fixture.NixVersion)
	v.ssh("/run/current-system/sw/bin/nix-store --store daemon --query --hash /run/current-system", nil)
}

func (v *integrationVM) shell(command, marker string) {
	v.t.Helper()
	v.process.write(v.t, command+"\n")
	// Match an output line, not the command echoed by the interactive shell.
	v.process.wait(v.t, "\n"+marker+"\n", 1, 30*time.Second)
}

func (v *integrationVM) logout(count int) {
	v.t.Helper()
	v.process.write(v.t, "exit 0\n")
	v.process.wait(v.t, "VM is running. Press Enter to reconnect", count, 30*time.Second)
}

func (v *integrationVM) shutdown() {
	v.t.Helper()
	password, err := os.ReadFile(filepath.Join(v.directory, v.fixtures.StateDirectory, "credentials", "password"))
	if err != nil {
		v.t.Fatal(err)
	}
	v.ssh("sudo -S -p '' /sbin/shutdown -h now", strings.NewReader(string(password)+"\n"))
	v.process.expectExit(v.t, 0)
	v.expectStopped()
}

func (v *integrationVM) expectStopped() {
	v.t.Helper()
	ctx, cancel := context.WithTimeout(v.ctx, 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, v.fixtures.Initial.SSH, "true")
	command.Dir = v.directory
	if output, err := command.CombinedOutput(); err == nil {
		v.t.Fatalf("guest is still reachable after the runner exited: %s", output)
	}
}

func (v *integrationVM) cleanup() {
	v.cancel()
	if p := v.process; p != nil {
		if p.input != nil {
			p.input.Close()
		}
		select {
		case <-p.done:
		case <-time.After(20 * time.Second):
			v.t.Error("runner did not stop during cleanup")
		}
	}
	if err := v.stopRemainingVMs(); err != nil {
		v.t.Errorf("VM cleanup failed: %v", err)
	}
	if v.t.Failed() {
		v.t.Logf("retained VM state and logs: %s", v.directory)
	} else if err := os.RemoveAll(v.directory); err != nil {
		v.t.Error(err)
	}
}

func (v *integrationVM) stopRemainingVMs() error {
	state := filepath.Join(v.directory, v.fixtures.StateDirectory)
	if _, err := os.Stat(state); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	command := func(args ...string) *exec.Cmd {
		client := exec.CommandContext(ctx, v.fixtures.Tart, args...)
		client.Env = append(os.Environ(), "TART_HOME="+state)
		return client
	}
	output, err := command("list", "--source", "local", "--format", "json").Output()
	if err != nil {
		return err
	}
	var machines []struct{ Name, State string }
	if err := json.Unmarshal(output, &machines); err != nil {
		return err
	}
	for _, machine := range machines {
		if machine.State == "stopped" {
			continue
		}
		if output, err := command("stop", "--timeout", "5", machine.Name).CombinedOutput(); err != nil {
			return fmt.Errorf("stop %s: %w: %s", machine.Name, err, output)
		}
	}
	return nil
}
