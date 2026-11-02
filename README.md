# nix-darwin-vm

Run your nix-darwin configuration in a local macOS VM using Tart. The VM inherits
your configuration; `virtualisation.vmVariant` lets you customize it for the guest
without changing the host configuration.

## Requirements

- An Apple silicon Mac with Nix.
- macOS 27 or newer on both host and guest, for automatic account provisioning.

## Setup

Add this input to your existing flake:

```nix
inputs.nix-darwin-vm = {
  url = "github:booxter/nix-darwin-vm";
  inputs.nixpkgs.follows = "nixpkgs";
};
```

Import its module in your `darwinConfigurations.<name>` module list. Here,
`inputs` is the argument to your flake's `outputs` function:

```nix
modules = [
  inputs.nix-darwin-vm.darwinModules.vm
  # Your existing modules.
];
```

Allow Tart, which nixpkgs marks as unfree, in your nix-darwin configuration:

```nix
{ lib, ... }:
{
  nixpkgs.config.allowUnfreePredicate = package: lib.getName package == "tart";
}
```

## Start and connect

Replace `example` with your configuration's name:

```sh
nix run .#darwinConfigurations.example.config.system.build.vm
```

The build downloads the selected macOS restore image (IPSW) and Nix installer,
and builds the guest configuration on the host. The first run creates the guest
disk and an administrator account (`vmadmin` by default), installs
Nix for all users, then copies and activates the configuration over SSH.

Wait for `Ready: guest configuration activated`, then connect from another terminal:

```sh
nix run .#darwinConfigurations.example.config.system.build.vmSsh

# Run a command in the guest:
nix run .#darwinConfigurations.example.config.system.build.vmSsh -- uname -a
```

`vmSsh` requires a running VM. It uses a dedicated SSH key and ignores your SSH
configuration and agent. With a relative state path, run both launchers from the
same directory.

To attach a shell directly after activation:

```sh
nix run .#darwinConfigurations.example.config.system.build.vm -- --interactive
```

After logout, press Enter to reconnect, or Ctrl-C or Ctrl-D at the reconnect
prompt to stop the VM. Interactive mode requires a terminal and works with or
without a display window. Omit it for CI.

Ctrl-C stops the launcher and VM, preserving state. Inside an attached shell,
Ctrl-C goes to the guest. For a clean shutdown, use macOS's Shut Down command
inside the guest.

## Configure the guest

Set VM options inside your nix-darwin configuration (with `pkgs` in scope):

```nix
virtualisation.vmVariant = {
  virtualisation = {
    cores = 4;
    memorySize = 4096;
    diskSize = 64 * 1024;
    graphics = false;
    stateDirectory = "./example-vm";
  };

  # Ordinary nix-darwin options also work here.
  environment.systemPackages = [ pkgs.hello ];
};
```

| Option under `virtualisation.vmVariant.virtualisation` | Default |
| --- | --- |
| `cores` | `4` |
| `memorySize` | `4096` MiB |
| `diskSize` | `47683` MiB (50 decimal GB after rounding) |
| `graphics` | `true` (opens a display window) |
| `stateDirectory` | `./<hostname>-vm`, relative to the launcher's working directory |
| `bootstrapUser` | `"vmadmin"` |
| `macosVersion` | Newest version in the [image inventory](images/README.md) |
| `nixInstaller` | Newest official Nix release in the [installer inventory](installers/README.md) |

Audio and clipboard sharing are disabled.

`diskSize` is in MiB, rounded up to whole decimal GB for Tart. It applies only
when creating a VM; changing it does not resize an existing disk. Use a fresh
`stateDirectory` to select a different disk size.

## State and subsequent runs

Each state directory holds one VM's disk and credentials. Later runs reuse the
disk and Nix installation, apply CPU and memory settings, and activate the current
configuration. Only missing store paths are transferred.

Use a fresh `stateDirectory` to change the bootstrap account, macOS restore image,
or bootstrap Nix installer. Selecting a different image does not upgrade an
existing guest. After activation, `nix.package` controls Nix when `nix.enable` is
enabled.

Guest login and sudo use the password in `<stateDirectory>/credentials/password`.
That directory also contains SSH keys; keep it private.

The bootstrap account is trusted by the guest's Nix daemon so it can upload
configurations. Uploaded configurations are retained in the guest profile
`~/.local/state/nix-darwin-vm/system` to protect them from garbage collection.

## Development

See [contributing](CONTRIBUTING.md) for checks and CI maintenance, and
[VM integration tests](tests/README.md) for testing with real guests.
