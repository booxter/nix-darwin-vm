{
  config,
  lib,
  pkgs,
  ...
}:
let
  images = builtins.fromJSON (builtins.readFile ../images/macos.json);
  imageVersions = lib.sort lib.versionOlder (builtins.attrNames images);
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
      diskSize
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
      default =
        if config.system.primaryUser != null then
          config.system.primaryUser
        else
          # nix-darwin does not require a primary user.
          "vmadmin";
      description = ''
        Administrator account created on the guest's first boot and used for
        SSH provisioning. Defaults to system.primaryUser when configured, or
        vmadmin otherwise. Changing it requires a fresh state directory.
      '';
    };

    macosVersion = lib.mkOption {
      type = lib.types.enum imageVersions;
      default = lib.last imageVersions;
      defaultText = lib.literalExpression "the highest version in the macOS image inventory";
      description = ''
        Exact macOS version of the Apple IPSW restore image to use.
        Defaults to the newest release in the committed inventory.
      '';
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

    diskSize = lib.mkOption {
      type = lib.types.ints.positive;
      # Preserve Tart's default of 50 decimal GB after rounding to whole GB.
      default = builtins.div (50 * 1000 * 1000 * 1000) (1024 * 1024);
      description = ''
        Disk size of the virtual machine in MiB (1024 × 1024 bytes), rounded
        up to whole decimal GB for Tart. Applies only when creating a new VM;
        use a fresh stateDirectory to change it.
      '';
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

  config.security.sudo.extraConfig = ''
    ${config.virtualisation.bootstrapUser} ALL=(ALL) NOPASSWD: ALL
  '';

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
        # Nix GC scans process environments; retain the configuration and its closure.
        export NIX_DARWIN_VM_CONFIG=${configuration}
        exec ${lib.getExe runner} --config "$NIX_DARWIN_VM_CONFIG" "$@"
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
