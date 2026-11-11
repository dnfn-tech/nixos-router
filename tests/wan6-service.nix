# nix build --impure --file tests/wan6-service.nix
let
  flake = builtins.getFlake (toString ../.);
  nixpkgs = flake.inputs.nixpkgs;
  pkgs = import nixpkgs { system = builtins.currentSystem; };
  state = "/tmp/router-wan6-" + builtins.substring 0 16 (builtins.hashString "sha256" (builtins.readFile ./wan6-service.nix + builtins.readFile ../modules/managed.nix));
  executor = pkgs.writeShellScriptBin "router-executor" ''
    printf '%s\n' "$@" > "$TEST_CALLBACK_ARGS"
    if test -n "''${TEST_DNS_ENV:-}"; then printf '%s' "''${NIXOS_ROUTER_IPV4_DNS:-}" > "$TEST_DNS_ENV"; fi
  '';
  client = pkgs.writeShellScriptBin "odhcp6c" ''
    printf '%s\n' "$@" > "$TEST_CLIENT_ARGS"
    while test "$#" -gt 0; do
      if test "$1" = -s; then printf '%s' "$2" > "$TEST_HOOK"; fi
      shift
    done
  '';
  systemctl = pkgs.writeShellScriptBin "systemctl" ''
    printf '%s\n' "$@" > "$TEST_SYSTEMCTL_ARGS"
  '';
  sysctl = pkgs.writeShellScriptBin "sysctl" ''
    test "''${TEST_SYSCTL_FAIL:-0}" = 0 || exit 1
    printf '%s\n' "$@" >> "$TEST_SYSCTL_ARGS"
  '';
  ip = pkgs.writeShellScriptBin "ip" "exit 0";
  cfg = (import (nixpkgs + "/nixos/lib/eval-config.nix") {
    system = builtins.currentSystem;
    modules = [ ../modules/default.nix {
      nixpkgs.overlays = [ (_: prev: { odhcp6c = client; iproute2 = prev.iproute2 // { outPath = ip; }; procps = prev.procps // { outPath = sysctl; }; systemd = prev.systemd // { outPath = systemctl; }; }) ];
      services.nixos-router.backend = {
        enable = true; package = executor; stateDir = state;
        managed = { enable = true; interfaces = [ "wan0" "lan0" ]; bootstrapConfigFile = "/run/bootstrap.json"; };
      };
      networking.useDHCP = false;
      networking.dhcpcd.enable = false;
      networking.firewall.enable = false;
      system.stateVersion = "26.05";
    } ];
  }).config;
  service = cfg.systemd.services.nixos-router-wan6.serviceConfig;
  ppp = cfg.systemd.services.nixos-router-pppoe.serviceConfig;
in pkgs.runCommand "router-wan6-service-contract" { nativeBuildInputs = [ pkgs.python3 ]; } ''
  # Also isolate fixtures when the local Nix daemon has sandboxing disabled.
  mkdir -m 700 ${state}
  trap 'rm -rf ${state}' EXIT
  export TEST_STATE_DIR=${state}
  export TEST_SYSCTL_ARGS="$TMPDIR/sysctl-args"
  export TEST_CLIENT_ARGS="$TMPDIR/client-args" TEST_CALLBACK_ARGS="$TMPDIR/callback-args" TEST_HOOK="$TMPDIR/hook"
  export WAN6_ENABLED=1 WAN6_INTERFACE=wan0 WAN6_GENERATION=generation-test WAN6_PD=1 WAN6_MODE=dhcpv6
  ${service.ExecStart}
  python - <<'PY'
  import os
  args = open(os.environ['TEST_CLIENT_ARGS']).read().splitlines()
  assert args[-6:] == ['-S', '-N', 'try', '-P', '0', 'wan0'], args
  kernel = open(os.environ['TEST_SYSCTL_ARGS']).read().splitlines()
  assert 'net/ipv6/conf/wan0/accept_ra=0' in kernel, kernel
  assert 'net/ipv6/conf/wan0/autoconf=0' in kernel, kernel
  assert 'net/ipv6/conf/wan0/disable_ipv6=0' in kernel, kernel
  assert 'net/ipv6/conf/wan0/force_forwarding=1' in kernel, kernel
  assert args[:2] == ['-e', '-s']
  assert args[3:5] == ['-p', '/run/nixos-router-wan6/client.pid']
  PY
  hook=$(cat "$TEST_HOOK")
  "$hook" wan0 bound
  python - <<'PY'
  import os
  args = open(os.environ['TEST_CALLBACK_ARGS']).read().splitlines()
  assert args == ['ipv6-event', '--state-dir', os.environ['TEST_STATE_DIR'], '--interfaces', 'wan0,lan0', '--wifi-interface', "", '--management-port', '8080', '--generation', 'generation-test', '--interface', 'wan0', '--event', 'bound'], args
  PY
  rm "$TEST_CALLBACK_ARGS"
  if "$hook" foreign0 bound; then exit 1; fi
  test ! -e "$TEST_CALLBACK_ARGS"
  export WAN6_MODE=slaac WAN6_PD=0
  ${service.ExecStart}
  python - <<'PY'
  import os
  args = open(os.environ['TEST_CLIENT_ARGS']).read().splitlines()
  assert args[-3:] == ['-N', 'none', 'wan0'], args
  assert '-P' not in args and '-S' not in args
  PY
  rm "$TEST_CLIENT_ARGS"
  export TEST_SYSCTL_FAIL=1
  if ${service.ExecStart}; then exit 1; fi
  test ! -e "$TEST_CLIENT_ARGS"
  unset TEST_SYSCTL_FAIL
  export WAN6_MODE=invalid
  if ${service.ExecStart}; then exit 1; fi
  mkdir -p ${state}/managed/generation-test
  echo synthetic-options > ${state}/managed/generation-test/pppoe.options
  printf '%s\n' 'WAN6_ENABLED=1' 'WAN6_MODE=pppoe6' 'WAN6_GENERATION=generation-test' > ${state}/managed/generation-test/wan6.env
  ln -s generation-test ${state}/managed/current
  export WAN6_MODE=pppoe6 WAN6_INTERFACE=nr-ppp0
  export TEST_SYSTEMCTL_ARGS="$TMPDIR/systemctl-args"
  export TEST_PPP_START=${pkgs.lib.escapeShellArg ppp.ExecStart}
  export TEST_WAN_STOP=${pkgs.lib.escapeShellArg cfg.systemd.services.nixos-router-wan.serviceConfig.ExecStopPost}
  export TEST_WAN_START=${pkgs.lib.escapeShellArg cfg.systemd.services.nixos-router-wan.serviceConfig.ExecStart}
  export TEST_DNS_ENV="$TMPDIR/dns-env"
  export TEST_PPP_STOP=${pkgs.lib.escapeShellArg ppp.ExecStopPost}
  python - <<'PY'
  import os, pathlib, shlex, subprocess
  args = shlex.split(os.environ['TEST_PPP_START'])
  log = pathlib.Path(os.environ['TEST_SYSTEMCTL_ARGS'])
  assert 'net-pre-up-script' in args and 'ip-pre-up-script' not in args
  for kind, action in [('ipv6-up-script', 'restart'), ('ipv6-down-script', 'stop')]:
      hook = args[args.index(kind) + 1]
      # pppd constructs a fresh script_env; service environment is not inherited.
      hook_env = {'TEST_SYSTEMCTL_ARGS': str(log)}
      subprocess.run([hook, 'nr-ppp0', 'tty', '0', 'fe80::1', 'fe80::2', 'generation-test'], env=hook_env, check=True)
      assert log.read_text().splitlines() == (['--no-block'] if action == 'stop' else []) + [action, 'nixos-router-wan6.service']
      log.unlink()
      subprocess.run([hook, 'nr-ppp0', 'tty', '0', 'fe80::1', 'fe80::2', 'generation-stale'], env=hook_env, check=True)
      assert not log.exists()
  subprocess.run([os.environ['TEST_PPP_STOP']], check=True)
  assert log.read_text().splitlines() == ['--no-block', 'stop', 'nixos-router-wan6.service']
  log.unlink()
  os.environ['WAN6_ENABLED'] = '0'
  subprocess.run([os.environ['TEST_PPP_STOP']], check=True)
  assert not log.exists()
  PY
  python - <<'PYTEST'
  import os, pathlib, shlex, subprocess
  log = pathlib.Path(os.environ['TEST_CALLBACK_ARGS'])
  envlog = pathlib.Path(os.environ['TEST_DNS_ENV'])
  def check(event, iface, servers):
      args = log.read_text().splitlines()
      assert args[0] == 'dns-event', args
      assert args[-6:] == ['--generation', 'generation-test', '--interface', iface, '--event', event], args
      assert envlog.read_text() == servers
      log.unlink()
  args = shlex.split(os.environ['TEST_PPP_START'])
  for kind, event in [('ip-up-script', 'bound'), ('ip-down-script', 'unbound')]:
      hook = args[args.index(kind) + 1]
      env = {'TEST_CALLBACK_ARGS': str(log), 'TEST_DNS_ENV': str(envlog), 'DNS1':'1.1.1.1', 'DNS2':'8.8.8.8'}
      log.unlink(missing_ok=True)
      subprocess.run([hook, 'nr-ppp0', 'tty', '0', '10.0.0.1', '10.0.0.2', 'generation-test'], env=env, check=True)
      check(event, 'nr-ppp0', '1.1.1.1 8.8.8.8' if event == 'bound' else "")
  stop_env = {'TEST_CALLBACK_ARGS': str(log), 'TEST_DNS_ENV': str(envlog), 'WAN6_GENERATION': 'generation-test', 'WAN6_ENABLED': '0'}
  subprocess.run([os.environ['TEST_PPP_STOP']], env=stop_env, check=True)
  check('unbound', 'nr-ppp0', "")
  args = shlex.split(os.environ['TEST_WAN_START'])
  hook = args[args.index('-s') + 1]
  state = pathlib.Path(os.environ['TEST_STATE_DIR'])
  (state/'managed/generation-test/wan.env').write_text("WAN_GENERATION='generation-test'\n")
  env = dict(os.environ, WAN_GENERATION='generation-test', WAN_INTERFACE='wan0', interface='wan0', ip='192.0.2.2', subnet='255.255.255.0', router='192.0.2.1', dns='9.9.9.9')
  for action,event in [('bound','bound'),('renew','bound'),('deconfig','unbound')]:
      subprocess.run([hook, action], env=env, check=True)
      check(event, 'wan0', '9.9.9.9' if event == 'bound' else "")
  subprocess.run([os.environ['TEST_WAN_STOP']], env=env, check=True)
  check('unbound', 'wan0', "")
  PYTEST
  touch "$out"
''
