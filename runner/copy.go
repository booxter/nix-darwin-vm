package main

import (
	"context"
	_ "embed"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"golang.org/x/crypto/ssh"
)

//go:embed configure-copy.sh
var configureCopyScript string

//go:embed retain-system.sh
var retainSystemScript string

func (g guest) prepareCopy(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	password := strings.NewReader(g.password + "\n")
	if _, err := g.runSSH(ctx, g.address, ssh.PublicKeys(g.signer), configureCopyScript, password); err != nil {
		return fmt.Errorf("authorize closure import: %w", err)
	}
	return g.waitNix(ctx)
}

func (g guest) copySystem(ctx context.Context, stdout, stderr io.Writer) error {
	query := url.Values{
		"remote-program": {"/nix/var/nix/profiles/default/bin/nix-daemon"},
		"remote-store":   {"daemon"},
	}
	destination := url.URL{
		Scheme: "ssh-ng", User: url.User(g.user), Host: g.address, RawQuery: query.Encode(),
	}

	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, "nix",
		"--extra-experimental-features", "nix-command",
		"--option", "post-build-hook", "",
		"copy", "--no-check-sigs", "--to", destination.String(), "--", g.system,
	)
	command.Dir = g.directory
	// Every option uses fixed filenames or whitespace-free values. The working
	// directory carries the state path without passing it through NIX_SSHOPTS.
	command.Env = append(os.Environ(), "NIX_SSHOPTS="+strings.Join(sshOptions(), " "))
	command.Stdout = stdout
	command.Stderr = stderr
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error {
		return syscall.Kill(-command.Process.Pid, syscall.SIGTERM)
	}
	command.WaitDelay = 10 * time.Second
	if err := command.Run(); err != nil {
		return fmt.Errorf("copy system closure: %w", err)
	}

	input := strings.NewReader(g.system + "\n")
	if _, err := g.runSSH(ctx, g.address, ssh.PublicKeys(g.signer), retainSystemScript, input); err != nil {
		return fmt.Errorf("retain copied system: %w", err)
	}
	return nil
}
