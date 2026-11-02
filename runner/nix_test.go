package main

import (
	"archive/tar"
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func nixArchive(t *testing.T, version string) string {
	t.Helper()
	var archive bytes.Buffer
	writer := tar.NewWriter(&archive)
	root := "nix-" + version + "-aarch64-darwin/"
	if err := writer.WriteHeader(&tar.Header{
		Name: root, Typeflag: tar.TypeDir, Mode: 0755,
	}); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\nexec install-nix-fixture " + version + " \"$@\"\n"
	if err := writer.WriteHeader(&tar.Header{
		Name: root + "install",
		Mode: 0755, Size: int64(len(script)),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte(script)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	compress := exec.Command("xz", "-c")
	compress.Stdin = &archive
	compressed, err := compress.Output()
	if err != nil {
		t.Fatal(err)
	}
	filename := filepath.Join(t.TempDir(), "installer.tar.xz")
	if err := os.WriteFile(filename, compressed, 0600); err != nil {
		t.Fatal(err)
	}
	return filename
}

func nixFixture(t *testing.T) (guest, *sshTestServer) {
	t.Helper()
	g, server := sshFixture(t)
	g.address = "192.0.2.10:22"
	g.installer = nixInstaller{Type: "nix", Version: "2.35.4", Path: nixArchive(t, "2.35.4")}
	if err := g.bootstrap(t.Context(), g.address); err != nil {
		t.Fatal(err)
	}
	return g, server
}

func TestNixArchiveIsTransferredAndExtractedPrivately(t *testing.T) {
	g, _ := nixFixture(t)
	directory, err := g.stageNixInstaller(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(directory)
	info, err := os.Stat(directory)
	if err != nil || info.Mode().Perm()&0077 != 0 {
		t.Fatalf("staged installer is not private: %v", err)
	}
	script, err := os.ReadFile(filepath.Join(directory, "install"))
	if err != nil || !strings.Contains(string(script), "install-nix-fixture 2.35.4") {
		t.Fatalf("archive contents were not delivered: %q, %v", script, err)
	}
	info, err = os.Stat(filepath.Join(directory, "install"))
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0755 {
		t.Fatalf("archive permissions were not preserved: got %o, want 755", mode)
	}
}

func TestNixInstallationAndReuse(t *testing.T) {
	g, server := nixFixture(t)
	facts, err := g.ensureNix(t.Context())
	if err != nil || facts.NixVersion != "2.35.4" {
		t.Fatalf("Nix was not installed and observed: %+v, %v", facts, err)
	}

	// Reuse must not require the original archive or the sudo password.
	g.installer.Path = "/missing/installer.tar.xz"
	g.password = "wrong-password"
	if _, err := g.ensureNix(t.Context()); err != nil {
		t.Fatal(err)
	}
	vm, err := readFakeVM(server.directory)
	if err != nil || vm.NixInstallations != 1 {
		t.Fatalf("Nix was reinstalled: %+v, %v", vm, err)
	}
	assertNoStagedInstaller(t, server.directory)
}

func TestNixInstallationFailuresStopVM(t *testing.T) {
	tests := []struct {
		name           string
		failure        string
		version        string
		corruptArchive bool
		want           string
	}{
		{name: "transfer", corruptArchive: true, want: "transfer Nix installer"},
		{name: "installer", failure: "install-nix", want: "fixture Nix installer failed"},
		{name: "daemon", failure: "nix-daemon", want: "wait for Nix"},
		{name: "version", version: "2.34.0", want: "requested 2.34.0"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := tartFixture(t)
			t.Setenv("NIX_DARWIN_VM_TEST_FAIL", test.failure)
			if test.corruptArchive {
				if err := os.WriteFile(config.NixInstaller.Path, []byte("invalid archive"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if test.version != "" {
				config.NixInstaller.Version = test.version
			}
			var output bytes.Buffer
			err := config.run(t.Context(), &output, &output)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("got %v, want %q", err, test.want)
			}
			if strings.Contains(output.String(), "Ready:") {
				t.Fatal("failed installation reported readiness")
			}
			vm, err := readFakeVM(config.StateDirectory)
			if err != nil || vm.State != "stopped" || !vm.Interrupted {
				t.Fatalf("failed installation left VM running: %+v, %v", vm, err)
			}
			assertNoStagedInstaller(t, config.StateDirectory)
		})
	}
}

func TestNixInstallationRejectsWrongSudoPassword(t *testing.T) {
	g, server := nixFixture(t)
	g.password = "wrong-password"
	_, err := g.ensureNix(t.Context())
	if err == nil || !strings.Contains(err.Error(), "sudo: incorrect password") {
		t.Fatalf("wrong sudo password was not rejected: %v", err)
	}
	if strings.Contains(err.Error(), g.password) {
		t.Fatal("sudo password leaked into the error")
	}
	vm, err := readFakeVM(server.directory)
	if err != nil || vm.NixInstallations != 0 {
		t.Fatalf("failed sudo authentication installed Nix: %+v, %v", vm, err)
	}
	assertNoStagedInstaller(t, server.directory)
}

func TestExistingNixVersionMismatchIsRejected(t *testing.T) {
	g, server := nixFixture(t)
	vm, err := readFakeVM(server.directory)
	if err != nil {
		t.Fatal(err)
	}
	vm.NixVersion = "2.34.0"
	if err := saveFakeVM(server.directory, vm); err != nil {
		t.Fatal(err)
	}
	if _, err := g.ensureNix(t.Context()); err == nil || !strings.Contains(err.Error(), "fresh stateDirectory") {
		t.Fatalf("different Nix version was not rejected: %v", err)
	}
	after, err := readFakeVM(server.directory)
	if err != nil || after != vm {
		t.Fatalf("existing Nix installation was changed: %+v, %v", after, err)
	}
}

func TestManagedNixDoesNotRequireInstallerVersion(t *testing.T) {
	g, server := nixFixture(t)
	vm, err := readFakeVM(server.directory)
	if err != nil {
		t.Fatal(err)
	}
	vm.NixVersion = "2.34.9"
	vm.CurrentSystem = "/nix/store/active-system"
	if err := saveFakeVM(server.directory, vm); err != nil {
		t.Fatal(err)
	}
	g.installer.Path = "/missing/installer.tar.xz"
	facts, err := g.ensureNix(t.Context())
	if err != nil || facts.NixVersion != vm.NixVersion || facts.CurrentSystem != vm.CurrentSystem {
		t.Fatalf("managed Nix was not reused: %+v, %v", facts, err)
	}
	after, err := readFakeVM(server.directory)
	if err != nil || after != vm {
		t.Fatalf("managed Nix installation was changed: %+v, %v", after, err)
	}
}

func TestNixReuseWaitsForStartup(t *testing.T) {
	g, server := nixFixture(t)
	vm, err := readFakeVM(server.directory)
	if err != nil {
		t.Fatal(err)
	}
	vm.NixVersion = "2.35.4"
	vm.NixInstallations = 1
	if err := saveFakeVM(server.directory, vm); err != nil {
		t.Fatal(err)
	}
	g.installer.Path = "/missing/installer.tar.xz"
	server.nixPending.Store(true)
	defer server.nixPending.Store(false)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	type result struct {
		facts systemFacts
		err   error
	}
	done := make(chan result, 1)
	go func() {
		facts, err := g.ensureNix(ctx)
		done <- result{facts: facts, err: err}
	}()
	select {
	case result := <-done:
		t.Fatalf("preparation finished before Nix became available: %+v", result)
	case <-time.After(50 * time.Millisecond):
	}
	server.nixPending.Store(false)
	finished := <-done
	if finished.err != nil || finished.facts.NixVersion != "2.35.4" {
		t.Fatalf("existing Nix was not reused after startup: %+v", finished)
	}
	after, err := readFakeVM(server.directory)
	if err != nil || after.NixInstallations != 1 {
		t.Fatalf("existing Nix was reinstalled: %+v, %v", after, err)
	}
}

func assertNoStagedInstaller(t *testing.T, directory string) {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(directory, "nix-darwin-vm.*"))
	if err != nil || len(paths) != 0 {
		t.Fatalf("staged installers were not removed: %v, %v", paths, err)
	}
}
