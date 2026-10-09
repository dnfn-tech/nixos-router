{ pkgs, self }:
{
  # 上游“ISP”节点：vlan1，静态 10.0.0.1/24，dnsmasq 提供 DHCP+DNS，test.isp → 10.0.0.1
  upstream = { config, pkgs, ... }: {
    virtualisation.interfaces.eth1.vlan = 1;

    networking.useDHCP = false;
    networking.interfaces.eth1.ipv4.addresses = [{
      address = "10.0.0.1";
      prefixLength = 24;
    }];

    environment.systemPackages = with pkgs; [ curl iproute2 iputils ];

    services.dnsmasq = {
      enable = true;
      # 使用 settings（nixos-unstable 推荐）
      settings = {
        interface = "eth1";
        authoritative = true;
        # 仅限定接口，避免绑定特定地址造成早期竞态
        domain-needed = true;
        bogus-priv = true;
        # 上游随便指一个公共 DNS，避免其他查询卡住
        server = [ "1.1.1.1" "8.8.8.8" ];
        # DHCP for vlan1
        dhcp-range = "10.0.0.100,10.0.0.200,12h";
        # 测试域名
        address = [ "/test.isp/10.0.0.1" ];
      };
    };
  };

  # 路由器节点：导入模块，vlan1=WAN(DHCP)、vlan2=LAN(192.168.1.1/24)
  router = { config, pkgs, ... }: {
    imports = [ self.nixosModules.default ];
    virtualisation.interfaces.eth1.vlan = 1; # WAN
    virtualisation.interfaces.eth2.vlan = 2; # LAN

    networking.useDHCP = false;
    networking.interfaces.eth1.useDHCP = true; # WAN via upstream dnsmasq
    networking.interfaces.eth2.ipv4.addresses = [{
      address = "192.168.1.1";
      prefixLength = 24;
    }];

    # NAT：LAN -> WAN
    networking.nat = {
      enable = true;
      externalInterface = "eth1";
      internalInterfaces = [ "eth2" ];
      # 更稳妥地覆盖 RFC1918 网段由 dnsmasq 分配
    };

    # LAN DHCP + DNS 转发到上游（10.0.0.1），并回传路由/DNS 选项
    services.dnsmasq = {
      enable = true;
      settings = {
        interface = "eth2";
        authoritative = true;
        # 仅限定接口，避免绑定特定地址造成早期竞态
        domain-needed = true;
        bogus-priv = true;
        # DNS 上游指向 ISP
        server = [ "10.0.0.1" ];
        dhcp-range = "192.168.1.100,192.168.1.200,12h";
        # option 3: router, option 6: dns
        "dhcp-option" = [
          "3,192.168.1.1"
          "6,192.168.1.1"
        ];
      };
    };

    services.nixos-router.backend = {
      enable = true;
      # 测试用：dev 模式；会自动种子 admin/adminadmin，且免鉴权
      #（我们仍验证登录接口本身可用）
      dev = true;
      openFirewall = true;
      applyReload = false;        # 保守：只生成，不尝试 reload 以避免宿主缺少单元导致失败
      privilegedApply = false;
      consumeGenerated = false;   # 测试中手动配置 dnsmasq，避免接口名不一致
    };

    environment.systemPackages = with pkgs; [ curl iproute2 iputils ];
  };

  # 客户端：vlan2，DHCP 获取地址与 DNS
  client = { config, pkgs, ... }: {
    virtualisation.interfaces.eth1.vlan = 2;
    networking.useDHCP = false;
    networking.interfaces.eth1.useDHCP = true;

    environment.systemPackages = with pkgs; [ curl iproute2 iputils ];
  };
}

