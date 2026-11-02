package main

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

type nixInstaller struct {
	Type    string `json:"type"`
	Version string `json:"version"`
	Path    string `json:"path"`
}

var nixVersionPattern = regexp.MustCompile(`^[0-9]+(\.[0-9]+)+$`)

//go:embed stage-nix-installer.sh
var stageNixInstallerScript string

//go:embed install-nix.sh
var installNixScript string

//go:embed wait-nix.sh
var waitNixScript string

const queryNixVersion = `if [ -x /run/current-system/sw/bin/nix ]; then
    /run/current-system/sw/bin/nix --version
elif [ -x /nix/var/nix/profiles/default/bin/nix ]; then
    /nix/var/nix/profiles/default/bin/nix --version
fi`

func (installer nixInstaller) validate() error {
	if installer.Type != "nix" {
		return fmt.Errorf("unsupported nixInstaller.type %q; expected nix", installer.Type)
	}
	if !nixVersionPattern.MatchString(installer.Version) {
		return fmt.Errorf("invalid nixInstaller.version %q", installer.Version)
	}
	if installer.Path == "" {
		return errors.New("nixInstaller.path is required")
	}
	return nil
}

func (g guest) ensureNix(ctx context.Context) (systemFacts, error) {
	if err := g.waitNix(ctx); err != nil {
		return systemFacts{}, err
	}
	facts, err := g.inspect(ctx)
	if err != nil {
		return systemFacts{}, err
	}
	if facts.NixVersion == "" {
		if err := g.installNix(ctx); err != nil {
			return systemFacts{}, err
		}
		if err := g.waitNix(ctx); err != nil {
			return systemFacts{}, err
		}
		facts, err = g.inspect(ctx)
		if err != nil {
			return systemFacts{}, err
		}
	}
	// The installer pin applies until nix-darwin takes over Nix management.
	if facts.CurrentSystem == "" && facts.NixVersion != g.installer.Version {
		return systemFacts{}, fmt.Errorf("guest has Nix %q, requested %s; choose a fresh stateDirectory", facts.NixVersion, g.installer.Version)
	}

	return facts, nil
}

func (g guest) waitNix(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if _, err := g.runSSH(ctx, g.address, ssh.PublicKeys(g.signer), waitNixScript, nil); err != nil {
		return fmt.Errorf("wait for Nix: %w", err)
	}
	return nil
}

func (g guest) stageNixInstaller(ctx context.Context) (string, error) {
	archive, err := os.Open(g.installer.Path)
	if err != nil {
		return "", err
	}
	defer archive.Close()

	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	output, err := g.runSSH(ctx, g.address, ssh.PublicKeys(g.signer), stageNixInstallerScript, archive)
	if err != nil {
		return "", err
	}
	directory := strings.TrimSpace(string(output))
	if directory == "" || strings.ContainsAny(directory, "\r\n") {
		return "", errors.New("installer transfer returned an invalid guest directory")
	}
	return directory, nil
}

func (g guest) installNix(ctx context.Context) error {
	directory, err := g.stageNixInstaller(ctx)
	if err != nil {
		return fmt.Errorf("transfer Nix installer: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	// Send the directory and password as data, never as shell arguments.
	input := strings.NewReader(directory + "\n" + g.password + "\n")
	if _, err := g.runSSH(ctx, g.address, ssh.PublicKeys(g.signer), installNixScript, input); err != nil {
		return fmt.Errorf("install Nix: %w", err)
	}
	return nil
}
