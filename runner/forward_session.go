package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"
)

func (t tart) activateWithSockets(ctx context.Context, g guest, tunnels *sync.WaitGroup) error {
	if len(g.forwardedSockets) == 0 {
		return g.activateSystem(ctx, t.stdout, t.stderr)
	}

	persistent := make(map[string]forwardedSocket)
	temporary := make(map[string]forwardedSocket)
	for name, socket := range g.forwardedSockets {
		if socket.Lifetime == vmLifetime {
			persistent[name] = socket
		} else {
			temporary[name] = socket
		}
	}

	vm, err := g.startForwards(ctx, persistent, t.stderr)
	if err != nil {
		return err
	}
	if err := t.activateWithTunnel(ctx, g, temporary, vm); err != nil {
		vm.stop()
		return err
	}
	if len(persistent) > 0 {
		tunnels.Go(func() {
			t.maintainForwards(ctx, g, persistent, vm)
		})
	}
	return nil
}

func (t tart) activateWithTunnel(ctx context.Context, g guest, sockets map[string]forwardedSocket, vm *socketTunnel) error {
	activation, err := g.startForwards(ctx, sockets, t.stderr)
	if err != nil {
		return err
	}
	// Terminating this master also revokes connections opened during activation.
	defer activation.stop()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	result := make(chan error, 1)
	go func() {
		result <- g.activateSystem(ctx, t.stdout, t.stderr)
	}()
	select {
	case err := <-result:
		return err
	case <-vm.done:
	case <-activation.done:
	}
	cancel()
	<-result
	return errors.New("socket forwarding disconnected during guest activation")
}

func (t tart) maintainForwards(ctx context.Context, g guest, sockets map[string]forwardedSocket, tunnel *socketTunnel) {
	for {
		select {
		case <-ctx.Done():
		case <-tunnel.done:
		}
		tunnel.stop()
		if ctx.Err() != nil {
			return
		}

		fmt.Fprintln(t.stderr, "VM socket forwarding disconnected; reconnecting")
		tunnel = t.reconnectForwards(ctx, g, sockets)
		if tunnel == nil {
			return
		}
		fmt.Fprintln(t.stderr, "VM socket forwarding restored")
	}
}

func (t tart) reconnectForwards(ctx context.Context, g guest, sockets map[string]forwardedSocket) *socketTunnel {
	for delay := time.Second; ; delay = min(2*delay, 30*time.Second) {
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(delay):
		}
		tunnel, err := t.reopenForwards(ctx, g, sockets)
		if err == nil {
			return tunnel
		}
		if ctx.Err() == nil {
			fmt.Fprintln(t.stderr, "Reconnect VM socket forwarding:", err)
		}
	}
}

func (t tart) reopenForwards(ctx context.Context, g guest, sockets map[string]forwardedSocket) (*socketTunnel, error) {
	// The guest can get a new DHCP lease after reboot. Its pinned SSH key stays the same.
	ip, err := t.ip(ctx)
	if err != nil {
		return nil, err
	}
	g.address = net.JoinHostPort(ip.String(), "22")
	return g.startForwards(ctx, sockets, t.stderr)
}
