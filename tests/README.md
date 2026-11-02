# VM integration tests

## Run the scenarios

On an Apple silicon Mac with Nix and macOS 27 or newer:

```sh
nix run .#test-vm
```

The build downloads the selected IPSW and Nix installer and builds the guest
configurations. Each scenario creates a fresh VM outside the Nix build sandbox.
These tests are separate from `nix flake check`.

Select a scenario with the Go test filter:

```sh
nix run .#test-vm -- -test.run TestIntegrationLifecycle
nix run .#test-vm -- -test.run TestIntegrationTerminal
```

| Scenario | Coverage |
| --- | --- |
| `TestIntegrationLifecycle` | Account creation, Nix installation, activation, cold-boot reuse, persistent files, configuration updates |
| `TestIntegrationTerminal` | Guest PTY, Ctrl-C, logout, reconnect, stopping at the reconnect prompt, guest shutdown while waiting there |

## Inspect failures

The suite prints its temporary state directory and progress. Successful scenarios
remove their state; failures retain the VM disk and `run-*.log` files for inspection.
The state directory contains guest credentials; keep it private.

Cleanup stops remaining test VMs. Ctrl-C cancels the suite and starts cleanup.

## GitHub Actions

CI runs both scenarios on pull requests and pushes to `main`, using the default
macOS image and Nix installer. Run them manually through
**Actions → VM → Run workflow**.

Failed jobs upload `run-*.log` files as `vm-logs`, retained for seven days. Disks
and credentials stay on the runner until its ephemeral work directory is cleaned
between jobs. See [contributing](../CONTRIBUTING.md) for runner setup.
