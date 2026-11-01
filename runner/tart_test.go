package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

type fakeVM struct {
	Name        string
	State       string
	IPSW        string
	Cores       int
	Memory      int
	Graphics    bool
	Creations   int
	Runs        int
	Interrupted bool
}

func TestMain(m *testing.M) {
	if os.Getenv("NIX_DARWIN_VM_TEST_HELPER") == "1" {
		if filepath.Base(os.Args[0]) == "tart" {
			os.Exit(fakeTart())
		}
		os.Exit(run(os.Args[1:], os.Stderr))
	}
	os.Exit(m.Run())
}

func readFakeVM(home string) (fakeVM, error) {
	var vm fakeVM
	data, err := os.ReadFile(filepath.Join(home, "fake-vm.json"))
	if err != nil {
		return vm, err
	}
	err = json.Unmarshal(data, &vm)
	return vm, err
}

func saveFakeVM(home string, vm fakeVM) error {
	data, err := json.Marshal(vm)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(home, "fake-vm.json"), data, 0600)
}

func fakeTart() int {
	args := os.Args[1:]
	if len(args) == 0 {
		return 2
	}
	if os.Getenv("NIX_DARWIN_VM_TEST_FAIL") == args[0] {
		fmt.Fprintln(os.Stderr, "Tart operation failed")
		return 17
	}
	home := os.Getenv("TART_HOME")
	vm, err := readFakeVM(home)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	switch args[0] {
	case "list":
		machines := []fakeVM{}
		if vm.Name != "" {
			machines = append(machines, vm)
		}
		if err := json.NewEncoder(os.Stdout).Encode(machines); err != nil {
			return 1
		}
		return 0
	case "create":
		options := flag.NewFlagSet("create", flag.ContinueOnError)
		ipsw := options.String("from-ipsw", "", "")
		if err := options.Parse(args[1:]); err != nil || options.NArg() != 1 {
			return 2
		}
		vm = fakeVM{Name: options.Arg(0), State: "stopped", IPSW: *ipsw, Creations: vm.Creations + 1}
		if err := os.WriteFile(filepath.Join(home, "disk"), []byte("fresh disk"), 0600); err != nil {
			return 1
		}
	case "set":
		if len(args) < 2 || args[1] != vm.Name {
			return 2
		}
		options := flag.NewFlagSet("set", flag.ContinueOnError)
		cores := options.Int("cpu", 0, "")
		memory := options.Int("memory", 0, "")
		if err := options.Parse(args[2:]); err != nil {
			return 2
		}
		vm.Cores = *cores
		vm.Memory = *memory
	case "run":
		if args[len(args)-1] != vm.Name {
			return 2
		}
		vm.Graphics = !slices.Contains(args, "--no-graphics")
		vm.Runs++
		if os.Getenv("NIX_DARWIN_VM_TEST_BLOCK") == "1" {
			interrupts := make(chan os.Signal, 1)
			signal.Notify(interrupts, os.Interrupt)
			defer signal.Stop(interrupts)
			vm.State = "running"
			if err := saveFakeVM(home, vm); err != nil {
				return 1
			}
			fmt.Fprintln(os.Stdout, "guest ready")
			<-interrupts
			vm.Interrupted = true
		}
		vm.State = "stopped"
		if err := saveFakeVM(home, vm); err != nil {
			return 1
		}
		fmt.Fprintln(os.Stdout, "guest console")
		code, _ := strconv.Atoi(os.Getenv("NIX_DARWIN_VM_TEST_EXIT"))
		return code
	default:
		return 2
	}
	if err := saveFakeVM(home, vm); err != nil {
		return 1
	}
	return 0
}

