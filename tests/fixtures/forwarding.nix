{ pkgs, user }:
let
  guestDirectory = "/Users/${user}/vm-test-sockets";
  holdSocket = pkgs.writeShellScript "hold-test-socket" ''
    printf 'held\n'
    IFS= read -r reply
    if [ "$reply" != held ]; then
      exit 1
    fi
    touch /tmp/vm-test-held
    exec /bin/cat
  '';
in
{
  virtualisation.vmVariant = {
    virtualisation.forwardedSockets = {
      activation = {
        hostPath = "activation.sock";
        guestPath = "${guestDirectory}/activation.sock";
      };
      vm = {
        hostPath = "vm.sock";
        guestPath = "${guestDirectory}/vm.sock";
        lifetime = "vm";
      };
    };
    environment.systemPackages = [ pkgs.socat ];
    system.activationScripts.postActivation.text = pkgs.lib.mkAfter ''
      rm -f /tmp/vm-test-held
      ${pkgs.socat}/bin/socat UNIX-CONNECT:${guestDirectory}/activation.sock \
        EXEC:${holdSocket} >/dev/null 2>&1 &
      socket_pid=$!
      for _ in $(seq 1 100); do
        if [ -f /tmp/vm-test-held ]; then
          break
        fi
        if ! kill -0 "$socket_pid"; then
          exit 1
        fi
        sleep 0.1
      done
      if [ ! -f /tmp/vm-test-held ] || ! kill -0 "$socket_pid"; then
        exit 1
      fi
    '';
  };
}
