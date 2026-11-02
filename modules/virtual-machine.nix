{
  config,
  lib,
  pkgs,
  ...
}:
let
  images = builtins.fromJSON (builtins.readFile ../images/macos.json);
  image = images.${config.virtualisation.macosVersion};
  installers = builtins.fromJSON (builtins.readFile ../installers/nix.json);
  installer = config.virtualisation.nixInstaller;
  installerReleases = installers.${installer.type};
  installerVersions = lib.sort lib.versionOlder (builtins.attrNames installerReleases);
  runner = pkgs.callPackage ../runner { };
  configuration = (pkgs.formats.json { }).generate "vm-config.json" {
    name = config.networking.hostName;
    inherit (config.virtualisation)
      macosVersion
      bootstrapUser
      cores
      memorySize
      graphics
      stateDirectory
      ;
    ipsw = config.system.build.ipsw;
    system = config.system.build.toplevel;
    nixInstaller = {
      inherit (installer) type version;
      path = config.system.build.nixInstaller;
    };
  };
in
{
  options.virtualisation = {
    bootstrapUser = lib.mkOption {
      type = lib.types.strMatching "[a-zA-Z_][a-zA-Z0-9_.-]*";
      default = "vmadmin";
      description = ''
        Administrator account created on the guest's first boot and used for
        SSH provisioning. Changing it requires a fresh state directory.
      '';
    };

    macosVersion = lib.mkOption {
      type = lib.types.enum (builtins.attrNames images);
      default = "27.2";
      description = "Exact macOS version of the Apple IPSW restore image to use.";
    };

    nixInstaller = {
      type = lib.mkOption {
        type = lib.types.enum (builtins.attrNames installers);
        default = "nix";
        description = "Installer family. The nix family uses the installer bundled with Nix releases.";
      };

      version = lib.mkOption {
        type = lib.types.enum installerVersions;
        default = lib.last installerVersions;
        defaultText = lib.literalExpression "the highest version in the selected installer's inventory";
        description = ''
          Installer release version. For the nix family this is also the Nix
          version used for bootstrap. After activation, nix.package controls
          managed Nix. Defaults to the newest release in the committed inventory.
        '';
      };
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

  config.nix.settings.trusted-users = lib.mkIf config.nix.enable (
    lib.mkAfter [ config.virtualisation.bootstrapUser ]
  );

  config.system.build = {
    ipsw = pkgs.fetchurl {
      inherit (image) url hash;
    };

    nixInstaller = pkgs.fetchurl {
      inherit (installerReleases.${installer.version}) url hash;
    };

    vm = pkgs.writeShellApplication {
      name = "run-${config.networking.hostName}-vm";
      runtimeInputs = [
        pkgs.darwin.DarwinTools
        pkgs.tart
        pkgs.nix
        pkgs.openssh
      ];
      text = ''
        exec ${lib.getExe runner} --config ${configuration} "$@"
      '';
    };

    vmSsh = pkgs.writeShellApplication {
      name = "ssh-${config.networking.hostName}-vm";
      runtimeInputs = [
        pkgs.tart
        pkgs.openssh
      ];
      text = ''
        exec ${lib.getExe runner} ssh \
          --state-directory ${lib.escapeShellArg config.virtualisation.stateDirectory} \
          -- "$@"
      '';
    };
  };
}
