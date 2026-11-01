{
  config,
  lib,
  pkgs,
  ...
}:
{
  imports = [ ./minimal.nix ];

  options.example.message = lib.mkOption {
    type = lib.types.str;
    default = "parent";
  };

  config = {
    networking.hostName = "customized";
    environment.systemPackages = [ pkgs.hello ];
    environment.etc."message".text = config.example.message;

    virtualisation.vmVariant = {
      example.message = "guest";
      networking.hostName = lib.mkForce "guest";

      virtualisation = {
        macosVersion = "26.6.2";
        bootstrapUser = "builder";
        stateDirectory = "./custom vm";
        cores = 2;
        memorySize = 8192;
        graphics = false;
      };

      environment.etc."vm-only".text = "guest only";
    };
  };
}
