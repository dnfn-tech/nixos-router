# 插件与功能模块总览

本项目后端采用“稳定核心 + 模块/插件”的扩展模型：核心仅提供通用能力（配置注册、作业队列、审计、会话/鉴权、机密、在线设备），各网络功能以模块实现（v1 作为内置一方模块编译进二进制），并为将来第三方插件预留扩展点与协议。

- 合同：Manifest（id/name/version/routes/ui/configSchema/applyHooks/capabilities）
- 生命周期：discover → register → enable/disable → configure → apply（pre/post validate、generate、reload）
- 隔离：统一中间件链（LAN-only、鉴权、CSRF）；能力按需授权；生成配置由核心统一 reload/restart
- 打包：v1 内置模块随核心编译；第三方将来可作为子进程插件（JSON-RPC over stdio）与静态资源目录
- 配置：插件配置位于 `config.json` 的 `plugins.<id>`；schema 片段合并入总 schema
- 热/冷：v1 建议启用/禁用后重启 API/Apply（冷加载）；热加载后续评估

详细设计、Go 接口草图与示例模块映射请见 `docs/backend-architecture.md` 的“插件 / 功能模块”章节。

## UI / 页面控制

为避免前端菜单硬编码、支持按模块裁剪功能，WebUI 导航与页面由插件/功能模块贡献并由后端汇总。

### Manifest 中的 UI 声明
- `ui.nav[]`（侧栏入口，按分组/顺序渲染）
  - 结构：`{ id, label, group, icon?, order?, page }`
    - `group`：如「基础」「网络服务」「系统」
    - `page`：点击后跳转的页面 id（由 `ui.pages` 注册）
- `ui.pages[]`（页面注册）
  - 结构：`{ id, route, title, asset }`
    - `route`：如 `/wifi`、`/ddns`，前端路由映射
    - `asset`：组件或静态资源路径（由前端构建/静态目录提供）
- 预留：`ui.sections[]`（向现有页面注入分区/面板），v1 可延后实现，仅作为文档占位

### 启用/禁用与可见性
- 启用的插件：其 `ui.nav` 与 `ui.pages` 对应的导航与页面可见；相关 API 路由可用
- 禁用的插件：隐藏对应导航与页面；其 API 路由返回 404/disabled
- 内置“核心页面”不可在运行期禁用（或仅能通过编译期构建标记禁用）：
  - 概览（overview）、WAN、LAN、WiFi、设备（clients）、DNS、防火墙（firewall）、SSH、系统（system）、插件管理（plugins-mgmt）
- 可选内置（QoS、家长控制、DDNS、IPv6 …）：与插件 1:1 对应，可通过 `config.json` 的 `plugins.<id>.enabled` 进行运行期开启/关闭

### 前端加载方式
- 前端通过 `GET /api/v1/ui/nav` 或 `GET /api/v1/plugins`（返回 manifest + enabled）拉取导航/页面定义
- Shell/导航不硬编码完整功能清单，而是根据返回结果动态绘制
- v1 冷加载：启用/禁用后可能需要 API 进程重启；在 apply 作业完成后刷新前端即可生效

