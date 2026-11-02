package main

import (
	"context"
	_ "embed"
	"fmt"
	"io"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

//go:embed activate-system.sh
var activateSystemScript string

func (g guest) activateSystem(ctx context.Context, stdout, stderr io.Writer) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	input := strings.NewReader(g.system + "\n" + g.password + "\n")
	if err := g.executeSSH(ctx, g.address, ssh.PublicKeys(g.signer), activateSystemScript, input, stdout, stderr); err != nil {
		return fmt.Errorf("activate guest system: %w", err)
	}
	return nil
}

func (g guest) verifySystem(ctx context.Context) (systemFacts, error) {
	if err := g.waitNix(ctx); err != nil {
		return systemFacts{}, err
	}
	facts, err := g.inspect(ctx)
	if err != nil {
		return systemFacts{}, err
	}
	if facts.CurrentSystem != g.system {
		return systemFacts{}, fmt.Errorf("guest activated %q, expected %s", facts.CurrentSystem, g.system)
	}
	if facts.NixVersion == "" {
		return systemFacts{}, fmt.Errorf("Nix is unavailable after activation")
	}
	return facts, nil
}
