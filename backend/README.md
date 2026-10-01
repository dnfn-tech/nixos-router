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
- `PUT /api/v1/plugins/{id}`：启用/禁用（以及配置项变更，`"****"`/空值会被视为“保持原有机密值不变”）
- `GET /api/v1/clients`：客户端列表（优先租约来源，否则基于静态租约，`source: "stub"`）
  - 解析 dnsmasq 租约（默认 `/var/lib/misc/dnsmasq.leases`，可用 `NIXOS_ROUTER_DNSMASQ_LEASES` 指定；或 `stateDir/generated/dnsmasq.leases` 等），与静态租约合并（hostname 补全），`source: "leases"|"mixed"|"stub"`
- `GET /api/v1/capabilities/wifi`：WiFi 能力（桩值/来自 env）
- `POST /api/v1/session`：登录，设置 HttpOnly Cookie（SameSite=Lax）
- `GET /api/v1/session`：查看会话（需已登录）
- `DELETE /api/v1/session`：登出（清除会话 cookie）
- 兼容路径 `POST /api/v1/auth/login` 已转发到 `/api/v1/session`
- `PUT /api/v1/config`：保存完整配置（Schema/语义校验通过后原子写入）；仅保存，不 apply
- `POST /api/v1/apply`：生成运行时片段到 `stateDir/generated/`（dnsmasq/nftables/hostapd 占位），记录作业；默认不 reload 系统单元
- `GET /api/v1/jobs/{id}`：查询作业状态（`mode: generate-only`，`appliedRuntime: false`）
- `GET /api/v1/jobs?limit=20`：按创建时间倒序返回最近作业
- `GET /api/v1/audit?limit=50`：审计日志（时间倒序）
- `POST /api/v1/session/password`（别名 `PUT /api/v1/account/password`）：修改密码，body: `{"currentPassword","newPassword"}`；成功 200，失败返回 400/401；审计记录
- `GET /api/v1/backup`（别名 `GET /api/v1/system/backup`）：下载配置备份（application/json，文件名 `nixos-router-backup-YYYYMMDD.json`，含完整 config 含敏感字段）
- `POST /api/v1/backup/restore`：恢复备份（body 为 `{config:{...}}` 或原始 config 对象，或 multipart 文件上传）；验证通过后原子写入，仅保存不 apply；审计记录
- `POST /api/v1/system/reboot`：重启（默认禁用；`--dev` 或未设置 `NIXOS_ROUTER_ALLOW_REBOOT=1` 返回 501），仅在显式允许时返回 `{ok:true, scheduled:true}` 并后台执行；审计记录

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

# 5) 更新配置（仅保存，不 apply）
# 形状一：与 GET 对称（{ config: {...} }）
# 5) 更新配置（仅保存，不 apply）
# 形状一：与 GET 对称（{ config: {...} }）
curl -s -b cookie.txt -H 'Content-Type: application/json' \
  -d '{"config":{"system":{"hostname":"new-name"}}, "wan":{"mode":"dhcp"}, "lan":{...}, "dns":{...}, "wifi":{...}, "firewall":{...}, "ssh":{...}, "plugins":{}}' \
  -X PUT http://localhost:8080/api/v1/config | jq .

# 形状二：直接传完整配置对象
curl -s -b cookie.txt -H 'Content-Type: application/json' \
  -d '{"system":{"hostname":"new-name"}, "wan":{"mode":"dhcp"}, "lan":{...}, "dns":{...}, "wifi":{...}, "firewall":{...}, "ssh":{...}, "plugins":{}}' \
  -X PUT http://localhost:8080/api/v1/config | jq .

# 秘密字段合并规则（避免误清空）
# - 当 UI 以 \"****\" 或空字符串提交时，后端将保留已存储的密钥/口令（PPPoE、WiFi PSK、DDNS Token、插件密钥等）
# - 若要显式清除，请传入明确的空值协议（后续里程碑可提供专门 API）

