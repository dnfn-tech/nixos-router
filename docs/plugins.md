# 插件与功能模块总览

本项目后端采用“稳定核心 + 模块/插件”的扩展模型：核心仅提供通用能力（配置注册、作业队列、审计、会话/鉴权、机密、在线设备），各网络功能以模块实现（v1 作为内置一方模块编译进二进制），并为将来第三方插件预留扩展点与协议。

- 合同：Manifest（id/name/version/routes/ui/configSchema/applyHooks/capabilities）
- 生命周期：discover → register → enable/disable → configure → apply（pre/post validate、generate、reload）
- 隔离：统一中间件链（LAN-only、鉴权、CSRF）；能力按需授权；生成配置由核心统一 reload/restart
- 打包：v1 内置模块随核心编译；第三方将来可作为子进程插件（JSON-RPC over stdio）与静态资源目录
- 配置：插件配置位于 `config.json` 的 `plugins.<id>`；schema 片段合并入总 schema
- 热/冷：v1 建议启用/禁用后重启 API/Apply（冷加载）；热加载后续评估

详细设计、Go 接口草图与示例模块映射请见 `docs/backend-architecture.md` 的“插件 / 功能模块”章节。

