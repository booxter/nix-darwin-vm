{
  config,
  lib,
  pkgs,
  ...
}:
let
  runner = pkgs.callPackage ../runner { };
  configuration = (pkgs.formats.json { }).generate "vm-config.json" {
    name = config.networking.hostName;
    inherit (config.virtualisation) cores memorySize graphics;
  };
in
{
  options.virtualisation = {
    cores = lib.mkOption {
      type = lib.types.ints.positive;
      default = 4;
      description = "Number of virtual CPU cores.";
    };

    memorySize = lib.mkOption {
      type = lib.types.ints.positive;
      default = 4096;
      description = "Memory size of the virtual machine in MiB (1024 × 1024 bytes).";
    };

    graphics = lib.mkOption {
      type = lib.types.bool;
      default = true;
      description = "Whether to open the virtual machine's display window.";
    };
  };

  config.system.build.vm = pkgs.writeShellApplication {
    name = "run-${config.networking.hostName}-vm";
    text = ''
      exec ${lib.getExe runner} --config ${configuration} "$@"
    '';
  };
}
