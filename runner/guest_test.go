package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestProvisioningRequiresMacOS27(t *testing.T) {
	for _, platform := range []string{"host", "guest"} {
		t.Run(platform, func(t *testing.T) {
			config := tartFixture(t)
			if platform == "host" {
				t.Setenv("NIX_DARWIN_VM_TEST_HOST_VERSION", "26.6")
			} else {
				config.MacOSVersion = "26.6"
			}
			err := config.run(t.Context(), io.Discard, io.Discard)
			if err == nil || !strings.Contains(err.Error(), "macOS 27 or newer on the "+platform) {
				t.Fatalf("unexpected version error: %v", err)
			}
			if _, err := os.Stat(config.StateDirectory); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("unsupported provisioning created VM state: %v", err)
			}
		})
	}
}

func TestMissingCredentialsDoNotModifyVM(t *testing.T) {
	config := tartFixture(t)
	s := startTestVM(t, config)
	s.stop(t)
	original, err := readFakeVM(config.StateDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(config.StateDirectory, "credentials")); err != nil {
		t.Fatal(err)
	}
	err = config.run(t.Context(), io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "no bootstrap credentials") {
		t.Fatalf("missing credentials were not reported: %v", err)
	}
	after, err := readFakeVM(config.StateDirectory)
	if err != nil || after != original {
		t.Fatalf("existing VM was modified: %+v, %v", after, err)
	}
}

func TestInterruptedProvisioningCanResume(t *testing.T) {
	config := tartFixture(t)
	t.Setenv("NIX_DARWIN_VM_TEST_REJECT_PASSWORD", "1")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	var output bytes.Buffer
	go func() { done <- config.run(ctx, &output, &output) }()
	deadline := time.After(15 * time.Second)
	var initial fakeVM
	for initial.State != "running" {
		select {
		case err := <-done:
			t.Fatalf("VM exited before boot: %v", err)
		case <-deadline:
			cancel()
			<-done
			t.Fatal("VM did not boot")
		case <-time.After(10 * time.Millisecond):
			initial, _ = readFakeVM(config.StateDirectory)
		}
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("unexpected interrupted startup: %v", err)
	}
	if strings.Contains(output.String(), "Ready:") {
		t.Fatal("runner reported readiness without authenticated SSH")
	}
	if strings.Contains(output.String(), initial.Password) {
		t.Fatal("bootstrap password leaked into runner output")
	}
	for _, path := range []string{"credentials", "credentials/password", "credentials/ssh_key"} {
		info, err := os.Stat(filepath.Join(config.StateDirectory, path))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm()&0077 != 0 {
			t.Fatalf("credentials accessible to other users: %s", path)
		}
	}
	t.Setenv("NIX_DARWIN_VM_TEST_REJECT_PASSWORD", "")
	s := startTestVM(t, config)
	s.stop(t)
	after, err := readFakeVM(config.StateDirectory)
	if err != nil || after.Password != initial.Password || !after.KeyInstalled || after.Creations != 1 {
		t.Fatalf("interrupted provisioning did not recover: %+v, %v", after, err)
	}
}

func TestStartupFailureStopsVM(t *testing.T) {
	config := tartFixture(t)
	t.Setenv("NIX_DARWIN_VM_TEST_IP", "not an IP address")
	var output bytes.Buffer
	err := config.run(t.Context(), &output, &output)
	if err == nil || !strings.Contains(err.Error(), "invalid guest IP address") {
		t.Fatalf("unexpected startup failure: %v", err)
	}
	if strings.Contains(output.String(), "Ready:") {
		t.Fatal("failed startup reported readiness")
	}
	vm, err := readFakeVM(config.StateDirectory)
	if err != nil || vm.State != "stopped" || !vm.Interrupted {
		t.Fatalf("failed startup left Tart running: %+v, %v", vm, err)
	}
}

func TestCustomBootstrapUserAndRenameRejection(t *testing.T) {
	config := tartFixture(t)
	config.BootstrapUser = "builder"
	s := startTestVM(t, config)
	s.stop(t)
	original, err := readFakeVM(config.StateDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if original.User != "builder" || !original.KeyInstalled {
		t.Fatalf("custom account did not reach authenticated SSH: %+v", original)
	}
	config.BootstrapUser = "another-user"
	err = config.run(t.Context(), io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "fresh stateDirectory") {
		t.Fatalf("account rename was not rejected: %v", err)
	}
	after, err := readFakeVM(config.StateDirectory)
	if err != nil || after != original {
		t.Fatalf("account rename modified the VM: %+v, %v", after, err)
	}
}
