# VM integration tests

Run the real VM scenarios on an Apple silicon Mac with macOS 27 or newer:

```sh
nix run .#test-vm
```

The first build downloads the pinned IPSW and Nix installer and builds the guest
configurations. Each scenario creates a fresh VM. These tests run outside the
Nix build sandbox and are separate from `nix flake check`.

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

## GitHub Actions

The `VM` workflow runs both scenarios on pushes to `main` and through
**Actions → VM → Run workflow**. It uses the self-hosted `nix-vm-darwin` runner
on `mmini`; formatter and ordinary checks use GitHub-hosted runners.

The runner needs Nix, macOS 27 or newer, and an unlocked login keychain for its
service account. Its host configuration is maintained in `booxter/nix`, under
`darwin/mmini/vm-runner.nix`.

The job allows 60 minutes for builds and tests. With cached inputs, the scenarios
take about 11 minutes. Failed runs upload only `run-*.log` files as `vm-logs`,
retained for seven days. VM disks and credentials stay on the runner and are
removed when its ephemeral work directory is cleaned between jobs.
