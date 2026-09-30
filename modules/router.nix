{
  config,
  lib,
  ...
}:
let
  cfg = config.router;
in
{
  options.router = {
    wan.interface = lib.mkOption {
      type = lib.types.str;
      description = "连接上游网络的网卡名。";
    };

    lan.interface = lib.mkOption {
      type = lib.types.str;
      description = "面向内网的网卡名。";
    };

    lan.address = lib.mkOption {
      type = lib.types.str;
      default = "192.168.88.1";
      description = "内网网关地址。";
    };

    lan.prefixLength = lib.mkOption {
      type = lib.types.ints.between 8 30;
      default = 24;
      description = "内网前缀长度。";
    };

    dhcp.start = lib.mkOption {
      type = lib.types.str;
      description = "DHCP 地址池起始地址。";
    };

    dhcp.end = lib.mkOption {
      type = lib.types.str;
      description = "DHCP 地址池结束地址。";
    };

    dhcp.leaseTime = lib.mkOption {
      type = lib.types.str;
      default = "12h";
      description = "DHCP 租约时长。";
    };

    dns.upstreams = lib.mkOption {
      type = lib.types.listOf lib.types.str;
      default = [
        "223.5.5.5"
        "119.29.29.29"
      ];
      description = "路由器自身以及 dnsmasq 转发使用的上游 DNS。";
    };

    domain = lib.mkOption {
      type = lib.types.str;
      default = "lan";
      description = "内网域名。";
    };
  };

  config = {
    time.timeZone = "Asia/Shanghai";
    i18n.defaultLocale = "zh_CN.UTF-8";

    nix.settings.experimental-features = [
      "nix-command"
      "flakes"
    ];

    networking.useDHCP = false;
    networking.dhcpcd.enable = false;
    networking.networkmanager.enable = false;
    networking.nameservers = cfg.dns.upstreams;

    networking.interfaces.${cfg.wan.interface}.useDHCP = true;
    networking.interfaces.${cfg.lan.interface}.ipv4.addresses = [
      {
        address = cfg.lan.address;
        prefixLength = cfg.lan.prefixLength;
      }
    ];

    networking.nat = {
      enable = true;
      externalInterface = cfg.wan.interface;
      internalInterfaces = [ cfg.lan.interface ];
    };

    networking.firewall = {
      enable = true;
      interfaces.${cfg.lan.interface} = {
        allowedTCPPorts = [
          22
          53
        ];
        allowedUDPPorts = [
          53
          67
        ];
      };
    };

    services.dnsmasq = {
      enable = true;
      # 只在内网接口上提供解析，避免路由器把自己的 resolv.conf 指回仅监听 LAN 的 dnsmasq。
      resolveLocalQueries = false;
      settings = {
        interface = cfg.lan.interface;
        bind-interfaces = true;
        domain-needed = true;
        bogus-priv = true;
        domain = cfg.domain;
        expand-hosts = true;
        dhcp-authoritative = true;
        dhcp-range = [ "${cfg.dhcp.start},${cfg.dhcp.end},${cfg.dhcp.leaseTime}" ];
        server = cfg.dns.upstreams;
      };
    };

    services.openssh = {
      enable = true;
      settings = {
        PermitRootLogin = "no";
        PasswordAuthentication = false;
        KbdInteractiveAuthentication = false;
      };
    };

    security.sudo.wheelNeedsPassword = false;
  };
}
