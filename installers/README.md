# Nix installers

## Select a version

The default is the newest version in [nix.json](nix.json). Pin a version to keep
it when updating this flake:

```nix
virtualisation.vmVariant.virtualisation.nixInstaller = {
  type = "nix";
  version = "2.34.0";
};
```

The `nix` family uses official Nix release tarballs for ARM Darwin. The installer
version is the Nix version installed in a fresh guest. The download is exposed as
`virtualisation.vmVariant.system.build.nixInstaller`.

Use a fresh state directory to change the bootstrap installer. After activation,
`nix.package` controls Nix when `nix.enable` is enabled.

## Update the inventory

From the repository root:

```sh
nix run .#update-nix-installers
```

Review the diff before committing. The updater discovers stable releases in the
official Nix release bucket and reads their published SHA-256 checksums. Releases
without ARM Darwin artifacts are skipped.

Previously recorded entries are retained and rechecked. Missing recorded
artifacts, changed pins, and failed requests abort the update without replacing
the inventory.
