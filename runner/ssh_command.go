package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

func sshMain(args []string, stderr io.Writer) int {
	flags := flag.NewFlagSet("runner ssh", flag.ContinueOnError)
	flags.SetOutput(stderr)
	directory := flags.String("state-directory", "", "directory containing the running VM's state")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if *directory == "" {
		fmt.Fprintln(stderr, "runner ssh: --state-directory is required")
		return 2
	}

	stateDirectory, err := filepath.Abs(*directory)
	if err != nil {
		fmt.Fprintln(stderr, "runner ssh:", err)
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()

	command, err := guestSSHCommand(ctx, stateDirectory, flags.Args(), stderr)
	if err != nil {
		fmt.Fprintln(stderr, "runner ssh:", err)
		if ctx.Err() != nil {
			return 130
		}
		return 1
	}

	// OpenSSH takes over the terminal and handles signals directly.
	stop()
	if err := replaceWithSSH(command); err != nil {
		fmt.Fprintln(stderr, "runner ssh:", err)
		return 1
	}
	return 0
}

func guestSSHCommand(ctx context.Context, stateDirectory string, args []string, stderr io.Writer) (*exec.Cmd, error) {
	credentials := filepath.Join(stateDirectory, "credentials")
	if _, err := os.Stat(filepath.Join(credentials, "provisioned")); err != nil {
		return nil, fmt.Errorf("VM is not prepared; start it with system.build.vm first: %w", err)
	}

	username, err := os.ReadFile(filepath.Join(credentials, "username"))
	if err != nil {
		return nil, fmt.Errorf("read bootstrap username: %w", err)
	}
	if !bootstrapUserPattern.Match(username) {
		return nil, errors.New("invalid saved bootstrap username")
	}

	t := tart{home: stateDirectory, stdout: io.Discard, stderr: stderr}
	machine, err := t.inspectVM(ctx)
	if err != nil {
		return nil, err
	}
	if machine == nil || machine.State != "running" {
		return nil, errors.New("VM is not running; start it with system.build.vm first")
	}
	ip, err := t.ip(ctx)
	if err != nil {
		return nil, err
	}

	options := append([]string{"-l", string(username), ip.String()}, args...)
	command := sshCommand(ctx, credentials, options...)
	if command.Err != nil {
		return nil, command.Err
	}

	return command, nil
}

func sshCommand(ctx context.Context, credentials string, args ...string) *exec.Cmd {
	command := exec.CommandContext(ctx, "ssh", append(sshOptions(), args...)...)
	// Fixed relative filenames avoid OpenSSH path expansion and quoting rules.
	command.Dir = credentials
	// Give OpenSSH a chance to restore the terminal before forcing it to exit.
	command.Cancel = func() error {
		return command.Process.Signal(syscall.SIGTERM)
	}
	command.WaitDelay = 5 * time.Second
	return command
}

func sshOptions() []string {
	return []string{
		"-F", "/dev/null",
		"-i", "ssh_key",
		"-o", "IdentityAgent=none",
		"-o", "IdentitiesOnly=yes",
		"-o", "PreferredAuthentications=publickey",
		"-o", "BatchMode=yes",
		"-o", "UserKnownHostsFile=known_hosts",
		"-o", "GlobalKnownHostsFile=/dev/null",
		"-o", "HostKeyAlias=vm",
		"-o", "StrictHostKeyChecking=yes",
		"-o", "ConnectTimeout=3",
		"-o", "ConnectionAttempts=1",
		"-o", "ServerAliveInterval=5",
		"-o", "ServerAliveCountMax=1",
	}
}

func replaceWithSSH(command *exec.Cmd) error {
	if err := os.Chdir(command.Dir); err != nil {
		return err
	}
	return syscall.Exec(command.Path, command.Args, os.Environ())
}
