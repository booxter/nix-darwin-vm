package main

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

type systemFacts struct {
	MacOSVersion  string
	NixVersion    string
	CurrentSystem string
}

const queryCurrentSystem = `if [ -L /run/current-system ]; then
    /usr/bin/readlink /run/current-system
fi`

// Fact queries are fixed shell commands, interpreted locally or over SSH.
type commandExecutor func(context.Context, string) ([]byte, error)

func inspectSystem(ctx context.Context, execute commandExecutor) (systemFacts, error) {
	output, err := execute(ctx, "sw_vers -productVersion")
	if err != nil {
		return systemFacts{}, err
	}
	version, err := readMacOSVersion(output)
	if err != nil {
		return systemFacts{}, err
	}
	return systemFacts{MacOSVersion: version}, nil
}

func localCommand(ctx context.Context, command string) ([]byte, error) {
	return exec.CommandContext(ctx, "/bin/sh", "-c", command).Output()
}

func inspectHost(ctx context.Context) (systemFacts, error) {
	facts, err := inspectSystem(ctx, localCommand)
	if err != nil {
		return systemFacts{}, fmt.Errorf("inspect host: %w", err)
	}
	return facts, nil
}

func (g guest) inspect(ctx context.Context) (systemFacts, error) {
	if g.address == "" {
		return systemFacts{}, errors.New("cannot inspect guest: address is unknown")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	execute := func(ctx context.Context, command string) ([]byte, error) {
		return g.runSSH(ctx, g.address, ssh.PublicKeys(g.signer), command, nil)
	}
	facts, err := inspectSystem(ctx, execute)
	if err != nil {
		return systemFacts{}, fmt.Errorf("inspect guest: %w", err)
	}
	output, err := execute(ctx, queryCurrentSystem)
	if err != nil {
		return systemFacts{}, fmt.Errorf("inspect guest system: %w", err)
	}
	facts.CurrentSystem = strings.TrimSpace(string(output))
	output, err = execute(ctx, queryNixVersion)
	if err != nil {
		return systemFacts{}, fmt.Errorf("inspect guest Nix: %w", err)
	}
	if version := strings.TrimSpace(string(output)); version != "" {
		version, ok := strings.CutPrefix(version, "nix (Nix) ")
		if !ok || !nixVersionPattern.MatchString(version) {
			return systemFacts{}, fmt.Errorf("invalid guest Nix version %q", output)
		}
		facts.NixVersion = version
	}
	return facts, nil
}

func readMacOSVersion(output []byte) (string, error) {
	version := strings.TrimSpace(string(output))
	for _, component := range strings.Split(version, ".") {
		value, err := strconv.Atoi(component)
		if err != nil || value < 0 {
			return "", fmt.Errorf("invalid macOS version %q", version)
		}
	}
	return version, nil
}

func (g guest) checkProvisioningSupport(host systemFacts) error {
	if err := requireProvisioningVersion("host", host.MacOSVersion); err != nil {
		return err
	}
	return requireProvisioningVersion("guest", g.imageVersion)
}

func requireProvisioningVersion(system, version string) error {
	major, _, _ := strings.Cut(version, ".")
	value, err := strconv.Atoi(major)
	if err != nil || value < 27 {
		return fmt.Errorf("guest provisioning requires macOS 27 or newer on the %s (got %q)", system, version)
	}
	return nil
}
