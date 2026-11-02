package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func runInteractiveFixture() int {
	g := newGuest(configuration{StateDirectory: os.Args[2], BootstrapUser: os.Args[4]}, nil)
	if err := g.prepare(true); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	g.address = os.Args[3]
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()
	err := g.interact(ctx, os.Stdin, os.Stdout, os.Stderr)
	if errors.Is(err, context.Canceled) {
		fmt.Println("interactive cancelled")
		return 0
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Println("interactive completed")
	return 0
}

type terminalOutput struct {
	sync.Mutex
	buffer  bytes.Buffer
	changed chan struct{}
}

func (o *terminalOutput) Write(data []byte) (int, error) {
	o.Lock()
	defer o.Unlock()
	n, err := o.buffer.Write(data)
	select {
	case o.changed <- struct{}{}:
	default:
	}
	return n, err
}

func (o *terminalOutput) wait(t *testing.T, text string, count int) {
	t.Helper()
	timer := time.NewTimer(15 * time.Second)
	defer timer.Stop()
	for {
		o.Lock()
		output := o.buffer.String()
		o.Unlock()
		if strings.Count(output, text) >= count {
			return
		}
		select {
		case <-o.changed:
		case <-timer.C:
			t.Fatalf("did not receive %q %d times; output: %s", text, count, output)
		}
	}
}

func TestInteractiveShellReconnectsAndRestoresTerminal(t *testing.T) {
	for _, ending := range []string{"interrupt", "EOF"} {
		t.Run(ending, func(t *testing.T) {
			g, server := nixFixture(t)
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			// script supplies a controlling terminal, so OpenSSH handles real
			// terminal modes and Ctrl-C rather than reading a pipe.
			command := exec.CommandContext(t.Context(), "script", "-q", "/dev/null", executable,
				"interactive-fixture", server.directory, server.listener.Addr().String(), g.user)
			input, err := command.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			defer input.Close()
			output := &terminalOutput{changed: make(chan struct{}, 1)}
			command.Stdout = output
			command.Stderr = output
			command.WaitDelay = 2 * time.Second
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- command.Wait() }()
			t.Cleanup(func() { command.Process.Kill() })
			write := func(text string) {
				t.Helper()
				if _, err := input.Write([]byte(text)); err != nil {
					t.Fatal(err)
				}
			}

			output.wait(t, "guest shell ready", 1)
			write("\x03")
			output.wait(t, "guest received Ctrl-C", 1)
			write("exit\n")
			output.wait(t, "VM is running. Press Enter", 1)
			write("\n")
			output.wait(t, "guest shell ready", 2)
			write("exit 23\n")
			output.wait(t, "SSH session ended:", 1)
			output.wait(t, "VM is running. Press Enter", 2)
			if ending == "interrupt" {
				write("\x03")
				output.wait(t, "interactive cancelled", 1)
			} else {
				write("\x04")
				output.wait(t, "interactive completed", 1)
			}
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("interactive session did not exit")
			}
		})
	}
}

func TestInteractiveModeRequiresTerminalBeforeStartingVM(t *testing.T) {
	config := tartFixture(t)
	data, err := json.Marshal(config.configuration)
	if err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(t.Context(), executable, "--config", writeConfiguration(t, string(data)), "--interactive")
	output, err := command.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 2 || !strings.Contains(string(output), "requires a terminal") {
		t.Fatalf("non-terminal input was not rejected: %v; output: %s", err, output)
	}
	if _, err := os.Stat(config.StateDirectory); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("interactive mode created VM state before checking the terminal: %v", err)
	}
}
