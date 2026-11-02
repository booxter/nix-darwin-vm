package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type sessionOutput struct {
	sync.Mutex
	buffer bytes.Buffer
	ready  chan struct{}
	once   sync.Once
}

func (o *sessionOutput) Write(data []byte) (int, error) {
	o.Lock()
	defer o.Unlock()
	n, err := o.buffer.Write(data)
	if strings.Contains(o.buffer.String(), "Ready:") {
		o.once.Do(func() { close(o.ready) })
	}
	return n, err
}

type testSession struct {
	cancel context.CancelFunc
	done   chan struct{}
	err    error
}

func startTestVM(t *testing.T, config vmFixture) *testSession {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	s := &testSession{cancel: cancel, done: make(chan struct{})}
	output := &sessionOutput{ready: make(chan struct{})}
	go func() {
		s.err = config.run(ctx, output, output)
		close(s.done)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-s.done:
		case <-time.After(15 * time.Second):
			t.Error("VM did not stop after cancellation")
		}
	})
	select {
	case <-output.ready:
	case <-s.done:
		t.Fatalf("VM exited before readiness: %v; output: %s", s.err, &output.buffer)
	case <-time.After(20 * time.Second):
		t.Fatal("VM did not become ready")
	}
	return s
}

func (s *testSession) stop(t *testing.T) {
	t.Helper()
	s.cancel()
	<-s.done
	if !errors.Is(s.err, context.Canceled) {
		t.Fatalf("unexpected cancellation result: %v", s.err)
	}
}

func TestCreateAndReuseVM(t *testing.T) {
	config := tartFixture(t)
	otherHome := t.TempDir()
	t.Setenv("TART_HOME", otherHome)
	s := startTestVM(t, config)
	s.stop(t)
	vm, err := readFakeVM(config.StateDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if vm.Cores != 2 || vm.Memory != 4096 || vm.Graphics || !vm.KeyInstalled || vm.Password == "" {
		t.Fatalf("unexpected initial VM: %+v", vm)
	}
	if vm.DiskSizeGB != 50 {
		t.Fatalf("default disk size = %d GB, want 50", vm.DiskSizeGB)
	}
	if vm.Copies != 1 || vm.CopiedSystem != config.System {
		t.Fatalf("guest configuration was not copied once: %+v", vm)
	}
	if vm.Activations != 1 || vm.CurrentSystem != config.System || vm.NixVersion != "2.34.9" {
		t.Fatalf("guest configuration was not activated: %+v", vm)
	}
	if _, err := os.Stat(filepath.Join(otherHome, "fake-vm.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("runner used the inherited TART_HOME: %v", err)
	}
	disk := filepath.Join(config.StateDirectory, "vms", vmName, "disk.img")
	if err := os.WriteFile(disk, []byte("guest changes"), 0600); err != nil {
		t.Fatal(err)
	}
	password := vm.Password
	config.Name = "renamed"
	config.IPSW = "/nix/store/another.ipsw"
	config.Cores = 4
	config.DiskSize = 64 * 1024
	config.Graphics = true
	config.System = "/nix/store/changed-system"
	// Reuse must authenticate with the key, without needing the password again.
	t.Setenv("NIX_DARWIN_VM_TEST_REJECT_PASSWORD", "1")
	s = startTestVM(t, config)
	s.stop(t)
	vm, err = readFakeVM(config.StateDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if vm.Creations != 1 || vm.Runs != 2 || vm.Cores != 4 || !vm.Graphics || vm.Password != password {
		t.Fatalf("unexpected reused VM: %+v", vm)
	}
	if vm.DiskSizeGB != 50 {
		t.Fatalf("existing disk size changed to %d GB", vm.DiskSizeGB)
	}
	if vm.Copies != 2 || vm.CopiedSystem != config.System {
		t.Fatalf("latest guest configuration was not copied once: %+v", vm)
	}
	if vm.Activations != 2 || vm.CurrentSystem != config.System {
		t.Fatalf("latest guest configuration was not activated: %+v", vm)
	}
	contents, err := os.ReadFile(disk)
	if err != nil || string(contents) != "guest changes" {
		t.Fatalf("guest disk was replaced: %q, %v", contents, err)
	}
}

func TestCustomDiskSize(t *testing.T) {
	config := tartFixture(t)
	config.DiskSize = 64 * 1024
	s := startTestVM(t, config)
	s.stop(t)
	vm, err := readFakeVM(config.StateDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if vm.DiskSizeGB != 69 {
		t.Fatalf("disk size = %d GB, want 69 for 64 GiB rounded up", vm.DiskSizeGB)
	}
}

func TestTartFailuresAreReported(t *testing.T) {
	for _, operation := range []string{"list", "create", "set", "run"} {
		t.Run(operation, func(t *testing.T) {
			config := tartFixture(t)
			t.Setenv("NIX_DARWIN_VM_TEST_FAIL", operation)
			err := config.run(t.Context(), io.Discard, io.Discard)
			if err == nil || exitCode(err) != 17 {
				t.Fatalf("got %v, want Tart exit status 17", err)
			}
		})
	}
}

func TestRunningVMIsNotChanged(t *testing.T) {
	config := tartFixture(t)
	if err := os.MkdirAll(config.StateDirectory, 0700); err != nil {
		t.Fatal(err)
	}
	original := fakeVM{Name: "vm", State: "running", Cores: 8}
	if err := saveFakeVM(config.StateDirectory, original); err != nil {
		t.Fatal(err)
	}
	err := config.run(t.Context(), io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "VM is running") {
		t.Fatalf("unexpected error: %v", err)
	}
	after, err := readFakeVM(config.StateDirectory)
	if err != nil || after != original {
		t.Fatalf("running VM was changed: %+v, %v", after, err)
	}
}

func TestCancellationWaitsForTartAndReleasesState(t *testing.T) {
	config := tartFixture(t)
	s := startTestVM(t, config)
	err := config.run(t.Context(), io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "already in use") {
		t.Fatalf("concurrent invocation was not rejected: %v", err)
	}
	s.stop(t)
	vm, err := readFakeVM(config.StateDirectory)
	if err != nil || !vm.Interrupted || vm.State != "stopped" {
		t.Fatalf("Tart was not interrupted and reaped: %+v, %v", vm, err)
	}
	s = startTestVM(t, config)
	s.stop(t)
}

func TestRunnerPreservesTartExitStatus(t *testing.T) {
	config := tartFixture(t)
	t.Setenv("NIX_DARWIN_VM_TEST_EXIT", "23")
	data, err := json.Marshal(config.configuration)
	if err != nil {
		t.Fatal(err)
	}
	path := writeConfiguration(t, string(data))
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(t.Context(), executable, "--config", path)
	output, err := command.CombinedOutput()
	var exitError *exec.ExitError
	if !errors.As(err, &exitError) || exitError.ExitCode() != 23 {
		t.Fatalf("got %v, want exit status 23; output: %s", err, output)
	}
}
