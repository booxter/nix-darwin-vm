{
  buildGoModule,
  lib,
  nix,
  openssh,
  xz,
}:
buildGoModule {
  pname = "nix-darwin-vm-runner";
  version = "0.1.0";
  src = ./.;
  vendorHash = "sha256-coHCpgjyAarT1N2nJ63eXpv3doWTrORMq94YLhcQT3A=";
  __darwinAllowLocalNetworking = true;
  nativeCheckInputs = [
    nix
    openssh
    xz
  ];

  meta = {
    description = "Runner for nix-darwin virtual machines";
    mainProgram = "runner";
    license = lib.licenses.mit;
    platforms = [ "aarch64-darwin" ];
  };
}
