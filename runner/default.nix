{
  buildGoModule,
  lib,
  nix,
  openssh,
  unixtools,
  xz,
}:
buildGoModule {
  pname = "nix-darwin-vm-runner";
  version = "0.1.0";
  src = ./.;
  vendorHash = "sha256-MOXCMY/O4xg/bMCUDQFvBf1cvnyA2iZvrlThZNSwTbs=";
  __darwinAllowLocalNetworking = true;
  nativeCheckInputs = [
    nix
    openssh
    unixtools.script
    xz
  ];

  meta = {
    description = "Runner for nix-darwin virtual machines";
    mainProgram = "runner";
    license = lib.licenses.mit;
    platforms = [ "aarch64-darwin" ];
  };
}
