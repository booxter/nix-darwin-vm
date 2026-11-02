# macOS restore images

## Select a version

The default is the newest version in [macos.json](macos.json). Pin a version to
keep it when updating this flake:

```nix
virtualisation.vmVariant.virtualisation.macosVersion = "27.2";
```

Nix downloads and verifies the selected IPSW when building the launcher. Use a
fresh state directory to boot a different image; this option does not upgrade an
existing guest. Automatic provisioning requires macOS 27 or newer, even though
the inventory includes older releases.

## Update the inventory

From the repository root:

```sh
nix run .#update-images
```

Review the diff before committing. Each entry pins a macOS version to a build,
Apple download URL, and SHA-256 hash in Nix SRI format.

The updater discovers releases through the ipsw.me `VirtualMac2,1` catalog and
checks each image against Apple:

- The URL must use HTTPS on `updates.cdn-apple.com`, with no redirects.
- The filename must match the version and build.
- Apple's `x-amz-meta-digest-sha256` response header must match the catalog hash.

It uses HEAD requests without downloading IPSWs. Nix verifies the actual image
contents when fetching them. A catalog entry does not guarantee that the image
can be restored on every host.

Previously recorded entries are retained and rechecked, even if absent from the
catalog. Changes to an existing version's build, URL, or hash abort the update
and require manual review. Missing images or hash headers, checksum mismatches,
and network failures also leave the inventory unchanged.
