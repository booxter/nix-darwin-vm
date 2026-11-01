{
  nix-darwin,
  pkgs,
  vmModule,
}:
let
  inherit (pkgs) lib;

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

  failures = lib.runTests {
    testImportPreservesParentSystem = {
      expr = minimal.config.system.build.toplevel.drvPath;
      expected = physical.config.system.build.toplevel.drvPath;
    };

    testDefaultVariantInheritsHostname = {
      expr = minimal.config.virtualisation.vmVariant.networking.hostName;
      expected = "minimal";
    };

    testDefaultResources = {
      expr = {
        inherit (minimal.config.virtualisation.vmVariant.virtualisation) cores memorySize graphics;
      };
      expected = {
        cores = 4;
        memorySize = 4096;
        graphics = true;
      };
    };

    testVariantInheritsPackages = {
      expr = builtins.elem pkgs.hello guest.environment.systemPackages;
      expected = true;
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

    testResourcesCanBeOverridden = {
      expr = {
        inherit (guest.virtualisation) cores memorySize graphics;
      };
      expected = {
        cores = 2;
        memorySize = 8192;
        graphics = false;
      };
    };

    testParentExposesVariantLauncher = {
      expr = parent.system.build.vm.drvPath;
      expected = guest.system.build.vm.drvPath;
    };

    testLauncherUsesVariantName = {
      expr = parent.system.build.vm.meta.mainProgram;
      expected = "run-guest-vm";
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
    ${lib.getExe minimal.config.system.build.vm} --help
    ${lib.getExe parent.system.build.vm} --help
    touch "$out"
  ''
