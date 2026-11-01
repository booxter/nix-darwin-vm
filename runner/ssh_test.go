package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

type sshTestServer struct {
	listener       net.Listener
	config         *ssh.ServerConfig
	directory      string
	active         sync.Map
	workers        sync.WaitGroup
	nixPending     atomic.Bool
	storeDirectory string
	storeBytes     atomic.Int64
	storeBlocked   atomic.Bool
}

func newSSHTestServer(t *testing.T, directory string) *sshTestServer {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &sshTestServer{listener: listener, directory: directory}
	s.config = &ssh.ServerConfig{
		PasswordCallback:  s.checkPassword,
		PublicKeyCallback: s.checkPublicKey,
	}
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	s.config.AddHostKey(signer)
	ecdsaKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ecdsaSigner, err := ssh.NewSignerFromKey(ecdsaKey)
	if err != nil {
		t.Fatal(err)
	}
	s.config.AddHostKey(ecdsaSigner)
	acceptDone := make(chan struct{})
	go func() {
		defer close(acceptDone)
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			s.active.Store(connection, true)
			s.workers.Add(1)
			go func() {
				defer s.workers.Done()
				defer s.active.Delete(connection)
				defer connection.Close()
				s.serve(connection)
			}()
		}
	}()
	t.Cleanup(func() {
		listener.Close()
		<-acceptDone
		s.active.Range(func(key, _ any) bool {
			key.(net.Conn).Close()
			return true
		})
		s.workers.Wait()
	})
	return s
}

func (s *sshTestServer) DialContext(ctx context.Context, network, _ string) (net.Conn, error) {
	return (&net.Dialer{}).DialContext(ctx, network, s.listener.Addr().String())
}

func (s *sshTestServer) checkPassword(connection ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
	vm, err := readFakeVM(s.directory)
	if err == nil && vm.State == "running" && connection.User() == vm.User &&
		vm.Password != "" && string(password) == vm.Password && os.Getenv("NIX_DARWIN_VM_TEST_REJECT_PASSWORD") != "1" {
		return nil, nil
	}
	return nil, errors.New("password authentication rejected")
}

func (s *sshTestServer) checkPublicKey(connection ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
	vm, err := readFakeVM(s.directory)
	if err != nil || connection.User() != vm.User {
		return nil, errors.New("unknown SSH user")
	}
	data, err := os.ReadFile(filepath.Join(s.directory, "authorized_keys"))
	if err == nil {
		authorized, _, _, _, err := ssh.ParseAuthorizedKey(data)
		if err == nil && bytes.Equal(authorized.Marshal(), key.Marshal()) {
			return nil, nil
		}
	}
	return nil, errors.New("public key authentication rejected")
}

func (s *sshTestServer) serve(connection net.Conn) {
	transport, channels, requests, err := ssh.NewServerConn(connection, s.config)
	if err != nil {
		return
	}
	defer transport.Close()
	go ssh.DiscardRequests(requests)
	for channel := range channels {
		if channel.ChannelType() != "session" {
			channel.Reject(ssh.UnknownChannelType, "session required")
			continue
		}
		session, requests, err := channel.Accept()
		if err != nil {
			return
		}
		s.serveSession(session, requests)
	}
}

func (s *sshTestServer) serveSession(channel ssh.Channel, requests <-chan *ssh.Request) {
	defer channel.Close()
	for request := range requests {
		if request.Type != "exec" {
			request.Reply(false, nil)
			continue
		}
		var payload struct{ Command string }
		if err := ssh.Unmarshal(request.Payload, &payload); err != nil {
			request.Reply(false, nil)
			return
		}
		request.Reply(true, nil)
		var status uint32
		switch payload.Command {
		case "true":
		case "exit 23":
			status = 23
		case "sw_vers -productVersion":
			if os.Getenv("NIX_DARWIN_VM_TEST_FAIL") == "inspect" {
				status = 1
				break
			}
			vm, err := readFakeVM(s.directory)
			if err != nil {
				status = 1
			} else {
				fmt.Fprintln(channel, vm.MacOSVersion)
			}
		case queryNixVersion:
			vm, err := readFakeVM(s.directory)
			if err != nil {
				status = 1
			} else if vm.NixVersion != "" && !s.nixPending.Load() {
				fmt.Fprintln(channel, "nix (Nix)", vm.NixVersion)
			}
		case queryCurrentSystem:
			vm, err := readFakeVM(s.directory)
			if err != nil {
				status = 1
			} else {
				fmt.Fprintln(channel, vm.CurrentSystem)
			}
		case waitNixScript:
			for s.nixPending.Load() {
				time.Sleep(10 * time.Millisecond)
			}
			vm, err := readFakeVM(s.directory)
			if err != nil || (vm.NixVersion != "" && os.Getenv("NIX_DARWIN_VM_TEST_FAIL") == "nix-daemon") {
				status = 1
			}
		case stageNixInstallerScript, installNixScript:
			status = s.runScript(channel, payload.Command)
		case configureCopyScript:
			if err := s.authorizeCopy(channel); err != nil {
				fmt.Fprintln(channel.Stderr(), err)
				status = 1
			}
		case retainSystemScript:
			if err := s.retainSystem(channel); err != nil {
				fmt.Fprintln(channel.Stderr(), err)
				status = 1
			}
		case activateSystemScript:
			if err := s.activateSystem(channel); err != nil {
				fmt.Fprintln(channel.Stderr(), err)
				status = 1
			}
		case installPublicKey:
			if err := s.installKey(channel); err != nil {
				status = 1
			}
		default:
			if s.storeDirectory != "" && strings.HasPrefix(payload.Command, "/nix/var/nix/profiles/default/bin/nix-daemon --stdio") {
				status = s.serveStore(channel)
			} else {
				fmt.Fprintln(channel.Stderr(), "unsupported fixture command")
				status = 127
			}
		}
		channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{status}))
		return
	}
}

