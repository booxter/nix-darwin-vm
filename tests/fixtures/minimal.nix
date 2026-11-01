{ lib, ... }:
{
  nixpkgs.hostPlatform = "aarch64-darwin";
  nixpkgs.config.allowUnfreePredicate = package: lib.getName package == "tart";
  networking.hostName = lib.mkDefault "minimal";
  system.stateVersion = 6;
}
