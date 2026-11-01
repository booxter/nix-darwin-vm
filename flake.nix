{
  description = "Nix-darwin virtual machines";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";
  inputs.nix-darwin = {
    url = "github:nix-darwin/nix-darwin";
    inputs.nixpkgs.follows = "nixpkgs";
  };

  outputs =
    {
      self,
      nixpkgs,
      nix-darwin,
      ...
    }:
    let
      pkgs = nixpkgs.legacyPackages.aarch64-darwin;
    in
    {
      darwinModules.vm = import ./modules/vm.nix;

      checks.aarch64-darwin.module-interface = import ./tests {
        inherit nix-darwin pkgs;
        vmModule = self.darwinModules.vm;
      };
      checks.aarch64-darwin.runner = pkgs.callPackage ./runner { };

      formatter.aarch64-darwin = pkgs.nixfmt-tree;

      devShells.aarch64-darwin.default = pkgs.mkShell {
        packages = [ pkgs.go ];
      };
    };
}
