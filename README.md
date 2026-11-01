# nix-darwin-vm

Import `darwinModules.vm` into a nix-darwin configuration to expose
`config.system.build.vm` and `config.system.build.vmSsh` for starting and
connecting to a macOS VM running the variant's nix-darwin configuration.

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

Add `-- --interactive` to attach a guest shell in the same terminal after
activation. Logging out leaves the VM running; press Enter to reconnect, or
Ctrl-C or Ctrl-D at the reconnect prompt to stop the VM. Interactive mode is
independent of `graphics` and requires a terminal. Omit it for CI.

The first build downloads the selected Apple IPSW and Nix installer. The first
run creates a 50 GB guest disk, creates the `bootstrapUser` administrator account
(default `vmadmin`), and installs Nix for all users.
The VM variant's configuration is built on the host, copied, and activated at
startup. Later runs transfer only missing store paths.
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

Copied configurations are retained in the guest profile
`~/.local/state/nix-darwin-vm/system`. The bootstrap account is trusted by the
guest's Nix daemon for configuration uploads.

Later runs reuse the disk, credentials, and Nix installation, and apply CPU and
memory settings.
Choose a fresh state directory to change `bootstrapUser`, `macosVersion`, or the
bootstrap Nix installer. After activation, the configuration's `nix.package`
controls Nix when `nix.enable` is enabled. Audio and clipboard sharing are disabled.

Ctrl-C stops the VM during startup or at the reconnect prompt and retains its
state. Inside an attached shell, Ctrl-C goes to the guest. Use macOS's Shut Down
command inside the guest for a clean shutdown.

The [image inventory guide](images/README.md) describes the pinned IPSWs and their
updater. The [installer inventory guide](installers/README.md) describes Nix
installer selection. Run `nix fmt -- --ci` and `nix flake check` for formatting
and fast tests. Run `nix run .#test-vm` for the opt-in
[real VM integration scenarios](tests/README.md).
