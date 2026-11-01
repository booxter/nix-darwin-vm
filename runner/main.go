package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type configuration struct {
	Name           string `json:"name"`
	Cores          int    `json:"cores"`
	MemorySize     int    `json:"memorySize"`
	Graphics       bool   `json:"graphics"`
	IPSW           string `json:"ipsw"`
	StateDirectory string `json:"stateDirectory"`
}

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

	config.StateDirectory, err = filepath.Abs(config.StateDirectory)
	if err != nil {
		return config, fmt.Errorf("resolve state directory: %w", err)
	}

	return config, nil
}

func run(args []string, stderr io.Writer) int {
	flags := flag.NewFlagSet("runner", flag.ContinueOnError)
	flags.SetOutput(stderr)
	path := flags.String("config", "", "path to the VM configuration JSON file")

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

	fmt.Fprintf(stderr, "runner: running VM %q is not implemented yet\n", config.Name)
	return 1
}

func main() {
	os.Exit(run(os.Args[1:], os.Stderr))
}
