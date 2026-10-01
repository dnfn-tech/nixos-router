# nixos-router

NixOS 路由器通用模块。

```nix
inputs.nixos-router.url = "github:dnfn-tech/nixos-router";
```

在主机配置里导入 `inputs.nixos-router.nixosModules.default`。

## WebUI（家用路由器式管理）

- 在局域网浏览器中编辑配置并“保存并应用”，无需把远程 `nixos-rebuild` 作为日常操作路径
- 配置单一真源：`/var/lib/nixos-router/config.json`；运行态（会话/审计/作业等）在 `state.db`（SQLite）
- NixOS 模块会安装 Web UI + API + apply agent（仅 LAN 监听），并在激活时从 `config.json` 幂等再应用
- 远程/git 更新模块与程序时，不清空 `/var/lib/nixos-router`
- 页面由功能模块/插件贡献（导航与页面注册），核心页面固定存在；可选模块可在运行期启用/禁用

## Docs

- 需求与信息架构：`docs/requirements.md`
- 后端服务架构：`docs/backend-architecture.md`
- 存储选型（混合：JSON + SQLite）：`docs/storage-choice.md`（已在开放 PR 提交）
