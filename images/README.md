# macOS restore images

`macos.json` pins Apple IPSW restore images by exact macOS version, with their
build identifiers, Apple download URLs, and SHA-256 hashes in Nix SRI format.

Select an inventory entry in a VM variant:

```nix
virtualisation.vmVariant.virtualisation.macosVersion = "27.2";
```

The default is pinned to `27.2`. Nix fetches and verifies the selected IPSW when
building the VM launcher, and passes its store path to the runner. Updating the
inventory does not change the default version.

Run the updater from the repository root:

```sh
nix run .#update-images
```

The updater uses the ipsw.me `VirtualMac2,1` catalog for discovery, including
historical releases. It requires HTTPS image URLs on `updates.cdn-apple.com`
and filenames matching the advertised macOS version and build. For each image,
it sends a HEAD request to Apple and compares `x-amz-meta-digest-sha256` with the
catalog's checksum. Redirects are rejected. No IPSW contents are downloaded.

Previously recorded releases are retained and checked again even when they
disappear from the discovery catalog. Changes to an existing version's build,
URL, or checksum require manual review. Missing images, missing or mismatched
hash headers, and network failures abort the update. The inventory is replaced
atomically only after every record passes verification.

Review the generated diff before committing it. The recorded hashes come from
Apple's response metadata; Nix must still verify the image contents when fetching
an IPSW. Catalog membership does not establish that an image can be restored on
every host.
