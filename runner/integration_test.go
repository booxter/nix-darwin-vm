//go:build integration

package main

import (
	"testing"
	"time"
)

func TestIntegrationLifecycle(t *testing.T) {
	v := newIntegrationVM(t)
	t.Log("first boot: provision, install Nix, and activate")
	v.start(v.fixtures.Initial, false)
	v.verify(v.fixtures.Initial)
	v.ssh(`printf persisted > "$HOME/vm-test-persistent"`, nil)
	v.shutdown()

	t.Log("cold boot: reuse the same configuration and guest disk")
	v.start(v.fixtures.Initial, false)
	v.verify(v.fixtures.Initial)
	v.expectSSH(`cat "$HOME/vm-test-persistent"`, "persisted")
	v.shutdown()

	t.Log("update: copy and activate a changed configuration on the same disk")
	v.start(v.fixtures.Updated, false)
	v.verify(v.fixtures.Updated)
	v.expectSSH(`cat "$HOME/vm-test-persistent"`, "persisted")
	v.shutdown()
}

func TestIntegrationTerminal(t *testing.T) {
	v := newIntegrationVM(t)
	t.Log("first boot: attach a real SSH terminal")
	v.start(v.fixtures.Initial, true)
	v.shell(`test -t 0 && test -t 1 && printf '\nVM_TEST_PTY\n'`, "VM_TEST_PTY")
	v.verify(v.fixtures.Initial)

	t.Log("Ctrl-C interrupts a guest command without stopping the VM")
	v.process.write(t, "sh -c 'printf \"\\nVM_TEST_INTERRUPT_READY\\n\"; exec sleep 300'\n")
	v.process.wait(t, "\nVM_TEST_INTERRUPT_READY\n", 1, 30*time.Second)
	v.process.write(t, "\x03")
	v.shell(`printf '\nVM_TEST_INTERRUPT_DONE\n'`, "VM_TEST_INTERRUPT_DONE")

	t.Log("logout keeps the VM running; Enter reconnects")
	v.logout(1)
	v.verify(v.fixtures.Initial)
	v.process.write(t, "\n")
	v.shell(`test -t 0 && printf '\nVM_TEST_RECONNECTED\n'`, "VM_TEST_RECONNECTED")
	v.logout(2)

	t.Log("Ctrl-C at the reconnect prompt stops the VM")
	v.process.write(t, "\x03")
	v.process.expectExit(t, 130)
	v.expectStopped()

	t.Log("guest shutdown exits a runner waiting at the reconnect prompt")
	v.start(v.fixtures.Initial, true)
	v.shell(`printf '\nVM_TEST_REUSED\n'`, "VM_TEST_REUSED")
	v.logout(1)
	v.shutdown()
}
