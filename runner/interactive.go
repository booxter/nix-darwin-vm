package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"

	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

func (g guest) interact(ctx context.Context, input *os.File, stdout, stderr io.Writer) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := g.openShell(ctx, input, stdout, stderr)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			fmt.Fprintln(stderr, "SSH session ended:", err)
		}
		fmt.Fprintln(stdout, "VM is running. Press Enter to reconnect, or Ctrl-C to stop.")
		if err := waitReconnect(ctx, input); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

func (g guest) openShell(ctx context.Context, input *os.File, stdout, stderr io.Writer) error {
	host, port, err := net.SplitHostPort(g.address)
	if err != nil {
		return err
	}
	state, err := term.GetState(int(input.Fd()))
	if err != nil {
		return fmt.Errorf("read terminal settings: %w", err)
	}
	// OpenSSH normally restores these itself; also restore them if it is killed.
	defer term.Restore(int(input.Fd()), state)

	command := sshCommand(ctx, g.directory, "-t", "-p", port, "-l", g.user, host)
	command.Stdin = input
	command.Stdout = stdout
	command.Stderr = stderr
	return command.Run()
}

func waitReconnect(ctx context.Context, input *os.File) error {
	// Poll before reading so cancellation never waits for a line of terminal
	// input. Closing a terminal descriptor does not interrupt a macOS read.
	descriptors := []unix.PollFd{{Fd: int32(input.Fd()), Events: unix.POLLIN}}
	var character [1]byte
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		n, err := unix.Poll(descriptors, 100)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return err
		}
		if n == 0 {
			continue
		}
		n, err = unix.Read(int(descriptors[0].Fd), character[:])
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return err
		}
		if n == 0 {
			return io.EOF
		}
		if character[0] == '\n' {
			return nil
		}
	}
}
