# nix-darwin-vm

Import `darwinModules.vm` into a nix-darwin configuration to expose
`config.system.build.vm` and `config.system.build.vmSsh` for starting and
connecting to a macOS VM. Nix installation and nix-darwin activation are not yet
supported.

Configure the VM variant:

```nix
virtualisation.vmVariant.virtualisation = {
  macosVersion = "27.2";
  bootstrapUser = "vmadmin";
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

An Apple silicon Mac and macOS 27 or newer on both host and guest are required.
Start the VM:

```sh
nix run .#darwinConfigurations.example.config.system.build.vm
```

The first build downloads the selected Apple IPSW. The first run creates a
50 GB guest disk and the `bootstrapUser` administrator account (default `vmadmin`).
Wait for the readiness message, then connect from another terminal:

```sh
nix run .#darwinConfigurations.example.config.system.build.vmSsh

# Run a command in the guest:
nix run .#darwinConfigurations.example.config.system.build.vmSsh -- uname -a
```

`vmSsh` requires a running VM. It uses the VM's dedicated SSH key and does not
use your SSH configuration or agent.

State defaults to `./<hostname>-vm`, relative to the working directory. Run both
launchers from the same directory when using a relative state path. Account
credentials and SSH keys are stored in `<stateDirectory>/credentials`; use
`credentials/password` for guest login and sudo. Keep this directory private.

Later runs reuse the disk and credentials and apply CPU and memory settings.
Choose a fresh state directory to change `bootstrapUser` or install a different
`macosVersion`. Audio and clipboard sharing are disabled.

Ctrl-C stops the VM and retains its state. Use macOS's Shut Down command inside
the guest for a clean shutdown.

The [image inventory guide](images/README.md) describes the pinned IPSWs and their
updater. Run `nix fmt -- --ci` and `nix flake check` for formatting and tests.
