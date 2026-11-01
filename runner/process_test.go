package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

type fakeVM struct {
	Name         string
	State        string
	IPSW         string
	Cores        int
	Memory       int
	Graphics     bool
	Creations    int
	Runs         int
	Interrupted  bool
	Password     string
	User         string
	MacOSVersion string
	KeyInstalled bool
}

func TestMain(m *testing.M) {
	if os.Getenv("NIX_DARWIN_VM_TEST_HELPER") == "1" {
		switch filepath.Base(os.Args[0]) {
		case "tart":
			os.Exit(fakeTart())
		case "sw_vers":
			version := os.Getenv("NIX_DARWIN_VM_TEST_HOST_VERSION")
			if version == "" {
				version = "27.2"
			}
			fmt.Println(version)
			os.Exit(0)
		default:
			os.Exit(run(os.Args[1:], os.Stderr))
		}
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
	case "ip":
		if vm.State != "running" {
			return 1
		}
		address := os.Getenv("NIX_DARWIN_VM_TEST_IP")
		if address == "" {
			address = "192.0.2.10"
		}
		fmt.Println(address)
		return 0
	case "create":
		options := flag.NewFlagSet("create", flag.ContinueOnError)
		ipsw := options.String("from-ipsw", "", "")
		if err := options.Parse(args[1:]); err != nil || options.NArg() != 1 {
			return 2
		}
		version := os.Getenv("NIX_DARWIN_VM_TEST_GUEST_VERSION")
		if version == "" {
			version = "27.2"
		}
		vm = fakeVM{Name: options.Arg(0), State: "stopped", IPSW: *ipsw, Creations: vm.Creations + 1, MacOSVersion: version}
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
		if code := os.Getenv("NIX_DARWIN_VM_TEST_EXIT"); code != "" {
			value, _ := strconv.Atoi(code)
			return value
		}
		vm.Graphics = !slices.Contains(args, "--no-graphics")
		if vm.Password == "" {
			vm.provision(args)
		}
		interrupts := make(chan os.Signal, 1)
		signal.Notify(interrupts, os.Interrupt)
		defer signal.Stop(interrupts)
		vm.Runs++
		vm.State = "running"
		if err := saveFakeVM(home, vm); err != nil {
			return 1
		}
		<-interrupts
		vm, err = readFakeVM(home)
		if err != nil {
			return 1
		}
		vm.State = "stopped"
		vm.Interrupted = true
		fmt.Fprintln(os.Stdout, "guest console")
	default:
		return 2
	}
	if err := saveFakeVM(home, vm); err != nil {
		return 1
	}
	return 0
}

func (vm *fakeVM) provision(args []string) {
	for _, argument := range args {
		options, ok := strings.CutPrefix(argument, "--provisioning-opts=")
		if !ok {
			continue
		}
		for _, option := range strings.Split(options, ",") {
			key, value, _ := strings.Cut(option, "=")
			switch key {
			case "username":
				vm.User = value
			case "password":
				vm.Password = value
			}
		}
	}
}

type vmFixture struct {
	configuration
	dialer *sshTestServer
}

func (f vmFixture) run(ctx context.Context, stdout, stderr io.Writer) error {
	return runVM(ctx, f.configuration, stdout, stderr, f.dialer)
}

func tartFixture(t *testing.T) vmFixture {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	for _, name := range []string{"tart", "sw_vers"} {
		if err := os.Symlink(executable, filepath.Join(bin, name)); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("NIX_DARWIN_VM_TEST_HELPER", "1")
	config := configuration{
		Name: "example", Cores: 2, MemorySize: 4096, Graphics: false, MacOSVersion: "27.2",
		BootstrapUser: "vmadmin",
		IPSW:          "/nix/store/example.ipsw", StateDirectory: filepath.Join(t.TempDir(), "VM's 100% state"),
	}
	return vmFixture{configuration: config, dialer: newSSHTestServer(t, config.StateDirectory)}
}
