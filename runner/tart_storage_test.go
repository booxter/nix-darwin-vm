package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func storageFixture(t *testing.T) tart {
	t.Helper()
	machine := tart{home: t.TempDir()}
	directory := filepath.Join(machine.home, "vms", vmName)
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"nvram.bin", "disk.img"} {
		if err := os.WriteFile(filepath.Join(directory, name), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	return machine
}

func holdStorage(t *testing.T, machine tart, name string) *os.File {
	t.Helper()
	file, err := os.OpenFile(filepath.Join(machine.home, "vms", vmName, name), os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { file.Close() })
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	return file
}

func TestStorageWaitsForLockRelease(t *testing.T) {
	for _, name := range []string{"nvram.bin", "disk.img"} {
		t.Run(name, func(t *testing.T) {
			machine := storageFixture(t)
			file := holdStorage(t, machine, name)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- machine.waitStorage(ctx) }()

			select {
			case err := <-done:
				t.Fatalf("storage became ready while %s was locked: %v", name, err)
			case <-time.After(50 * time.Millisecond):
			}

			if err := file.Close(); err != nil {
				t.Fatal(err)
			}

			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("storage wait did not finish after lock release")
			}
		})
	}
}

func TestStorageWaitCanBeCancelled(t *testing.T) {
	machine := storageFixture(t)
	holdStorage(t, machine, "nvram.bin")
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- machine.waitStorage(ctx) }()
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v, expected cancellation", err)
		}
	case <-time.After(time.Second):
		t.Fatal("storage wait ignored cancellation")
	}
}

func TestStorageDeadlineIdentifiesBlockedFile(t *testing.T) {
	machine := storageFixture(t)
	holdStorage(t, machine, "disk.img")
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	err := machine.waitStorage(ctx)
	if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "disk.img") {
		t.Fatalf("got %v, expected a deadline naming disk.img", err)
	}
}
