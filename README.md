# nixos-router

为 NixOS 打造的“家用路由器式”管理模块与参考实现（docs-first）。目标是在局域网（LAN）内通过 Web UI 配置网络与系统功能，“保存并应用”即可生效；配置采用单一真源，运行态分离，具备可备份/可回滚的工程实践。

```nix
inputs.nixos-router.url = "github:dnfn-tech/nixos-router";
```

在主机配置里导入 `inputs.nixos-router.nixosModules.default`。

## Docs

- 需求与信息架构：`docs/requirements.md`
- 后端服务架构：`docs/backend-architecture.md`
- 插件与功能模块：`docs/plugins.md`
- 存储选型（JSON + SQLite）：`docs/storage-choice.md`
- 实现现状：`docs/implementation-status.md`

## Development (Backend + WebUI)

基于 Go 的后端与内嵌 WebUI 位于 `backend/`，详见 `backend/README.md`。启用 NixOS 模块后浏览器可直接访问同源页面与 API（默认 `:8080`，仅 LAN 暴露为设计目标）。