func (s *sshTestServer) activateSystem(channel ssh.Channel) error {
	input, err := io.ReadAll(channel)
	if err != nil {
		return err
	}
	system, password, ok := strings.Cut(string(input), "\n")
	vm, err := readFakeVM(s.directory)
	if err != nil {
		return err
	}
	if !ok || strings.TrimSuffix(password, "\n") != vm.Password {
		return errors.New("sudo: incorrect password")
	}
	if os.Getenv("NIX_DARWIN_VM_TEST_FAIL") == "activate" {
		fmt.Fprintln(channel, "activation started")
		return errors.New("fixture activation failed")
	}
	if vm.CopiedSystem != system {
		return errors.New("configuration has not been copied")
	}
	vm.Activations++
	if os.Getenv("NIX_DARWIN_VM_TEST_FAIL") != "activation-system" {
		vm.CurrentSystem = system
	}
	vm.NixVersion = "2.34.9"
	if os.Getenv("NIX_DARWIN_VM_TEST_FAIL") == "activation-nix" {
		vm.NixVersion = ""
	}
	fmt.Fprintln(channel, "setting up /etc...")
	return saveFakeVM(s.directory, vm)
}

func (s *sshTestServer) authorizeCopy(reader io.Reader) error {
	password, err := io.ReadAll(reader)
	if err != nil {
		return err
	}
	vm, err := readFakeVM(s.directory)
	if err != nil {
		return err
	}
	if strings.TrimSuffix(string(password), "\n") != vm.Password || os.Getenv("NIX_DARWIN_VM_TEST_FAIL") == "copy-trust" {
		return errors.New("closure import authorization failed")
	}
	vm.CopyTrusted = true
	return saveFakeVM(s.directory, vm)
}

func (s *sshTestServer) retainSystem(reader io.Reader) error {
	data, err := io.ReadAll(reader)
	if err != nil {
		return err
	}
	if os.Getenv("NIX_DARWIN_VM_TEST_FAIL") == "retain" {
		return errors.New("fixture profile update failed")
	}
	system := strings.TrimSpace(string(data))
	if s.storeDirectory != "" {
		command := exec.Command("nix-env", "--option", "post-build-hook", "",
			"--store", localStoreURI(s.storeDirectory),
			"--profile", filepath.Join(s.directory, "copied-profile"), "--set", system)
		if output, err := command.CombinedOutput(); err != nil {
			return fmt.Errorf("set fixture profile: %w: %s", err, output)
		}
	}
	vm, err := readFakeVM(s.directory)
	if err != nil {
		return err
	}
	vm.CopiedSystem = system
	return saveFakeVM(s.directory, vm)
}

type countedReader struct {
	reader io.Reader
	bytes  *atomic.Int64
}

func (r countedReader) Read(data []byte) (int, error) {
	n, err := r.reader.Read(data)
	r.bytes.Add(int64(n))
	return n, err
}

func (s *sshTestServer) serveStore(channel ssh.Channel) uint32 {
	input := countedReader{reader: channel, bytes: &s.storeBytes}
	if s.storeBlocked.Load() {
		io.Copy(io.Discard, input)
		return 1
	}
	command := exec.Command("nix-daemon", "--stdio", "--store", localStoreURI(s.storeDirectory))
	command.Stdin = input
	command.Stdout = channel
	command.Stderr = channel.Stderr()
	return uint32(helperExitCode(command.Run()))
}

