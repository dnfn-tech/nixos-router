## WebUI（静态壳）

本目录包含前端外壳（里程碑 M1+M2，轻量、无构建步骤）。目标：中文界面、插件驱动导航、局域网家用路由的简洁观感；M2 增加登录与会话。

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

当你用同一主机（同域名/端口）托管 WebUI 与后端时，无需任何配置，前端会以“同源”访问 `/api/v1/*`。若本地开发时将静态文件与后端拆分到不同端口（例如 `python -m http.server` 在 8000、后端在 8080），请用以下任一方式覆盖：

- 在地址栏追加参数：`?api=http://127.0.0.1:8080`
- 或在页面加载前设置：`window.NIXOS_ROUTER_API = "http://127.0.0.1:8080"`

所有 API 请求均携带 `credentials: 'include'`，以便使用后端设置的 Cookie。未登录访问受保护接口将返回 401，前端会自动跳转到登录页；登录成功后回到主界面；点击“退出”将清理会话并返回登录页。

### 功能概览

- 左侧边栏导航 + 右侧主内容区
- 导航优先从后端获取；若失败则使用内置核心导航（总览、外网、内网、无线、客户端、DNS、防火墙、SSH、系统、插件）
- 概览页会尝试读取 `/api/v1/status` 与 `/api/v1/config`，以信息卡片与 JSON 只读视图呈现
- 登录页：用户名/密码，错误提示，提交调用 `POST /api/v1/session`
- 401 统一处理：自动返回登录页
- 侧栏“退出”：调用 `DELETE /api/v1/session`
- 其他核心路由为占位页（只读里程碑，不包含写入与应用）

### 约束

- 不引入重型前端框架（纯 HTML/CSS/JS）
- 暗色/亮色跟随系统偏好，样式保持简洁

