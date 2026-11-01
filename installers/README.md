# Nix installers

Select an installer in the VM variant:

```nix
virtualisation.vmVariant.virtualisation.nixInstaller = {
  type = "nix";
  version = "2.34.0";
};
```

The `nix` family uses the installer bundled in official Nix release tarballs.
Its installer version is also the Nix version. Only ARM Darwin artifacts are
included. The default is the highest version in the committed inventory;
pin `version` to keep that choice when updating this flake.

The selected download is available as
`virtualisation.vmVariant.system.build.nixInstaller`. Selecting an installer
does not yet install Nix in the guest.

Update the inventory from the repository root:

```sh
nix run .#update-nix-installers
```

The updater discovers stable releases in the official release bucket and reads
their published SHA-256 checksums. Historical entries are retained and checked
again. Missing recorded artifacts, changed pins, and failed requests abort the
update. Review the generated diff before committing it.
