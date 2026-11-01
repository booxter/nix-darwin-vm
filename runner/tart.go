package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Each state directory owns one VM, even if the configuration is renamed.
const vmName = "vm"

type tart struct {
	home   string
	stdout io.Writer
	stderr io.Writer
}

type tartVM struct {
	Name  string
	State string
}

type lockedWriter struct {
	mutex  *sync.Mutex
	writer io.Writer
}

func (w lockedWriter) Write(data []byte) (int, error) {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	return w.writer.Write(data)
}

func (t tart) command(ctx context.Context, args ...string) *exec.Cmd {
	command := exec.CommandContext(ctx, "tart", args...)
	command.Env = append(os.Environ(), "TART_HOME="+t.home)
	command.Stdin = os.Stdin
	command.Stdout = t.stdout
	command.Stderr = t.stderr

	// The runner owns terminal signals. Let Tart cancel its work before
	// falling back to a forced stop if it does not exit.
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error {
		return command.Process.Signal(os.Interrupt)
	}
	command.WaitDelay = 10 * time.Second
	return command
}

func (t tart) inspectVM(ctx context.Context) (*tartVM, error) {
	var output bytes.Buffer
	command := t.command(ctx, "list", "--source", "local", "--format", "json")
	command.Stdout = &output
	if err := command.Run(); err != nil {
		return nil, fmt.Errorf("list VMs: %w", err)
	}

	var machines []tartVM
	if err := json.Unmarshal(output.Bytes(), &machines); err != nil {
		return nil, fmt.Errorf("decode Tart VM list: %w", err)
	}
	for _, machine := range machines {
		if machine.Name != vmName {
			return nil, fmt.Errorf("state directory contains an unexpected VM: %s", machine.Name)
		}
	}
	if len(machines) == 0 {
		return nil, nil
	}
	return &machines[0], nil
}

func (t tart) ip(ctx context.Context) (net.IP, error) {
	var output bytes.Buffer
	command := t.command(ctx, "ip", vmName)
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Run(); err != nil {
		return nil, fmt.Errorf("resolve guest address: %w: %s", err, &output)
	}
	address := strings.TrimSpace(output.String())
	ip := net.ParseIP(address)
	if ip == nil {
		return nil, &net.ParseError{Type: "guest IP address", Text: address}
	}
	return ip, nil
}

func lockStateDirectory(directory string) (*os.File, error) {
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, fmt.Errorf("create state directory: %w", err)
	}
	// Keep the inode in place so every invocation locks the same file.
	lock, err := os.OpenFile(filepath.Join(directory, ".runner.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("open state lock: %w", err)
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lock.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, errors.New("VM state directory is already in use")
		}
		return nil, fmt.Errorf("lock state directory: %w", err)
	}
	return lock, nil
}

func runVM(ctx context.Context, config configuration, stdout, stderr io.Writer, dialer sshDialer) error {
	// Tart streams output while the runner reports guest preparation progress.
	// Share the lock because callers may use the same writer for both streams.
	var outputMutex sync.Mutex
	stdout = lockedWriter{mutex: &outputMutex, writer: stdout}
	stderr = lockedWriter{mutex: &outputMutex, writer: stderr}

	host, err := inspectHost(ctx)
	if err != nil {
		return err
	}
	g := newGuest(config, dialer)
	if err := g.checkProvisioningSupport(host); err != nil {
		return err
	}
	lock, err := lockStateDirectory(config.StateDirectory)
	if err != nil {
		return err
	}
	defer lock.Close()

	t := tart{home: config.StateDirectory, stdout: stdout, stderr: stderr}
	machine, err := t.inspectVM(ctx)
	if err != nil {
		return err
	}
	exists := machine != nil
	if exists && machine.State != "stopped" {
		return fmt.Errorf("VM is %s; stop it before starting another session", machine.State)
	}
	if err := g.prepare(exists); err != nil {
		return err
	}
	if !exists {
		fmt.Fprintf(stdout, "Creating macOS VM in %s\n", config.StateDirectory)
		if err := t.command(ctx, "create", "--from-ipsw", config.IPSW, vmName).Run(); err != nil {
			return fmt.Errorf("create VM: %w", err)
		}
	}
	if err := t.command(ctx, "set", vmName,
		"--cpu", strconv.Itoa(config.Cores),
		"--memory", strconv.Itoa(config.MemorySize),
	).Run(); err != nil {
		return fmt.Errorf("set VM resources: %w", err)
	}

	args := []string{"run", "--no-audio", "--no-clipboard"}
	if !config.Graphics {
		args = append(args, "--no-graphics")
	}
	if !g.configured {
		options := []string{
			"fullName=VM Administrator",
			"username=" + g.user,
			"password=" + g.password,
			"logsInAutomatically=false",
			"enablesRemoteLogin=true",
		}
		args = append(args, "--provisioning-opts="+strings.Join(options, ","))
	}
	args = append(args, vmName)
	fmt.Fprintf(stdout, "Starting %s; VM state is in %s\n", config.Name, config.StateDirectory)
	return t.runGuest(ctx, args, g)
}

func (t tart) runGuest(ctx context.Context, args []string, g guest) error {
	session, cancel := context.WithCancel(ctx)
	defer cancel()
	command := t.command(session, args...)
	if err := command.Start(); err != nil {
		return fmt.Errorf("start Tart: %w", err)
	}
	exited := make(chan error, 1)
	go func() {
		exited <- command.Wait()
		cancel()
	}()

	readyError := g.waitReady(session, t)
	var facts systemFacts
	if readyError == nil {
		fmt.Fprintln(t.stdout, "Preparing guest Nix")
		facts, readyError = g.ensureNix(session)
	}
	if readyError == nil {
		readyError = g.prepareCopy(session)
	}
	if readyError == nil {
		fmt.Fprintln(t.stdout, "Copying guest configuration")
		readyError = g.copySystem(session, t.stdout, t.stderr)
	}
	if readyError == nil {
		fmt.Fprintln(t.stdout, "Activating guest configuration")
		readyError = g.activateSystem(session, t.stdout, t.stderr)
	}
	if readyError == nil {
		readyError = g.waitReady(session, t)
	}
	if readyError == nil {
		facts, readyError = g.verifySystem(session)
	}
	if readyError != nil {
		cancel()
		runError := <-exited
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if runError != nil && !errors.Is(runError, context.Canceled) {
			return fmt.Errorf("run VM: %w", runError)
		}
		if errors.Is(readyError, context.Canceled) {
			return errors.New("VM exited before guest preparation completed")
		}
		return readyError
	}
	fmt.Fprintf(t.stdout, "Guest macOS %s; Nix %s\n", facts.MacOSVersion, facts.NixVersion)
	fmt.Fprintln(t.stdout, "Ready: guest configuration activated")
	if err := <-exited; err != nil {
		return fmt.Errorf("run VM: %w", err)
	}
	return nil
}

func exitCode(err error) int {
	if errors.Is(err, context.Canceled) {
		return 130
	}
	var exitError *exec.ExitError
	if errors.As(err, &exitError) {
		if code := exitError.ExitCode(); code >= 0 {
			return code
		}
		if status, ok := exitError.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			return 128 + int(status.Signal())
		}
	}
	return 1
}
