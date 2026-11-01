{
  config,
  lib,
  pkgs,
  ...
}:
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
      echo "The macOS VM runner is not implemented yet" >&2
      exit 1
    '';
  };
}
