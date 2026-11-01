package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

type guest struct {
	directory    string
	user         string
	imageVersion string
	installer    nixInstaller
	system       string
	address      string
	password     string
	signer       ssh.Signer
	configured   bool
	dialer       sshDialer
}

func newGuest(config configuration, dialer sshDialer) guest {
	return guest{
		directory:    filepath.Join(config.StateDirectory, "credentials"),
		user:         config.BootstrapUser,
		imageVersion: config.MacOSVersion,
		installer:    config.NixInstaller,
		system:       config.System,
		dialer:       dialer,
	}
}

func (g *guest) prepare(existingVM bool) error {
	if _, err := os.Stat(g.directory); errors.Is(err, os.ErrNotExist) {
		if existingVM {
			return errors.New("VM has no bootstrap credentials; choose a fresh stateDirectory for guest provisioning")
		}
		if err := createCredentials(filepath.Dir(g.directory), g.directory, g.user); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}

	savedUser, err := os.ReadFile(filepath.Join(g.directory, "username"))
	if err != nil {
		return fmt.Errorf("read bootstrap username: %w", err)
	}
	if string(savedUser) != g.user {
		return fmt.Errorf("VM bootstrap user is %q, requested %q; choose a fresh stateDirectory", savedUser, g.user)
	}

	password, err := os.ReadFile(filepath.Join(g.directory, "password"))
	if err != nil {
		return fmt.Errorf("read bootstrap password: %w", err)
	}
	g.password = string(password)
	// Tart's comma-separated provisioning options have no escaping for commas.
	if g.password == "" || strings.ContainsAny(g.password, ",\r\n") {
		return errors.New("invalid bootstrap password file")
	}
	privateKey, err := os.ReadFile(filepath.Join(g.directory, "ssh_key"))
	if err != nil {
		return fmt.Errorf("read bootstrap private key: %w", err)
	}
	g.signer, err = ssh.ParsePrivateKey(privateKey)
	if err != nil {
		return fmt.Errorf("parse bootstrap private key: %w", err)
	}
	_, err = os.Stat(filepath.Join(g.directory, "provisioned"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	g.configured = err == nil
	return nil
}

func createCredentials(stateDirectory, destination, user string) error {
	// Publish credentials together, before a VM can consume them on first boot.
	directory, err := os.MkdirTemp(stateDirectory, ".credentials-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(directory)
	if err := os.WriteFile(filepath.Join(directory, "username"), []byte(user), 0600); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(directory, "password"), []byte(rand.Text()), 0600); err != nil {
		return err
	}
	if err := writeSSHKeyPair(directory); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(directory, "known_hosts"), nil, 0600); err != nil {
		return err
	}
	return os.Rename(directory, destination)
}

func writeSSHKeyPair(directory string) error {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	block, err := ssh.MarshalPrivateKey(privateKey, "nix-darwin-vm")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(directory, "ssh_key"), pem.EncodeToMemory(block), 0600); err != nil {
		return err
	}
	key, err := ssh.NewPublicKey(publicKey)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(directory, "ssh_key.pub"), ssh.MarshalAuthorizedKey(key), 0644)
}

func (g *guest) waitReady(ctx context.Context, t tart) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	var lastError error
	for {
		ip, err := t.ip(ctx)
		lastError = err
		if err == nil {
			address := ip.String()
			g.address = net.JoinHostPort(address, "22")
			if g.configured {
				lastError = g.connect(ctx, g.address)
			} else {
				lastError = g.bootstrap(ctx, g.address)
			}
			if lastError == nil {
				if err := os.WriteFile(filepath.Join(g.directory, "provisioned"), nil, 0600); err != nil {
					return err
				}
				g.configured = true
				return nil
			}
		}
		var addressError *net.ParseError
		var keyError *knownhosts.KeyError
		if errors.As(lastError, &addressError) || errors.As(lastError, &keyError) {
			return lastError
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for guest SSH: %w (last attempt: %v)", ctx.Err(), lastError)
		case <-time.After(time.Second):
		}
	}
}
