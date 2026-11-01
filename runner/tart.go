package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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

func (t tart) inspectVM(ctx context.Context) (bool, error) {
	var output bytes.Buffer
	command := t.command(ctx, "list", "--source", "local", "--format", "json")
	command.Stdout = &output
	if err := command.Run(); err != nil {
		return false, fmt.Errorf("list VMs: %w", err)
	}

	var machines []struct {
		Name  string
		State string
	}
	if err := json.Unmarshal(output.Bytes(), &machines); err != nil {
		return false, fmt.Errorf("decode Tart VM list: %w", err)
	}
	for _, machine := range machines {
		if machine.Name != vmName {
			return false, fmt.Errorf("state directory contains an unexpected VM: %s", machine.Name)
		}
		if machine.State != "stopped" {
			return false, fmt.Errorf("VM is %s; stop it before starting another session", machine.State)
		}
	}
	return len(machines) != 0, nil
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

func runVM(ctx context.Context, config configuration, stdout, stderr io.Writer) error {
	lock, err := lockStateDirectory(config.StateDirectory)
	if err != nil {
		return err
	}
	defer lock.Close()

	t := tart{home: config.StateDirectory, stdout: stdout, stderr: stderr}
	exists, err := t.inspectVM(ctx)
	if err != nil {
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
	args = append(args, vmName)
	fmt.Fprintf(stdout, "Starting %s; VM state is in %s\n", config.Name, config.StateDirectory)
	if err := t.command(ctx, args...).Run(); err != nil {
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
