package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func localStoreURI(root string) string {
	return "local?" + url.Values{"root": {root}}.Encode()
}

func storeDirectory(t *testing.T) string {
	t.Helper()
	// Nix rejects symlinks in store parents, including macOS's /var and /tmp.
	directory, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return directory
}

func storeFixture(t *testing.T, store string) map[string]string {
	t.Helper()
	dataDirectory := t.TempDir()
	for name, size := range map[string]int{"shared": 1 << 20, "system": 1 << 16} {
		data := make([]byte, size/2)
		if _, err := rand.Read(data); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dataDirectory, name), []byte(hex.EncodeToString(data)), 0600); err != nil {
			t.Fatal(err)
		}
	}
	command := exec.Command("nix-instantiate", "--option", "post-build-hook", "",
		"--store", store, "--eval", "--strict", "--read-write-mode", "--json",
		"--argstr", "sharedFile", filepath.Join(dataDirectory, "shared"),
		"--argstr", "systemFile", filepath.Join(dataDirectory, "system"),
		"--expr", `{ sharedFile, systemFile }:
let
  shared = builtins.toFile "copy-shared" (builtins.readFile sharedFile);
  content = builtins.readFile systemFile;
in {
  inherit shared;
  first = builtins.toFile "copy-first" ("first " + content + shared);
  second = builtins.toFile "copy-second" ("second " + content + shared);
}`)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("create fixture closure: %v; output: %s", err, output)
	}
	var paths map[string]string
	if err := json.Unmarshal(output, &paths); err != nil {
		t.Fatalf("read fixture paths: %v; output: %s", err, output)
	}
	return paths
}

type copyFixture struct {
	guest
	server          *sshTestServer
	sourceDirectory string
	paths           map[string]string
}

func newCopyFixture(t *testing.T) copyFixture {
	t.Helper()
	realNix, err := exec.LookPath("nix")
	if err != nil {
		t.Fatal(err)
	}
	g, server := nixFixture(t)
	// This test uses real Nix clients and stores; lifecycle tests use a fixture CLI.
	t.Setenv("PATH", filepath.Dir(realNix)+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("NIX_CONF_DIR", t.TempDir())
	t.Setenv("NIX_CONFIG", "build-users-group =\n")
	sourceDirectory := storeDirectory(t)
	source := localStoreURI(sourceDirectory)
	paths := storeFixture(t, source)
	t.Setenv("NIX_REMOTE", source)
	server.storeDirectory = storeDirectory(t)
	g.address = server.listener.Addr().String()
	g.system = paths["first"]
	if err := g.prepareCopy(t.Context()); err != nil {
		t.Fatal(err)
	}
	return copyFixture{guest: g, server: server, sourceDirectory: sourceDirectory, paths: paths}
}

func TestSystemCopyTransfersOnlyMissingPaths(t *testing.T) {
	f := newCopyFixture(t)
	var output bytes.Buffer
	copy := func() int64 {
		t.Helper()
		before := f.server.storeBytes.Load()
		if err := f.copySystem(t.Context(), io.Discard, &output); err != nil {
			t.Fatalf("copy closure: %v; output: %s", err, &output)
		}
		return f.server.storeBytes.Load() - before
	}

	initial := copy()
	if initial < 1<<20 {
		t.Fatalf("initial copy did not transfer the closure: %d bytes", initial)
	}
	unchanged := copy()
	if unchanged > 1<<14 {
		t.Fatalf("unchanged copy resent store contents: %d bytes", unchanged)
	}
	f.system = f.paths["second"]
	changed := copy()
	if changed < 1<<16 || changed > 1<<18 {
		t.Fatalf("changed copy did not transfer just the new system path: %d bytes", changed)
	}
	t.Logf("protocol bytes transferred: initial=%d unchanged=%d changed=%d", initial, unchanged, changed)

	for _, path := range f.paths {
		original, err := os.ReadFile(filepath.Join(f.sourceDirectory, strings.TrimPrefix(path, "/")))
		if err != nil {
			t.Fatal(err)
		}
		copied, err := os.ReadFile(filepath.Join(f.server.storeDirectory, strings.TrimPrefix(path, "/")))
		if err != nil || !bytes.Equal(original, copied) {
			t.Fatalf("copied contents differ for %s: %v", path, err)
		}
	}
	profile, err := os.Readlink(filepath.Join(f.server.directory, "copied-profile"))
	if err != nil || profile == "" {
		t.Fatalf("copied system was not retained in a profile: %q, %v", profile, err)
	}
	target, err := os.Readlink(filepath.Join(f.server.directory, profile))
	if err != nil || target != f.system {
		t.Fatalf("profile does not retain the latest configuration: %q, %v", target, err)
	}
}

func TestSystemCopyRejectsChangedHostKey(t *testing.T) {
	f := newCopyFixture(t)
	otherServer := newSSHTestServer(t, f.server.directory)
	otherServer.storeDirectory = storeDirectory(t)
	f.address = otherServer.listener.Addr().String()
	var output bytes.Buffer
	if err := f.copySystem(t.Context(), io.Discard, &output); err == nil ||
		!strings.Contains(output.String(), "Host key verification failed") {
		t.Fatalf("changed store host key was not rejected: %v; output: %s", err, &output)
	}
	if otherServer.storeBytes.Load() != 0 {
		t.Fatal("unverified store received data")
	}
}

func TestSystemCopyCanBeCancelled(t *testing.T) {
	f := newCopyFixture(t)
	f.server.storeBlocked.Store(true)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	var output bytes.Buffer
	done := make(chan error, 1)
	go func() { done <- f.copySystem(ctx, io.Discard, &output) }()
	for f.server.storeBytes.Load() == 0 {
		select {
		case err := <-done:
			t.Fatalf("copy exited before reaching the store: %v; output: %s", err, &output)
		case <-time.After(10 * time.Millisecond):
		}
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled copy succeeded")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("copy did not stop after cancellation")
	}
}

func TestCopyFailuresStopVM(t *testing.T) {
	for _, failure := range []string{"copy-trust", "copy", "retain"} {
		t.Run(failure, func(t *testing.T) {
			config := tartFixture(t)
			t.Setenv("NIX_DARWIN_VM_TEST_FAIL", failure)
			var output bytes.Buffer
			if err := config.run(t.Context(), &output, &output); err == nil {
				t.Fatal("copy failure was ignored")
			}
			if strings.Contains(output.String(), "Ready:") {
				t.Fatal("copy failure reported readiness")
			}
			vm, err := readFakeVM(config.StateDirectory)
			if err != nil || vm.State != "stopped" || !vm.Interrupted || vm.CopiedSystem != "" {
				t.Fatalf("copy failure left a running or prepared VM: %+v, %v", vm, err)
			}
		})
	}
}