func tartFixture(t *testing.T) configuration {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	if err := os.Symlink(executable, filepath.Join(bin, "tart")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("NIX_DARWIN_VM_TEST_HELPER", "1")
	return configuration{
		Name: "example", Cores: 2, MemorySize: 2048, Graphics: false,
		IPSW: "/nix/store/example.ipsw", StateDirectory: filepath.Join(t.TempDir(), "VM state"),
	}
}

func TestCreateAndReuseVM(t *testing.T) {
	config := tartFixture(t)
	otherHome := t.TempDir()
	t.Setenv("TART_HOME", otherHome)
	var stdout, stderr bytes.Buffer
	if err := runVM(t.Context(), config, &stdout, &stderr); err != nil {
		t.Fatalf("%v; stderr: %s", err, &stderr)
	}
	vm, err := readFakeVM(config.StateDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if vm.IPSW != config.IPSW || vm.Cores != 2 || vm.Memory != 2048 || vm.Graphics {
		t.Fatalf("unexpected initial VM: %+v", vm)
	}
	if !strings.Contains(stdout.String(), "guest console") {
		t.Fatalf("guest output was not forwarded: %s", &stdout)
	}
	if _, err := os.Stat(filepath.Join(otherHome, "fake-vm.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("runner used the inherited TART_HOME: %v", err)
	}
	disk := filepath.Join(config.StateDirectory, "disk")
	if err := os.WriteFile(disk, []byte("guest changes"), 0600); err != nil {
		t.Fatal(err)
	}
	config.Name = "renamed"
	config.IPSW = "/nix/store/another.ipsw"
	config.Cores = 4
	config.Graphics = true
	if err := runVM(t.Context(), config, &stdout, &stderr); err != nil {
		t.Fatalf("%v; stderr: %s", err, &stderr)
	}
	vm, err = readFakeVM(config.StateDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if vm.Creations != 1 || vm.Runs != 2 || vm.Cores != 4 || !vm.Graphics {
		t.Fatalf("unexpected reused VM: %+v", vm)
	}
	contents, err := os.ReadFile(disk)
	if err != nil || string(contents) != "guest changes" {
		t.Fatalf("guest disk was replaced: %q, %v", contents, err)
	}
}

func TestTartFailuresAreReported(t *testing.T) {
	for _, operation := range []string{"list", "create", "set", "run"} {
		t.Run(operation, func(t *testing.T) {
			config := tartFixture(t)
			t.Setenv("NIX_DARWIN_VM_TEST_FAIL", operation)
			var output bytes.Buffer
			err := runVM(t.Context(), config, &output, &output)
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
	err := runVM(t.Context(), config, io.Discard, io.Discard)
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
	t.Setenv("NIX_DARWIN_VM_TEST_BLOCK", "1")
	ctx, cancel := context.WithCancel(t.Context())
	reader, writer := io.Pipe()
	done := make(chan struct{})
	var runError error
	go func() {
		runError = runVM(ctx, config, writer, io.Discard)
		writer.Close()
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		reader.Close()
		select {
		case <-done:
		case <-time.After(15 * time.Second):
			t.Error("Tart did not stop after cancellation")
		}
	})
	ready := make(chan struct{})
	go func() {
		scanner := bufio.NewScanner(reader)
		for scanner.Scan() {
			if scanner.Text() == "guest ready" {
				close(ready)
				return
			}
		}
	}()
	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("guest did not start")
	}
	err := runVM(t.Context(), config, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "already in use") {
		t.Fatalf("concurrent invocation was not rejected: %v", err)
	}
	cancel()
	// Drain the remaining console output so the subprocess can finish.
	io.Copy(io.Discard, reader)
	<-done
	if !errors.Is(runError, context.Canceled) {
		t.Fatalf("unexpected cancellation result: %v", runError)
	}
	vm, err := readFakeVM(config.StateDirectory)
	if err != nil || !vm.Interrupted || vm.State != "stopped" {
		t.Fatalf("Tart was not interrupted and reaped: %+v, %v", vm, err)
	}
	t.Setenv("NIX_DARWIN_VM_TEST_BLOCK", "")
	if err := runVM(t.Context(), config, io.Discard, io.Discard); err != nil {
		t.Fatalf("state was not released after cancellation: %v", err)
	}
}

func TestRunnerPreservesGuestExitStatus(t *testing.T) {
	config := tartFixture(t)
	t.Setenv("NIX_DARWIN_VM_TEST_EXIT", "23")
	data, err := json.Marshal(config)
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
