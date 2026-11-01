{ lib, ... }:
{
  nixpkgs.hostPlatform = "aarch64-darwin";
  networking.hostName = lib.mkDefault "minimal";
  system.stateVersion = 6;
}