# 6) 生成（仅生成，不 reload）
curl -s -b cookie.txt -X POST http://localhost:8080/api/v1/apply | jq .
# 查询作业
JOB=$(curl -s -b cookie.txt -X POST http://localhost:8080/api/v1/apply | jq -r .jobId)
curl -s -b cookie.txt http://localhost:8080/api/v1/jobs/$JOB | jq .
curl -s -b cookie.txt 'http://localhost:8080/api/v1/jobs?limit=5' | jq .
ls -la ./_state/generated/
curl -s -b cookie.txt 'http://localhost:8080/api/v1/audit?limit=20' | jq .

# 8) 修改密码
curl -s -b cookie.txt -H 'Content-Type: application/json' \
  -d '{"currentPassword":"oldpass","newPassword":"newpass123"}' \
  http://localhost:8080/api/v1/session/password | jq .

# 9) 备份/恢复
curl -s -b cookie.txt -D headers.txt http://localhost:8080/api/v1/backup -o backup.json
jq '.config.system.hostname="restored-host"' backup.json > backup2.json
curl -s -b cookie.txt -H 'Content-Type: application/json' \
  -d @backup2.json http://localhost:8080/api/v1/backup/restore | jq .

# 10) 重启（默认禁用，返回 501）
curl -i -b cookie.txt -X POST http://localhost:8080/api/v1/system/reboot

# 7) 登出
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
  - 可选：`services.nixos-router.backend.applyReload = true;` 启用占位 reload 钩子（默认关闭）
  - 可选：`services.nixos-router.backend.allowReboot = true;` 显式允许后端执行重启（默认关闭）

### 计算 vendorHash（M10）

- 本仓 flake 的 `buildGoModule` 需要固定 `vendorHash` 才能稳定缓存
- 在可用 nix 的主机执行：

```bash
./scripts/compute-vendor-hash.sh           # 打印建议哈希
./scripts/compute-vendor-hash.sh --apply   # 直接更新 flake.nix
nix build .#routerd                        # 成功后表示 vendorHash 正确
```

若在容器/CI 中缺失 nix-daemon，脚本会失败。请在本地开发机上执行并提交更新的 `flake.nix`。

## 实现现状

简述见 `docs/implementation-status.md`（当前默认 generate-only；当 `applyReload=true` 时提供保守的插件编排骨架：mihomo/tailscale/zerotier 按启用状态做 `systemctl try-reload-or-restart/try-stop`，缺失单元仅记录 notes；VLAN 仅注记）。

## 账户与会话

- 首次启动若 DB 中没有任何用户：
  - 若设置了 `NIXOS_ROUTER_ADMIN_PASSWORD`，将以该密码创建 `admin` 账户；
  - 否则在 `--dev` 或 `--seed-default-config` 下，会创建默认弱口令 `adminadmin`（仅供开发测试，生产请务必设置环境变量并更改密码）。
- 会话使用 HttpOnly Cookie（SameSite=Lax），同源前端可直接携带 Cookie 访问受保护 API。
- 登录失败有轻量限速（按客户端地址滑动窗口计数）。

## 配置保存（M3）

- `PUT /api/v1/config` 仅将合法配置保存到 `config.json`（原子写），并写入审计；不会触发 real apply（nftables/dnsmasq/hostapd 等）。GET 默认仍对敏感信息脱敏。

## Apply 骨架（M4 前半）

- `POST /api/v1/apply` 创建作业并串行执行：validate → 生成到 `stateDir/generated/`（dnsmasq.conf.fragment、nftables.nft.fragment、hostapd.conf.fragment）→ success/failed；仅生成、不 reload。
- `GET /api/v1/jobs/{id}` 可查询状态；`GET /api/v1/jobs` 列表暂未实现。

## 插件管理（M6）
- 内置可选插件：adblock、traffic、mihomo、vlan、tailscale、zerotier（以及 qos/parental/ddns/ipv6 若以插件化建模）
- `GET /api/v1/plugins` 返回插件清单（含启用状态与导航元数据）
- `PUT /api/v1/plugins/{id}` 可启用/禁用与更新配置；生效语义为“冷加载”，下一次生成/服务重启后生效；前端导航会依据启用状态显示/隐藏

## 生成器扩展（M8）
- `stateDir/generated/plugins/`：根据插件配置写入片段（仅生成，不下载/不执行）：
  - `adblock.conf.fragment`：`address=/domain/0.0.0.0` + 订阅 URL 注释（不下载）
  - `traffic.conf.fragment`：保留期等注记
  - `mihomo.yaml`：最小 YAML 骨架（profile/mode/tun/dns.port）
  - `vlan.network.fragment`：VLAN 备注（vid/name/bridge）
  - `tailscale.env.fragment`：controlPlane/loginServer 等，密钥以 `****` 占位
  - `zerotier.conf.fragment`：networks 加入列表与 controllerUrl 注记（token 以 `****` 占位）
  - 禁用插件会写入 `plugins/<id>.DISABLED` 或仅 notes
- QoS/Parental/IPv6：补充示例/注记片段（`qos.conf.fragment`、`parental.conf.fragment`、`ipv6.nft.fragment` 中含 ICMPv6 放通注释）

