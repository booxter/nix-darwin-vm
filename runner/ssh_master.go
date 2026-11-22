package main

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

//go:embed start-forward-master.sh
var startForwardMasterScript string

// One OpenSSH master owns all forwarded connections with the same lifetime.
// Its zero value represents an empty set of forwards: done never signals.
type socketTunnel struct {
	credentials string
	directory   string
	destination string
	cancel      context.CancelFunc
	done        chan struct{}
}

func (g guest) startSSHMaster(ctx context.Context, stderr io.Writer) (*socketTunnel, error) {
	directory, err := os.MkdirTemp("/tmp", "vm-ssh-")
	if err != nil {
		return nil, err
	}

	session, cancel := context.WithCancel(ctx)
	destination := url.URL{Scheme: "ssh", User: url.User(g.user), Host: g.address}
	tunnel := &socketTunnel{
		credentials: g.directory,
		directory:   directory,
		destination: destination.String(),
		cancel:      cancel,
		done:        make(chan struct{}),
	}
	if err := tunnel.launch(session, stderr); err != nil {
		cancel()
		os.RemoveAll(directory)
		return nil, err
	}
	if err := tunnel.waitReady(ctx); err != nil {
		tunnel.stop()
		return nil, err
	}
	return tunnel, nil
}

func (s *socketTunnel) launch(ctx context.Context, stderr io.Writer) error {
	args := []string{"-c", startForwardMasterScript, "sh", "ssh"}
	args = append(args, s.options()...)
	args = append(args, "-M", "-N", s.destination)

	master := exec.CommandContext(ctx, "/bin/sh", args...)
	master.Dir = s.credentials
	master.Stderr = stderr
	master.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	// Closing the pipe stops the master's process group. The kernel also
	// closes our end if the runner dies before it can cancel this command.
	lifetime, err := master.StdinPipe()
	if err != nil {
		return err
	}
	master.Cancel = lifetime.Close
	master.WaitDelay = 5 * time.Second
	if err := master.Start(); err != nil {
		return err
	}

	go func() {
		master.Wait()
		os.RemoveAll(s.directory)
		close(s.done)
	}()
	return nil
}

func (s *socketTunnel) options() []string {
	return append(sshOptions(false), "-S", filepath.Join(s.directory, "control"))
}

func (s *socketTunnel) command(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "ssh", append(s.options(), args...)...)
	cmd.Dir = s.credentials
	return cmd
}

func (s *socketTunnel) controlCommand(ctx context.Context, operation string, options ...string) *exec.Cmd {
	args := append([]string{"-O", operation}, options...)
	return s.command(ctx, append(args, s.destination)...)
}

func (s *socketTunnel) waitReady(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	for {
		if err := s.controlCommand(ctx, "check").Run(); err == nil {
			return nil
		}
		select {
		case <-s.done:
			return errors.New("SSH forwarding master exited during startup")
		case <-ctx.Done():
			return fmt.Errorf("wait for SSH forwarding master: %w", ctx.Err())
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func (s *socketTunnel) stop() {
	if s.cancel != nil {
		s.cancel()
		<-s.done
	}
}
