# nixos-router

NixOS 路由器通用模块。

```nix
inputs.nixos-router.url = "github:dnfn-tech/nixos-router";
```

在主机配置里导入 `inputs.nixos-router.nixosModules.default`。

## Docs

存储选型的设计与结论见 [docs/storage-choice.md](docs/storage-choice.md)。

## Development

前端 WebUI 原型见 `web/` 目录（在 `web/` 运行 `python -m http.server`；默认同源访问 API，拆分端口时用 `?api=` 或 `window.NIXOS_ROUTER_API` 覆盖）。
