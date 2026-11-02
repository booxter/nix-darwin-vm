{ buildGoModule, lib }:
buildGoModule {
  pname = "update-macos-images";
  version = "0.1.0";
  src = ./.;
  vendorHash = null;

  meta.mainProgram = "update-images";
  meta.license = lib.licenses.mit;
  meta.platforms = [ "aarch64-darwin" ];
}
