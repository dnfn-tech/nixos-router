## WebUI（静态壳）

本目录包含前端外壳（里程碑 M1–M7，轻量、无构建步骤）。目标：中文界面、插件驱动导航、局域网家用路由的简洁观感；已覆盖只读壳、登录与会话、核心页可编辑与保存/生成、插件管理与高级页、系统级操作（备份/恢复/审计/重启）。

### 运行

- 方式一：直接用浏览器打开 `index.html`
- 方式二：在本目录启动一个简单的 HTTP 服务（推荐）：

```bash
python -m http.server
```

然后访问输出的地址，例如：`http://127.0.0.1:8000/`

### API 基址

WebUI 会从以下位置确定后端 API 基址（优先级从高到低）：

1. URL 查询参数：`?api=http://host:port`
2. 全局变量：`window.NIXOS_ROUTER_API = "http://host:port"`
3. 默认值：同源 `window.location.origin`（会去掉末尾 `/`）

接口（由并行的后端 PR 提供，未就绪时会回退到内置导航并显示“后端未连接”横幅）：

- `GET /api/v1/ui/nav`：导航（优先尝试）
- `GET /api/v1/plugins`：插件列表（作为次要来源）
- `GET /api/v1/status`：系统状态（概览页展示）
- `GET /api/v1/config`：当前配置（概览页展示）
- `GET /api/v1/health`：运行状况
- 会话（M2）：`GET /api/v1/session` / `POST /api/v1/session` / `DELETE /api/v1/session`
 - WiFi 能力：`GET /api/v1/capabilities/wifi`

当你用同一主机（同域名/端口）托管 WebUI 与后端时，无需任何配置，前端会以“同源”访问 `/api/v1/*`。若本地开发时将静态文件与后端拆分到不同端口（例如 `python -m http.server` 在 8000、后端在 8080），请用以下任一方式覆盖：

- 在地址栏追加参数：`?api=http://127.0.0.1:8080`
- 或在页面加载前设置：`window.NIXOS_ROUTER_API = "http://127.0.0.1:8080"`

所有 API 请求均携带 `credentials: 'include'`，以便使用后端设置的 Cookie。未登录访问受保护接口将返回 401，前端会自动跳转到登录页；登录成功后回到主界面；点击“退出”将清理会话并返回登录页。

### 功能概览（只读 + 可编辑）

- 左侧边栏导航 + 右侧主内容区
- 导航优先从后端获取；若失败则使用内置核心导航（总览、外网、内网、无线、客户端、DNS、防火墙、SSH、系统、插件）
- 概览：读取 `/api/v1/status`、`/api/v1/config`、`/api/v1/health`，展示主机名、运行时长、WAN/LAN 摘要、接口列表（按 role）、WiFi 能力（`/api/v1/capabilities/wifi`）；缺字段时优雅降级并清晰提示
- 外网（#/wan）：编辑 `config.wan`（mode dhcp/static/pppoe、接口名、静态地址/GW/DNS、PPPoE 账号/密码），显示状态摘要。PPPoE/WiFi/DDNS 等密钥型字段未更改时保留为 `****` 或留空，后端将保留原值
- 内网（#/lan）：编辑 `config.lan`（桥、IPv4 CIDR、DHCP 范围/租约、ports[]、staticLeases[]），显示状态摘要
- 无线（#/wifi）：编辑 `config.wifi`（启用/桥接、aps[ssid/band/channel/psk/guest/isolate]），capabilities.maxAP 限制 AP 数；显示状态摘要
- DNS（#/dns）：编辑 `config.dns`（enableDnsmasq、upstreams[]、domain）
- 防火墙（#/firewall）：编辑 `config.firewall`（enable、natEnabled、portForwards[]、upnp），显示说明
- SSH（#/ssh）：编辑 `config.ssh`（enable、port、passwordAuth、authorizedKeys[]）并显示 `status.ssh`
- 系统（#/system）：编辑 `config.system`（hostname、timezone），展示 `health/status` 摘要
- 系统页附带“生成配置（不应用运行态）”：点击调用 `POST /api/v1/apply`，展示返回的 `jobId/mode/appliedRuntime`，并查询 `GET /api/v1/jobs/{id}` 显示状态/错误。说明：当前仅生成到服务端 stateDir，不会重载网络服务
- 系统页（M7 扩展）：修改密码（尝试 `POST /api/v1/session/password` 或 `PUT /api/v1/account/password`，若未提供则提示）、备份下载（`GET /api/v1/backup` 或 `/api/v1/system/backup`）、恢复上传（`POST /api/v1/backup/restore` 或 `/api/v1/system/restore`）、审计日志（`GET /api/v1/audit?limit=50`）、重启（`POST /api/v1/system/reboot`）；接口缺失时显示“接口尚未提供”
- 客户端（#/clients）：展示 `GET /api/v1/clients` 返回；若提供 `PATCH /api/v1/clients/{mac}`，支持行内重命名与阻止切换；404 时自动降级为只读；否则明确标注“接口尚未提供”
- 插件管理（#/plugins）：从 `GET /api/v1/plugins` 列表加载，切换启用状态优先调用 `PUT /api/v1/plugins/{id}`（若 404 再回退到 `PUT /api/v1/config`）；保存后刷新导航。插件详情页（如 `#/tailscale`/`#/zerotier` 等）在插件启用且后端导航提供时可直接进入；若未提供配置则以“未提供”提示并禁用保存
- 登录页：用户名/密码，错误提示，提交调用 `POST /api/v1/session`
- 401 统一处理：自动返回登录页
- 侧栏“退出”：调用 `DELETE /api/v1/session`
- 其他核心路由为占位页（只读里程碑，不包含写入与应用）

### 编辑与保存

- 所有编辑页面提供底部操作条（保存 / 保存并生成配置），显示“有未保存的更改”
- 离开页面或刷新时，如有未保存更改会提示确认
- 保存：向 `PUT /api/v1/config` 提交完整配置（将当前页面修改合并入最近一次载入的配置）；后端校验失败会在页面内提示
- 保存并生成：保存后调用 `POST /api/v1/apply`，仅在服务端 stateDir 生成，不会重载运行态
- 密钥/口令字段约定：若未改动，前端提交 `****` 或空串，后端按“保留原值”处理

### 约束

- 不引入重型前端框架（纯 HTML/CSS/JS）
- 暗色/亮色跟随系统偏好，样式保持简洁

