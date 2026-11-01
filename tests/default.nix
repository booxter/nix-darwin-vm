{
  nix-darwin,
  pkgs,
  vmModule,
}:
let
  inherit (pkgs) lib;
  images = builtins.fromJSON (builtins.readFile ../images/macos.json);
  installers = builtins.fromJSON (builtins.readFile ../installers/nix.json);
  defaultInstaller = minimal.config.virtualisation.vmVariant.virtualisation.nixInstaller;

  physical = nix-darwin.lib.darwinSystem {
    modules = [ ./fixtures/minimal.nix ];
  };
  minimal = physical.extendModules {
    modules = [ vmModule ];
  };
  customized = nix-darwin.lib.darwinSystem {
    modules = [
      vmModule
      ./fixtures/customized.nix
    ];
  };
  parent = customized.config;
  guest = parent.virtualisation.vmVariant;
  nested = minimal.extendModules {
    modules = [
      {
        virtualisation.vmVariant.virtualisation.vmVariant = { };
      }
    ];
  };
  unknownVersion = minimal.extendModules {
    modules = [
      {
        virtualisation.vmVariant.virtualisation.macosVersion = "unknown";
      }
    ];
  };
  unknownInstallerType = minimal.extendModules {
    modules = [
      { virtualisation.vmVariant.virtualisation.nixInstaller.type = "unknown"; }
    ];
  };
  unknownInstallerVersion = minimal.extendModules {
    modules = [
      { virtualisation.vmVariant.virtualisation.nixInstaller.version = "unknown"; }
    ];
  };
  renamed = minimal.extendModules {
    modules = [
      {
        virtualisation.vmVariant.networking.hostName = lib.mkForce "renamed";
      }
    ];
  };

  expectedIPSW =
    version:
    pkgs.fetchurl {
      inherit (images.${version}) url hash;
    };

  expectedInstaller =
    version:
    pkgs.fetchurl {
      inherit (installers.nix.${version}) url hash;
    };

  withFixtureInputs =
    configuration:
    configuration.extendModules {
      modules = [
        {
          virtualisation.vmVariant.system.build.ipsw = lib.mkForce (
            pkgs.writeText "fixture.ipsw" "test restore image"
          );
          virtualisation.vmVariant.system.build.nixInstaller = lib.mkForce (
            pkgs.writeText "fixture.tar.xz" "test installer"
          );
          virtualisation.vmVariant.system.build.toplevel = lib.mkForce (
            pkgs.writeText "fixture-system" "test configuration"
          );
        }
      ];
    };

  failures = lib.runTests {
    testImportPreservesParentSystem = {
      expr = minimal.config.system.build.toplevel.drvPath;
      expected = physical.config.system.build.toplevel.drvPath;
    };

    testDefaultVariantInheritsHostname = {
      expr = minimal.config.virtualisation.vmVariant.networking.hostName;
      expected = "minimal";
    };

    testDefaultSettings = {
      expr = {
        inherit (minimal.config.virtualisation.vmVariant.virtualisation)
          macosVersion
          bootstrapUser
          stateDirectory
          cores
          memorySize
          graphics
          ;
      };
      expected = {
        macosVersion = "27.2";
        bootstrapUser = "vmadmin";
        stateDirectory = "./minimal-vm";
        cores = 4;
        memorySize = 4096;
        graphics = true;
      };
    };

    testVariantInheritsPackages = {
      expr = builtins.elem pkgs.hello guest.environment.systemPackages;
      expected = true;
    };

    testBootstrapUserCanUploadConfigurations = {
      expr = builtins.elem "builder" guest.nix.settings.trusted-users;
      expected = true;
    };

    testBootstrapTrustStaysOutOfParent = {
      expr = builtins.elem "builder" parent.nix.settings.trusted-users;
      expected = false;
    };

    testCustomModuleIsEvaluatedForEachConfiguration = {
      expr = {
        parent = parent.environment.etc."message".text;
        guest = guest.environment.etc."message".text;
      };
      expected = {
        parent = "parent";
        guest = "guest";
      };
    };

    testVariantChangesStayOutOfParent = {
      expr = {
        parentHostname = parent.networking.hostName;
        guestHostname = guest.networking.hostName;
        parentHasFile = parent.environment.etc ? "vm-only";
        guestFile = guest.environment.etc."vm-only".text;
      };
      expected = {
        parentHostname = "customized";
        guestHostname = "guest";
        parentHasFile = false;
        guestFile = "guest only";
      };
    };

    testVariantSettingsCanBeOverridden = {
      expr = {
        inherit (guest.virtualisation)
          macosVersion
          bootstrapUser
          stateDirectory
          cores
          memorySize
          graphics
          ;
      };
      expected = {
        macosVersion = "26.6.2";
        bootstrapUser = "builder";
        stateDirectory = "./custom vm";
        cores = 2;
        memorySize = 8192;
        graphics = false;
      };
    };

    testStateDirectoryDefaultsToVariantName = {
      expr = renamed.config.virtualisation.vmVariant.virtualisation.stateDirectory;
      expected = "./renamed-vm";
    };

    testDefaultVersionSelectsPinnedImage = {
      expr = minimal.config.virtualisation.vmVariant.system.build.ipsw.outPath;
      expected = (expectedIPSW "27.2").outPath;
    };

    testCustomVersionSelectsPinnedImage = {
      expr = guest.system.build.ipsw.outPath;
      expected = (expectedIPSW "26.6.2").outPath;
    };

    testUnknownVersionIsRejected = {
      expr =
        (builtins.tryEval unknownVersion.config.virtualisation.vmVariant.virtualisation.macosVersion)
        .success;
      expected = false;
    };

    testDefaultInstallerFamily = {
      expr = defaultInstaller.type;
      expected = "nix";
    };

    testDefaultInstallerIsNewest = {
      expr = builtins.all (version: !lib.versionOlder defaultInstaller.version version) (
        builtins.attrNames installers.nix
      );
      expected = true;
    };

    testDefaultInstallerSelectsPinnedDownload = {
      expr = minimal.config.virtualisation.vmVariant.system.build.nixInstaller.outPath;
      expected = (expectedInstaller defaultInstaller.version).outPath;
    };

    testInstallerVersionCanBePinned = {
      expr = guest.virtualisation.nixInstaller;
      expected = {
        type = "nix";
        version = "2.34.0";
      };
    };

    testPinnedInstallerSelectsPinnedDownload = {
      expr = guest.system.build.nixInstaller.outPath;
      expected = (expectedInstaller "2.34.0").outPath;
    };

    testUnknownInstallerFamilyIsRejected = {
      expr =
        (builtins.tryEval unknownInstallerType.config.virtualisation.vmVariant.virtualisation.nixInstaller.type)
        .success;
      expected = false;
    };

    testUnknownInstallerVersionIsRejected = {
      expr =
        (builtins.tryEval unknownInstallerVersion.config.virtualisation.vmVariant.virtualisation.nixInstaller.version)
        .success;
      expected = false;
    };

    testParentExposesVariantLauncher = {
      expr = parent.system.build.vm.drvPath;
      expected = guest.system.build.vm.drvPath;
    };

    testParentExposesVariantSSHLauncher = {
      expr = parent.system.build.vmSsh.drvPath;
      expected = guest.system.build.vmSsh.drvPath;
    };

    testLauncherUsesVariantName = {
      expr = parent.system.build.vm.meta.mainProgram;
      expected = "run-guest-vm";
    };

    testSSHLauncherUsesVariantName = {
      expr = parent.system.build.vmSsh.meta.mainProgram;
      expected = "ssh-guest-vm";
    };

    testNestedVariantIsRejected = {
      expr = (builtins.tryEval nested.config.virtualisation.vmVariant.virtualisation.vmVariant).success;
      expected = false;
    };
  };
in
if failures != [ ] then
  throw "VM module interface tests failed: ${builtins.toJSON failures}"
else
  pkgs.runCommand "vm-module-interface" { } ''
    ${lib.getExe (withFixtureInputs minimal).config.system.build.vm} --help
    ${lib.getExe (withFixtureInputs customized).config.system.build.vm} --help
    for launcher in \
      ${lib.getExe minimal.config.system.build.vmSsh} \
      ${lib.getExe customized.config.system.build.vmSsh}; do
      if "$launcher" true >ssh-output 2>&1; then
        echo "SSH launcher unexpectedly connected without VM state" >&2
        exit 1
      fi
      grep -Fq "VM is not prepared" ssh-output
    done
    test ! -e minimal-vm
    test ! -e 'custom vm'
    touch "$out"
  ''
