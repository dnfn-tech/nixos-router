# nixos-router

NixOS 路由器通用模块。

```nix
inputs.nixos-router.url = "github:dnfn-tech/nixos-router";
```

在主机配置里导入 `inputs.nixos-router.nixosModules.default`。

## Docs

存储选型的设计与结论见 [docs/storage-choice.md](docs/storage-choice.md)。

## Development (Backend + WebUI)

基于 Go 的后端原型（里程碑 1：只读 API + 同源托管 WebUI）位于 `backend/`，详见 `backend/README.md`。二进制自带 UI（`web/` 已内嵌），启用 NixOS 模块后浏览器可直接访问同源页面与 API。
