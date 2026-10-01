# NixOS Router Backend (Go)

里程碑 1：只读 API（schema + 健康检查 + 配置读取）

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

## API（前缀 /api/v1）

- `GET /api/v1/health`：健康检查
- `GET /api/v1/config`：返回配置（默认脱敏），传 `?redact=0` 返回原值（仅 dev 建议）
- `GET /api/v1/status`：状态概览（桩值）
- `GET /api/v1/ui/nav`：UI 导航（内置页面 + 已启用插件）
- `GET /api/v1/plugins`：插件列表（来自配置）
- `GET /api/v1/capabilities/wifi`：WiFi 能力（桩值/来自 env）
- `POST /api/v1/auth/login`：501（未实现）

### curl 示例

```bash
curl -s http://localhost:8080/api/v1/health | jq .
curl -s http://localhost:8080/api/v1/config | jq .
curl -s http://localhost:8080/api/v1/config?redact=0 | jq .
curl -s http://localhost:8080/api/v1/status | jq .
curl -s http://localhost:8080/api/v1/ui/nav | jq .
curl -s http://localhost:8080/api/v1/plugins | jq .
curl -s http://localhost:8080/api/v1/capabilities/wifi | jq .
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

