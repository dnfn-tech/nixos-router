{
  description = "NixOS 路由器通用模块";

  outputs = { self }: {
    nixosModules.default = ./modules;
  };
}
