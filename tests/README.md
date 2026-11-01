# VM integration tests

Run the real VM scenarios on an Apple silicon Mac with macOS 27 or newer:

```sh
nix run .#test-vm
```

The first build downloads the pinned IPSW and Nix installer and builds the guest
configurations. Each scenario creates a fresh VM. These tests run outside the
Nix build sandbox and are separate from `nix flake check` and GitHub CI.

Select a scenario with the Go test filter:

```sh
nix run .#test-vm -- -test.run TestIntegrationLifecycle
nix run .#test-vm -- -test.run TestIntegrationTerminal
```

The lifecycle scenario checks account provisioning, Nix installation, activation,
cold-boot reuse, persistent guest files, and a configuration update. The terminal
scenario checks guest PTY allocation, Ctrl-C inside the guest, logout, reconnect,
stopping from the reconnect prompt, and guest shutdown while waiting at that prompt.

The suite prints its temporary state directory and progress. Cleanup stops any
remaining test VMs. Successful scenarios remove their state; failed scenarios
retain the VM disk and `run-*.log` files for inspection. The state directory also
contains guest credentials, so keep it private. Ctrl-C cancels the suite and
starts cleanup.
