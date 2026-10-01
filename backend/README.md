# NixOS Router Backend (Go)

里程碑 1+2：只读 API + 同源托管 WebUI + 会话鉴权

## 构建与运行

开发模式（免鉴权 + CORS \*）：

```bash
cd backend
go run ./cmd/routerd --dev --state-dir ./_state --seed-default-config
# 监听 :8080，首次会在 ./_state/ 生成 config.json 和 state.db
```

可用环境变量：

- `NIXOS_ROUTER_DEV=1`：开发模式
- `NIXOS_ROUTER_STATE_DIR=/var/lib/nixos-router`：状态目录（默认）
- `NIXOS_ROUTER_CONFIG=/var/lib/nixos-router/config.json`：配置文件路径（覆盖 state-dir）
- `NIXOS_ROUTER_ADDR=:8080`：监听地址
- `NIXOS_ROUTER_WIFI_MAX_APS=2`：WiFi 能力上限（示例）
- `NIXOS_ROUTER_CORS_ORIGIN`：dev 下可覆盖 CORS Origin（默认 `*`）
- `NIXOS_ROUTER_ADMIN_PASSWORD`：首次无用户时用于创建初始 `admin` 账户（未设置且 `--dev`/`--seed-default-config` 时会用默认弱口令，生产请务必设置）

## API（前缀 /api/v1）

- `GET /api/v1/health`：健康检查
- `GET /api/v1/config`：返回配置（默认脱敏），传 `?redact=0` 返回原值（仅 dev 建议）
- `GET /api/v1/status`：状态概览（桩值）
- `GET /api/v1/ui/nav`：UI 导航（内置页面 + 已启用插件）
- `GET /api/v1/plugins`：插件列表（来自配置）
- `GET /api/v1/capabilities/wifi`：WiFi 能力（桩值/来自 env）
- `POST /api/v1/session`：登录，设置 HttpOnly Cookie（SameSite=Lax）
- `GET /api/v1/session`：查看会话（需已登录）
- `DELETE /api/v1/session`：登出（清除会话 cookie）
- 兼容路径 `POST /api/v1/auth/login` 已转发到 `/api/v1/session`

## WebUI（静态资源）

- routerd 内嵌 `web/` 静态前端（`embed.FS`）；非 `/api/` 请求均由静态服务器处理
- SPA 路由回退：找不到静态文件时回退到 `index.html`
- 开发时可用 `--web-dir <path>` 覆盖本地目录（优先于内嵌）
- Content-Type 正确设置（html/js/css/svg/png…）

### curl 示例（含会话）

```bash
# 1) 健康检查
curl -s http://localhost:8080/api/v1/health | jq .

# 2) 未登录访问（非 --dev）将得到 401
curl -i http://localhost:8080/api/v1/config

# 3) 登录获取会话（保存 cookie）
echo '{"username":"admin","password":"<YOUR_PASSWORD>"}' | \
  curl -s -c cookie.txt -H 'Content-Type: application/json' \
  -d @- http://localhost:8080/api/v1/session | jq .

# 4) 携带 cookie 访问受保护接口
curl -s -b cookie.txt http://localhost:8080/api/v1/config | jq .
curl -s -b cookie.txt "http://localhost:8080/api/v1/config?redact=0" | jq .
curl -s -b cookie.txt http://localhost:8080/api/v1/status | jq .
curl -s -b cookie.txt http://localhost:8080/api/v1/ui/nav | jq .
curl -s -b cookie.txt http://localhost:8080/api/v1/plugins | jq .
curl -s -b cookie.txt http://localhost:8080/api/v1/capabilities/wifi | jq .

# 5) 登出
curl -i -X DELETE -b cookie.txt http://localhost:8080/api/v1/session
```

## 配置说明

- 配置文件：`/var/lib/nixos-router/config.json`（可通过 `--state-dir` 或 `NIXOS_ROUTER_CONFIG` 定位）
- JSON → Go 结构体（见 `internal/config`），加载时进行校验；首次缺失时返回内存默认值，并在 `--seed-default-config` 或 `--dev` 下写入默认文件
- 秘密字段（PPPoE 密码、WiFi PSK）在 API 默认脱敏

## 测试

```bash
cd backend
go test ./...
```

## Nix 构建/模块

- flake `packages.<system>.routerd`：包含嵌入的 WebUI
- NixOS 模块：`services.nixos-router.backend.enable = true;` 启用后访问 `http://<lan-ip>:8080/` 即可同源打开 UI 与 API

## 账户与会话

- 首次启动若 DB 中没有任何用户：
  - 若设置了 `NIXOS_ROUTER_ADMIN_PASSWORD`，将以该密码创建 `admin` 账户；
  - 否则在 `--dev` 或 `--seed-default-config` 下，会创建默认弱口令 `adminadmin`（仅供开发测试，生产请务必设置环境变量并更改密码）。
- 会话使用 HttpOnly Cookie（SameSite=Lax），同源前端可直接携带 Cookie 访问受保护 API。
- 登录失败有轻量限速（按客户端地址滑动窗口计数）。

