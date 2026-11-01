# nix-darwin-vm

Import `darwinModules.vm` into a nix-darwin configuration to expose
`config.system.build.vm`. The runner currently creates and starts bare macOS;
guest provisioning, Nix installation, and nix-darwin activation are future work.

Configure the VM variant with ordinary nix-darwin settings:

```nix
virtualisation.vmVariant.virtualisation = {
  macosVersion = "27.2";
  stateDirectory = "./example-vm";
  cores = 4;
  memorySize = 4096;
  graphics = true;
};
```

Tart is marked unfree in nixpkgs. Allow it through your configuration's package
policy, for example:

```nix
nixpkgs.config.allowUnfreePredicate = package: lib.getName package == "tart";
```

On a compatible Apple silicon Mac, run the launcher:

```sh
nix run .#darwinConfigurations.example.config.system.build.vm
```

The first build downloads and verifies the selected Apple IPSW. The first run
restores macOS into a 50 GB disk and boots to Setup Assistant. Later runs reuse
the disk and update its CPU and memory settings. Changing `macosVersion` does
not reinstall an existing VM; choose a new state directory for a fresh install.

State defaults to `./<hostname>-vm`, relative to the launcher's working directory.
The runner uses this directory as `TART_HOME` and names its VM `vm`. Concurrent
runner sessions using the same directory are rejected. Audio and clipboard
sharing are disabled.

Ctrl-C, SIGTERM, and SIGHUP interrupt Tart and wait for it to exit, with a forced
stop after ten seconds if needed. The disk is retained. This stops the VM; use
macOS's Shut Down command inside the guest for a clean guest shutdown.

The [image inventory guide](images/README.md) describes the pinned IPSWs and their
updater. Run `nix fmt -- --ci` and `nix flake check` for formatting and offline
tests. The subprocess tests do not start real VMs.
