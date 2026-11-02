package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"syscall"

	"golang.org/x/term"
)

type configuration struct {
	Name           string       `json:"name"`
	BootstrapUser  string       `json:"bootstrapUser"`
	Cores          int          `json:"cores"`
	MemorySize     int          `json:"memorySize"`
	DiskSize       int          `json:"diskSize"`
	MacOSVersion   string       `json:"macosVersion"`
	Graphics       bool         `json:"graphics"`
	IPSW           string       `json:"ipsw"`
	StateDirectory string       `json:"stateDirectory"`
	NixInstaller   nixInstaller `json:"nixInstaller"`
	System         string       `json:"system"`
}

var bootstrapUserPattern = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_.-]*$`)

func loadConfiguration(path string) (configuration, error) {
	var config configuration

	data, err := os.ReadFile(path)
	if err != nil {
		return config, fmt.Errorf("read configuration: %w", err)
	}

	if err := json.Unmarshal(data, &config); err != nil {
		return config, fmt.Errorf("decode configuration: %w", err)
	}

	if config.Name == "" {
		return config, errors.New("VM name is required")
	}
	if config.Cores <= 0 {
		return config, errors.New("cores must be positive")
	}
	if config.MemorySize <= 0 {
		return config, errors.New("memorySize must be positive (MiB)")
	}
	if config.IPSW == "" {
		return config, errors.New("IPSW path is required")
	}
	if config.StateDirectory == "" {
		return config, errors.New("stateDirectory is required")
	}
	if !bootstrapUserPattern.MatchString(config.BootstrapUser) {
		return config, errors.New("bootstrapUser must be a nonempty account name containing letters, digits, underscores, periods, or hyphens, starting with a letter or underscore")
	}
	if err := config.NixInstaller.validate(); err != nil {
		return config, err
	}
	if !filepath.IsAbs(config.System) {
		return config, errors.New("system must be an absolute store path")
	}
	// Tart accepts a UInt16 count of decimal GB, rounded up from MiB.
	if config.DiskSize <= 0 || config.DiskSize > 65535*1000*1000*1000/(1024*1024) {
		return config, errors.New("diskSize must be positive (MiB) and fit within Tart's 65535 GB limit")
	}

	config.StateDirectory, err = filepath.Abs(config.StateDirectory)
	if err != nil {
		return config, fmt.Errorf("resolve state directory: %w", err)
	}

	return config, nil
}

func run(args []string, stderr io.Writer) int {
	if len(args) > 0 && args[0] == "ssh" {
		return sshMain(args[1:], stderr)
	}
	flags := flag.NewFlagSet("runner", flag.ContinueOnError)
	flags.SetOutput(stderr)
	path := flags.String("config", "", "path to the VM configuration JSON file")
	interactive := flags.Bool("interactive", false, "open a guest shell after activation; Enter reconnects after logout")

	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}

	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "runner: unexpected positional arguments")
		return 2
	}
	if *path == "" {
		fmt.Fprintln(stderr, "runner: --config is required")
		return 2
	}

	config, err := loadConfiguration(*path)
	if err != nil {
		fmt.Fprintln(stderr, "runner:", err)
		return 1
	}
	var input *os.File
	if *interactive {
		if !term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stdout.Fd())) {
			fmt.Fprintln(stderr, "runner: --interactive requires a terminal on stdin and stdout")
			return 2
		}
		input = os.Stdin
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()
	if err := runVM(ctx, config, os.Stdout, stderr, &net.Dialer{}, input); err != nil {
		fmt.Fprintln(stderr, "runner:", err)
		if ctx.Err() != nil {
			return 130
		}
		return exitCode(err)
	}
	return 0
}

func main() {
	os.Exit(run(os.Args[1:], os.Stderr))
}
