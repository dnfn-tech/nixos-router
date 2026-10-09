{
  description = "NixOS 路由器通用模块 + Go 后端（内嵌 WebUI）";

  # M14: 升级到能提供 Go ≥ 1.26 的 nixpkgs（优先稳定，不满足则使用 unstable）
  # 24.05 的 Go 为 1.22.x，会阻塞本仓库（go.mod 要求 ≥1.26）
  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
  inputs.flake-utils.url = "github:numtide/flake-utils";

  outputs = { self, nixpkgs, flake-utils }:
    flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = import nixpkgs { inherit system; };
        lib = pkgs.lib;
        routerdPkg = pkgs.buildGoModule {
          pname = "routerd";
          version = "0.1.0";
          src = ./.;
          # 指定 Go 模块根，便于 vendorHash 计算与后续 vendor 固定
          modRoot = "./backend";
          subPackages = [ "cmd/routerd" ];
          # 已在有 Nix 的主机上验证：nix build .#routerd 成功
          vendorHash = "sha256-MM1ODEBButuG1Yalmyxv1mkJmc4Va4tclJpq1q0IAcc=";
        };
      in
      {
        packages = {
          default = routerdPkg;
          routerd = routerdPkg;
        };
        apps.default = {
          type = "app";
          program = "${routerdPkg}/bin/routerd";
        };
      }
    ) // {
      nixosModules.default = ./modules;
      # 仅 Linux 提供 VM 测试（flake checks）
      checks."x86_64-linux" = let
        pkgs = import nixpkgs { system = "x86_64-linux"; };
      in {
        vm-router = pkgs.testers.runNixOSTest {
          name = "vm-router";
          nodes = import ./tests/vm-router.nix { inherit pkgs self; };
          testScript = ''
            import json, time
            start_all()
            # upstream: dnsmasq (dhcp+dns)
            upstream.wait_for_unit("dnsmasq.service")
            # router: backend + dnsmasq
            router.wait_for_unit("nixos-router-backend.service")
            router.wait_for_unit("dnsmasq.service")
            # client up
            client.wait_for_unit("network-online.target")

            # 1) backend 端口 8080 打开
            router.wait_until_succeeds("ss -ltn | grep ':8080'")

            # 2) GET /api/v1/status 正常
            router.succeed("curl -sS -f http://127.0.0.1:8080/api/v1/status >/dev/null")

            # 3) POST /api/v1/session 登录成功（dev 模式下种子 admin/adminadmin）
            out = router.succeed("curl -sS -w '%{http_code}' -o /dev/null -H 'Content-Type: application/json' -c /root/cookie.txt -d '{\"username\":\"admin\",\"password\":\"adminadmin\"}' http://127.0.0.1:8080/api/v1/session")
            assert out.strip().endswith("201"), f\"unexpected login status: {out}\"

            # 4) client 拿到 LAN DHCP 租约（192.168.1.0/24）
            client.wait_until_succeeds(\"ip -4 addr show dev eth1 | grep -E 'inet 192\\\\.168\\\\.1\\\\.'\")

            # 5) client 经 NAT 能 ping 通 10.0.0.1（上游 upstream）
            client.succeed("ping -c1 -W2 10.0.0.1")

            # 6) client 经 dnsmasq 解析 test.isp -> 10.0.0.1
            client.succeed(\"getent hosts test.isp | grep '10.0.0.1'\")

            # 7) 通过 API 发起 apply 作业并轮询至成功（generate-only）
            apply_json = router.succeed("curl -sS -f -b /root/cookie.txt -X POST http://127.0.0.1:8080/api/v1/apply")
            job = json.loads(apply_json)
            assert job.get("jobId"), f\"apply missing jobId: {apply_json}\"
            jid = job["jobId"]
            for _ in range(50):
              time.sleep(0.2)
              jraw = router.succeed(f"curl -sS -f http://127.0.0.1:8080/api/v1/jobs/{jid}")
              jr = json.loads(jraw)
              st = jr.get("status", "")
              if st in ("success", "failed"):
                assert st == "success", f\"apply job failed: {jraw}\"
                break
            else:
              raise Exception("apply job did not finish in time")
          '';
        };
      };
    };
}
