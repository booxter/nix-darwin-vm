package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
)

func (t tart) waitStorage(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	var files []*os.File
	for _, name := range []string{"nvram.bin", "disk.img"} {
		path := filepath.Join(t.home, "vms", vmName, name)
		file, err := os.OpenFile(path, os.O_RDWR, 0)
		if err != nil {
			return fmt.Errorf("open VM storage: %w", err)
		}
		defer file.Close()
		files = append(files, file)
	}

	// Virtualization.framework can retain storage locks after `tart create` exits.
	// https://github.com/openai/tart/issues/865
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()

	for {
		var blocked string
		for _, file := range files {
			lock := unix.Flock_t{Type: unix.F_WRLCK, Whence: unix.SEEK_SET}
			if err := unix.FcntlFlock(file.Fd(), unix.F_GETLK, &lock); err != nil {
				return fmt.Errorf("inspect VM storage %s: %w", file.Name(), err)
			}
			if lock.Type != unix.F_UNLCK {
				blocked = filepath.Base(file.Name())
				break
			}
		}
		if blocked == "" {
			return ctx.Err()
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for VM storage %s: %w", blocked, ctx.Err())
		case <-ticker.C:
		}
	}
}
