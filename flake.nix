{
  description = "Nix-darwin virtual machines";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";

  outputs =
    { nixpkgs, ... }:
    let
      pkgs = nixpkgs.legacyPackages.aarch64-darwin;
    in
    {
      darwinModules.vm = import ./modules/vm.nix;

      formatter.aarch64-darwin = pkgs.nixfmt-tree;

      devShells.aarch64-darwin.default = pkgs.mkShell { };
    };
}
