{
  config,
  lib,
  pkgs,
  ...
}:
let
  images = builtins.fromJSON (builtins.readFile ../images/macos.json);
  image = images.${config.virtualisation.macosVersion};
  runner = pkgs.callPackage ../runner { };
  configuration = (pkgs.formats.json { }).generate "vm-config.json" {
    name = config.networking.hostName;
    inherit (config.virtualisation)
      cores
      memorySize
      graphics
      stateDirectory
      ;
    ipsw = config.system.build.ipsw;
  };
in
{
  options.virtualisation = {
    macosVersion = lib.mkOption {
      type = lib.types.enum (builtins.attrNames images);
      default = "27.2";
      description = "Exact macOS version of the Apple IPSW restore image to use.";
    };

    stateDirectory = lib.mkOption {
      type = lib.types.str;
      default = "./${config.networking.hostName}-vm";
      defaultText = lib.literalExpression ''"./''${config.networking.hostName}-vm"'';
      description = ''
        Directory for persistent VM state. Relative paths are resolved from
        the launcher's working directory at runtime.
      '';
    };

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

  config.system.build = {
    ipsw = pkgs.fetchurl {
      inherit (image) url hash;
    };

    vm = pkgs.writeShellApplication {
      name = "run-${config.networking.hostName}-vm";
      runtimeInputs = [ pkgs.tart ];
      text = ''
        exec ${lib.getExe runner} --config ${configuration} "$@"
      '';
    };
  };
}
