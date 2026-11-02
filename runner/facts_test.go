package main

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
)

func TestInspectHost(t *testing.T) {
	tartFixture(t)
	t.Setenv("NIX_DARWIN_VM_TEST_HOST_VERSION", "27.3.1")
	facts, err := inspectHost(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if facts.MacOSVersion != "27.3.1" {
		t.Fatalf("unexpected host facts: %+v", facts)
	}
}

func TestInspectGuestReturnsFreshObservations(t *testing.T) {
	g, s := sshFixture(t)
	g.address = "192.0.2.10:22"
	if err := g.bootstrap(t.Context(), g.address); err != nil {
		t.Fatal(err)
	}
	vm, err := readFakeVM(s.directory)
	if err != nil {
		t.Fatal(err)
	}
	vm.MacOSVersion = "27.3.1"
	vm.NixVersion = "2.34.0"
	if err := saveFakeVM(s.directory, vm); err != nil {
		t.Fatal(err)
	}
	before, err := g.inspect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if before.MacOSVersion != "27.3.1" || before.NixVersion != "2.34.0" {
		t.Fatalf("guest inspection used the requested image version: %+v", before)
	}
	vm.MacOSVersion = "27.4"
	vm.NixVersion = "2.35.4"
	if err := saveFakeVM(s.directory, vm); err != nil {
		t.Fatal(err)
	}
	after, err := g.inspect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if after.MacOSVersion != "27.4" || before.MacOSVersion != "27.3.1" ||
		after.NixVersion != "2.35.4" || before.NixVersion != "2.34.0" {
		t.Fatalf("guest facts were cached or modified: before=%+v, after=%+v", before, after)
	}
}

func TestInspectGuestReportsCommandFailure(t *testing.T) {
	g, _ := sshFixture(t)
	g.address = "192.0.2.10:22"
	if err := g.bootstrap(t.Context(), g.address); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NIX_DARWIN_VM_TEST_FAIL", "inspect")
	if _, err := g.inspect(t.Context()); err == nil || !strings.Contains(err.Error(), "SSH command") {
		t.Fatalf("failed inspection returned usable facts or failed before command execution: %v", err)
	}
}

func TestInvalidHostFactsDoNotCreateVMState(t *testing.T) {
	config := tartFixture(t)
	t.Setenv("NIX_DARWIN_VM_TEST_HOST_VERSION", "invalid")
	err := config.run(t.Context(), io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "inspect host: invalid macOS version") {
		t.Fatalf("unexpected host inspection error: %v", err)
	}
	if _, err := os.Stat(config.StateDirectory); !os.IsNotExist(err) {
		t.Fatalf("invalid host facts created VM state: %v", err)
	}
}

func TestGuestInspectionFailureStopsVM(t *testing.T) {
	config := tartFixture(t)
	t.Setenv("NIX_DARWIN_VM_TEST_GUEST_VERSION", "invalid")
	var output bytes.Buffer
	err := config.run(t.Context(), &output, &output)
	if err == nil || !strings.Contains(err.Error(), "inspect guest: invalid macOS version") {
		t.Fatalf("unexpected guest inspection error: %v", err)
	}
	if strings.Contains(output.String(), "Ready:") {
		t.Fatal("failed guest inspection reported readiness")
	}
	vm, err := readFakeVM(config.StateDirectory)
	if err != nil || vm.State != "stopped" {
		t.Fatalf("failed guest inspection left the VM running: %+v, %v", vm, err)
	}
}