func (s *sshTestServer) runScript(channel ssh.Channel, script string) uint32 {
	command := exec.Command("/bin/sh", "-c", script)
	command.Env = append(os.Environ(),
		"NIX_DARWIN_VM_TEST_VM_STATE="+s.directory,
		"TMPDIR="+s.directory,
	)
	command.Stdin = channel
	command.Stdout = channel
	command.Stderr = channel.Stderr()
	return uint32(helperExitCode(command.Run()))
}

func (s *sshTestServer) installKey(reader io.Reader) error {
	data, err := io.ReadAll(reader)
	if err != nil {
		return err
	}
	if _, _, _, _, err := ssh.ParseAuthorizedKey(data); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(s.directory, "authorized_keys"), data, 0600); err != nil {
		return err
	}
	vm, err := readFakeVM(s.directory)
	if err != nil {
		return err
	}
	vm.KeyInstalled = true
	return saveFakeVM(s.directory, vm)
}

func sshFixture(t *testing.T) (guest, *sshTestServer) {
	t.Helper()
	fakeCommands(t)
	directory := t.TempDir()
	s := newSSHTestServer(t, directory)
	g := newGuest(configuration{StateDirectory: directory, BootstrapUser: "builder", MacOSVersion: "27.2"}, s)
	if err := g.prepare(false); err != nil {
		t.Fatal(err)
	}
	if err := saveFakeVM(directory, fakeVM{Name: vmName, State: "running", User: g.user, Password: g.password, MacOSVersion: "27.2"}); err != nil {
		t.Fatal(err)
	}
	return g, s
}

func TestNativeSSHConnectRequiresInstalledKey(t *testing.T) {
	g, _ := sshFixture(t)
	if err := g.connect(t.Context(), "192.0.2.10:22"); err == nil {
		t.Fatal("connected without an installed key")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(g.directory), "authorized_keys")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("connection attempt installed a key: %v", err)
	}
}

func TestNativeSSHBootstrapAndKeyReuse(t *testing.T) {
	g, _ := sshFixture(t)
	if err := g.bootstrap(t.Context(), "192.0.2.10:22"); err != nil {
		t.Fatal(err)
	}
	// A different bootstrap password must not matter once the key is installed.
	g.password = "wrong-password"
	if err := g.connect(t.Context(), "192.0.2.11:22"); err != nil {
		t.Fatal(err)
	}
	// Retrying before the provisioning marker is saved also reuses the key.
	if err := g.bootstrap(t.Context(), "192.0.2.11:22"); err != nil {
		t.Fatal(err)
	}
}

func TestNativeSSHRejectsWrongPassword(t *testing.T) {
	g, _ := sshFixture(t)
	g.password = "wrong-password"
	if err := g.bootstrap(t.Context(), "192.0.2.10:22"); err == nil {
		t.Fatal("wrong password authenticated")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(g.directory), "authorized_keys")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed authentication installed a key: %v", err)
	}
}

func TestNativeSSHRejectsChangedHostKey(t *testing.T) {
	g, _ := sshFixture(t)
	if err := g.bootstrap(t.Context(), "192.0.2.10:22"); err != nil {
		t.Fatal(err)
	}
	g.dialer = newSSHTestServer(t, filepath.Dir(g.directory))
	err := g.connect(t.Context(), "192.0.2.10:22")
	var keyError *knownhosts.KeyError
	if !errors.As(err, &keyError) || len(keyError.Want) == 0 {
		t.Fatalf("changed host key was not rejected: %v", err)
	}
	err = g.bootstrap(t.Context(), "192.0.2.10:22")
	if !errors.As(err, &keyError) || len(keyError.Want) == 0 {
		t.Fatalf("bootstrap did not reject changed host key: %v", err)
	}
}

func TestNativeSSHWaitsForBannerAndCancels(t *testing.T) {
	g, _ := sshFixture(t)
	g.dialer = &net.Dialer{}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	accepted := make(chan net.Conn, 1)
	go func() {
		connection, _ := listener.Accept()
		accepted <- connection
	}()
	var connection net.Conn
	t.Cleanup(func() {
		listener.Close()
		if connection == nil {
			connection = <-accepted
		}
		if connection != nil {
			connection.Close()
		}
	})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- g.connect(ctx, listener.Addr().String()) }()
	select {
	case connection = <-accepted:
		if connection == nil {
			t.Fatal("TCP connection was not accepted")
		}
	case <-time.After(time.Second):
		t.Fatal("SSH client did not connect")
	}
	select {
	case err := <-done:
		t.Fatalf("connection finished without an SSH banner: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled handshake succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation did not interrupt the banner wait")
	}
}
