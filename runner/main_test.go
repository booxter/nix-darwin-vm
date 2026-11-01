package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConfiguration(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "vm.json")
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func checkRun(t *testing.T, args []string, wantCode int, wantMessage string) {
	t.Helper()
	var stderr bytes.Buffer
	if code := run(args, &stderr); code != wantCode {
		t.Fatalf("exit code %d, want %d; stderr: %s", code, wantCode, &stderr)
	}
	if !strings.Contains(stderr.String(), wantMessage) {
		t.Fatalf("stderr %q does not contain %q", stderr.String(), wantMessage)
	}
}

func TestLoadConfiguration(t *testing.T) {
	path := writeConfiguration(t, `{
		"name": "guest",
		"bootstrapUser": "builder",
		"cores": 2,
		"memorySize": 8192,
		"macosVersion": "27.2",
		"graphics": false,
		"ipsw": "/nix/store/example.ipsw",
		"stateDirectory": "/tmp/guest-vm"
	}`)

	got, err := loadConfiguration(path)
	if err != nil {
		t.Fatal(err)
	}
	want := configuration{
		Name:           "guest",
		BootstrapUser:  "builder",
		Cores:          2,
		MemorySize:     8192,
		MacOSVersion:   "27.2",
		Graphics:       false,
		IPSW:           "/nix/store/example.ipsw",
		StateDirectory: "/tmp/guest-vm",
	}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestRelativeStateDirectory(t *testing.T) {
	t.Chdir(t.TempDir())
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	path := writeConfiguration(t, `{
		"name": "guest",
		"bootstrapUser": "builder",
		"cores": 4,
		"memorySize": 4096,
		"ipsw": "/nix/store/example.ipsw",
		"stateDirectory": "./guest state"
	}`)

	config, err := loadConfiguration(path)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(workingDirectory, "guest state")
	if config.StateDirectory != want {
		t.Fatalf("state directory %q, want %q", config.StateDirectory, want)
	}
}

func TestRunArguments(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		code    int
		message string
	}{
		{
			name:    "help",
			args:    []string{"--help"},
			code:    0,
			message: "-config",
		},
		{
			name:    "missing config",
			code:    2,
			message: "--config is required",
		},
		{
			name:    "unknown flag",
			args:    []string{"--unknown"},
			code:    2,
			message: "flag provided but not defined",
		},
		{
			name:    "positional argument",
			args:    []string{"guest"},
			code:    2,
			message: "unexpected positional arguments",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			checkRun(t, test.args, test.code, test.message)
		})
	}
}

func TestRunRejectsInvalidConfiguration(t *testing.T) {
	tests := []struct {
		name     string
		contents string
		message  string
	}{
		{
			name:     "malformed JSON",
			contents: `{`,
			message:  "decode configuration",
		},
		{
			name:     "missing name",
			contents: `{"cores": 4, "memorySize": 4096}`,
			message:  "VM name is required",
		},
		{
			name:     "invalid cores",
			contents: `{"name": "guest", "cores": 0, "memorySize": 4096}`,
			message:  "cores must be positive",
		},
		{
			name:     "invalid memory",
			contents: `{"name": "guest", "cores": 4, "memorySize": -1}`,
			message:  "memorySize must be positive",
		},
		{
			name:     "missing IPSW",
			contents: `{"name": "guest", "cores": 4, "memorySize": 4096}`,
			message:  "IPSW path is required",
		},
		{
			name:     "missing state directory",
			contents: `{"name": "guest", "cores": 4, "memorySize": 4096, "ipsw": "/nix/store/example.ipsw"}`,
			message:  "stateDirectory is required",
		},
		{
			name:     "invalid bootstrap user",
			contents: `{"name":"guest","cores":4,"memorySize":4096,"ipsw":"example.ipsw","stateDirectory":"./vm","bootstrapUser":"user,enablesRemoteLogin=false"}`,
			message:  "bootstrapUser must be",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := writeConfiguration(t, test.contents)
			checkRun(t, []string{"--config", path}, 1, test.message)
		})
	}
}

func TestRunReportsMissingConfiguration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.json")
	checkRun(t, []string{"--config", path}, 1, "read configuration")
}
