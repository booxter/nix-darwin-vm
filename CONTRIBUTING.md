# Contributing

## Local checks

Run from the repository root on an Apple silicon Mac:

```sh
nix fmt -- --ci
nix flake check
```

These check formatting, the module interface, the runner, and inventory updaters.
Run `nix fmt` to apply formatting. For end-to-end checks, run the
[VM integration scenarios](tests/README.md).

## CI

The `CI` workflow runs formatting, flake checks, and VM scenarios on pull requests
and pushes to `main`. The `required` status check succeeds only when all three
jobs pass.

The `VM` workflow uses the self-hosted `nix-vm-darwin` runner on `mmini`.
The runner needs Nix, macOS 27 or newer, and an unlocked login keychain for its
service account. Its host configuration lives in `booxter/nix` at
`darwin/mmini/vm-runner.nix`.

The VM job allows 60 minutes for builds and tests. Formatting and flake checks
run separately on GitHub-hosted runners.

## Automated updates

| Workflow | Schedule (UTC) | PR branch |
| --- | --- | --- |
| Update flake inputs | Monday 00:00 | `ci/flake-update` |
| Update inventories | Monday 00:17 | `ci/nix-installers-update`, `ci/macos-images-update` |

Each workflow also supports **Actions → workflow name → Run workflow**. Inventory
updates run as separate jobs, each maintaining its own PR. See the
[image](images/README.md) and [installer](installers/README.md) guides for manual
updates and verification details.

To enable update PRs and auto-merge:

1. Set the `CI_PR_TOKEN` repository secret with contents and pull requests write
   permissions.
2. Enable repository auto-merge.
3. Require the `required` status check in branch protection so updates merge only
   after CI passes.

Both VM defaults follow the newest versions in their inventories. Inventory PRs
therefore test the new defaults in real VMs before merging.
