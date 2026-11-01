{
  config,
  extendModules,
  lib,
  ...
}:
let
  variant = extendModules {
    modules = [ ./virtual-machine.nix ];
  };
in
{
  options.virtualisation.vmVariant = lib.mkOption {
    inherit (variant) type;
    default = { };
    visible = "shallow";
    description = "Additional nix-darwin configuration for the virtual machine.";
  };

  config = {
    system.build.vm = lib.mkDefault config.virtualisation.vmVariant.system.build.vm;

    virtualisation.vmVariant.options.virtualisation.vmVariant = lib.mkOption {
      apply = _: throw "Nested virtualisation.vmVariant is not supported";
    };
  };
}
