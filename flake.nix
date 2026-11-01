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
      updateImages = pkgs.callPackage ./scripts/update-images { };
      updateNixInstallers = pkgs.callPackage ./scripts/update-nix-installers { };
    in
    {
      darwinModules.vm = import ./modules/vm.nix;

      apps.aarch64-darwin.update-images = {
        type = "app";
        program = pkgs.lib.getExe updateImages;
        meta.description = "Update the macOS restore image inventory";
      };

      apps.aarch64-darwin.update-nix-installers = {
        type = "app";
        program = pkgs.lib.getExe updateNixInstallers;
        meta.description = "Update the Nix installer inventory";
      };

      checks.aarch64-darwin.module-interface = import ./tests {
        inherit nix-darwin pkgs;
        vmModule = self.darwinModules.vm;
      };
      checks.aarch64-darwin.runner = pkgs.callPackage ./runner { };
      checks.aarch64-darwin.update-images = updateImages;
      checks.aarch64-darwin.update-nix-installers = updateNixInstallers;

      formatter.aarch64-darwin = pkgs.nixfmt-tree.override {
        runtimeInputs = [ pkgs.go ];

        settings.formatter.gofmt = {
          command = "gofmt";
          options = [ "-w" ];
          includes = [ "*.go" ];
        };
      };

      devShells.aarch64-darwin.default = pkgs.mkShell {
        packages = [
          pkgs.go
          pkgs.nix
          pkgs.openssh
          pkgs.unixtools.script
          pkgs.xz
        ];
      };
    };
}
