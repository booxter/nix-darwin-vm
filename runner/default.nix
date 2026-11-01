{ buildGoModule, lib }:
buildGoModule {
  pname = "nix-darwin-vm-runner";
  version = "0.1.0";
  src = ./.;
  vendorHash = null;

  meta = {
    description = "Runner for nix-darwin virtual machines";
    mainProgram = "runner";
    license = lib.licenses.mit;
    platforms = [ "aarch64-darwin" ];
  };
}
