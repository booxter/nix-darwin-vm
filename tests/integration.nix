{
  nix-darwin,
  pkgs,
  vmModule,
}:
let
  inherit (pkgs) lib;
  user = "vmtester";
  stateDirectory = "VM's 100% state";

  configuration =
    message:
    nix-darwin.lib.darwinSystem {
      modules = [
        vmModule
        ./fixtures/minimal.nix
        {
          virtualisation.vmVariant = {
            networking.hostName = lib.mkForce "vm-integration";
            virtualisation = {
              bootstrapUser = user;
              graphics = false;
              stateDirectory = "./${stateDirectory}";
            };
            environment.etc."vm-test".text = "${message}\n";
          };
        }
      ];
    };

  initial = configuration "initial";
  updated = configuration "updated";
  fixture =
    system: message:
    let
      guest = system.config.virtualisation.vmVariant;
    in
    {
      run = lib.getExe system.config.system.build.vm;
      ssh = lib.getExe system.config.system.build.vmSsh;
      system = guest.system.build.toplevel;
      nixVersion = lib.getVersion guest.nix.package;
      inherit message;
    };

  fixtures = (pkgs.formats.json { }).generate "vm-integration-config.json" {
    inherit user stateDirectory;
    tart = lib.getExe initial.pkgs.tart;
    initial = fixture initial "initial";
    updated = fixture updated "updated";
  };

  driver = (pkgs.callPackage ../runner { }).overrideAttrs {
    pname = "vm-integration-tests";
    # Only compile here: the tests need Virtualization.framework at runtime.
    doCheck = false;
    buildPhase = ''
      runHook preBuild
      go test -c -tags integration -o vm-integration-tests .
      runHook postBuild
    '';
    installPhase = ''
      runHook preInstall
      install -Dm755 vm-integration-tests "$out/bin/vm-integration-tests"
      runHook postInstall
    '';
  };
in
pkgs.writeShellApplication {
  name = "test-vm";
  runtimeInputs = [ pkgs.unixtools.script ];
  text = ''
    exec ${driver}/bin/vm-integration-tests \
      -test.v -test.timeout=45m -test.run=TestIntegration \
      -integration-config ${fixtures} "$@"
  '';
}
