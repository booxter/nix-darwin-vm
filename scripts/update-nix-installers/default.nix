{ buildGoModule, lib }:
buildGoModule {
  pname = "update-nix-installers";
  version = "0.1.0";
  src = ./.;
  vendorHash = null;

  meta.mainProgram = "update-nix-installers";
  meta.license = lib.licenses.mit;
  meta.platforms = [ "aarch64-darwin" ];
}
