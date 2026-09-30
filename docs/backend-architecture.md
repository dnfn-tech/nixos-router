# 后端服务架构设计（docs-first）

本设计围绕“局域网内可写、即时应用”的家用路由 Web 管理界面：前端为静态资源，后端提供认证、配置读写、作业编排与系统应用。单一真源为 `/var/lib/nixos-router/config.json`，`/var/lib/nixos-router/state.db`（SQLite）承载会话/审计/作业/可选修订。

## 组件与进程
- Static UI（前端静态文件）
  - 由后端 API 以 `/` 挂载（或 NixOS `nginx`/`caddy` 反代静态），局域网可访问
- API 服务（LAN-only）
  - REST 接口：读取/更新配置、触发 apply、查询作业状态与审计、下载/恢复备份
  - 会话认证、授权与 CSRF 防护；生成作业记录
- Apply Worker（应用代理）
  - 串行消费作业：validate → 原子写 → 生成运行时配置 → reload/restart → 结果入库
  - 失败时不破坏当前生效配置；提供回滚与诊断信息

系统以 2 个 systemd 单元运行（可同一二进制不同子命令）：
- `nixos-router-api.service`
- `nixos-router-apply.service`

## 推荐技术栈与理由（首选）
- 首选：Go
  - 优点：单文件静态编译、内存占用低、标准库健壮；SQLite 生态成熟；Nix 打包友好
  - Web：`chi`（或 `echo`/`gin`），中间件完善；模板与静态文件内嵌简便
  - SQLite：`modernc.org/sqlite`（无 cgo）或 `mattn/go-sqlite3`（cgo）；启用 WAL 模式
  - 任务编排：基于 `context` 与 systemd 的 `ExecStartPost`/`notify`；或用本地队列（bolt/内存）+ SQLite 记录
- 备选：Rust（`axum` + `sqlx`）/ Python（`FastAPI` + `aiosqlite`）
  - Rust：零成本抽象、强类型；学习曲线更陡
  - Python：开发快；常驻内存与依赖体积偏大

## REST API 轮廓（v1）
- 认证
  - `POST /api/v1/session` 登录（表单：用户名/密码）→ Set-Cookie；写审计
  - `GET /api/v1/session` 查看会话；`DELETE /api/v1/session` 登出
- 配置（以 config.json 为核心资源）
  - `GET /api/v1/config` → 当前完整配置（可选支持 `?redact=true` 脱敏）
  - `PUT /api/v1/config` → 提交完整配置（或 `PATCH` 局部段）；返回作业 id
  - `POST /api/v1/apply` → 基于已保存配置触发一次应用（可用于快速重载服务）
- 资源分段（便于前端分步保存/校验，可选暴露）
  - `GET/PUT /api/v1/wan|lan|wifi|dns|firewall|nat|upnp|qos|parental|ddns|ipv6|ssh`
- 运行态与资产
  - `GET /api/v1/clients` 在线设备（来源 dnsmasq/hostapd/nftables 统计）
  - `GET /api/v1/jobs` / `GET /api/v1/jobs/{id}` 作业状态/日志
  - `GET /api/v1/audit` 审计列表（分页/筛选）
  - 备份：`GET /api/v1/backup/config`（仅 JSON）、`GET /api/v1/backup/full`（含 DB）
  - 恢复：`POST /api/v1/backup/restore`（上传包 → 校验 → 作业化）
  - 健康/指标：`GET /api/v1/healthz`、`GET /metrics`（Prometheus）

说明：推荐以“完整 JSON 提交 + 服务器端校验 + 原子写 + 作业编排”为主路径；细分资源用于表单分块与增量校验。

## `config.json` 结构（纲要）
```json
{
  "version": 1,
  "wan": {
    "mode": "dhcp | static | pppoe",
    "interface": "enp1s0",
    "static": { "address": "192.0.2.2/24", "gateway": "192.0.2.1", "dns": ["1.1.1.1"] },
    "pppoe": { "username": "", "password": "", "serviceName": "", "acName": "", "usePeerDns": true }
  },
  "lan": {
    "bridge": "br-lan",
    "ports": ["enp2s0", "enp3s0"],
    "ipv4": { "address": "192.168.88.1/24" },
    "dhcp": { "enable": true, "rangeStart": "192.168.88.100", "rangeEnd": "192.168.88.200", "leaseTime": "12h", "domain": "lan" },
    "staticLeases": [ { "enable": true, "hostname": "nas", "mac": "AA:BB:CC:DD:EE:20", "ip": "192.168.88.20", "note": "群晖" } ]
  },
  "wifi": {
    "bands": [
      { "band": "2g", "enable": true, "ssid": "nixos-router", "hidden": false, "channel": "auto", "width": "20", "encryption": "wpa2+wpa3", "password": "" },
      { "band": "5g", "enable": true, "ssid": "nixos-router-5G", "hidden": false, "channel": "auto", "width": "80", "encryption": "wpa2+wpa3", "password": "", "samePasswordAs2g": true }
    ],
    "guest": { "enable": false, "ssid2g": "", "ssid5g": "", "password": "", "isolation": true, "bandwidth": { "downMbps": 30, "upMbps": 10 }, "schedule": { "mode": "always|range", "from": "08:00", "to": "22:00" } }
  },
  "dns": { "upstreams": ["223.5.5.5", "119.29.29.29"], "domain": "lan" },
  "firewall": { "enable": true, "wanInput": "drop", "lanAllowTcp": [22, 53, 8080], "lanAllowUdp": [53, 67] },
  "nat": { "portForwards": [ { "enable": true, "name": "NAS Web", "proto": "tcp", "ext": "8443", "host": "192.168.88.20", "int": "443" } ] },
  "upnp": { "enable": false, "lanOnly": true },
  "qos": { "enable": true, "downMbps": 500, "upMbps": 50, "rules": [ { "enable": true, "target": "192.168.88.118", "prio": "normal", "down": "20", "up": "5" } ] },
  "parental": { "rules": [ /* 周历/对象规则 */ ] },
  "ddns": { "provider": "cloudflare|duckdns|aliyun|custom", "options": {/* 各自字段 */}, "interval": "10m" },
  "ipv6": { "enable": false, "wan": { "mode": "dhcpv6|slaac|pppoe" }, "lan": { "pd": "auto", "ra": true, "assign": "slaac", "dnsAdvertise": true }, "fw": { "wanInput": "drop", "icmpv6Essential": true } },
  "ssh": { "enable": true, "allowPassword": false, "allowRoot": false, "wheelPasswordlessSudo": true },
  "system": { "uiPort": 8080 }
}
```

注：敏感字段（如 PPPoE/WiFi/DDNS 凭据）位于 `config.json` 内，但该文件仅存本机 `/var/lib/nixos-router`；提供“脱敏导出”接口。

## Apply 管道（关键路径）
1) Validate
   - JSON Schema 校验 + 语义校验（IP 段/端口/冲突检测/范围检查）
2) Atomic write
   - 写入 `config.json.tmp` → `fsync` → `rename()` 覆盖 `config.json`；同时记录作业条目
   - 维护 `last-good.json`（或版本指针）以便快速回退
3) Generate runtime configs
   - nftables（含 IPv6）/ dnsmasq / hostapd / ppp / sysctl / miniupnpd / cake 等
   - 写入到 `/run/nixos-router/*.conf` 或 `/etc/...`（按 NixOS 约定），必要时生成 unit drop-in
4) Reload/Restart
   - 优先 `reload`；不支持热加载时 `restart`；保持串行顺序与依赖（例如：先网络后服务）
5) Audit & Result
   - 将 apply 结果（成功/失败、耗时、摘要）写入 `state.db`；必要时保留生成的配置快照用于诊断
6) Failure & Rollback
   - 失败时不替换当前生效的运行时配置；支持回退到 `last-good.json` 再次 apply；错误信息通过作业接口暴露

## 密钥/机密存放
- 默认存放在 `config.json` 的对应字段；仅位于设备 `/var/lib/nixos-router/`，不进入 flake/git。
- 提供两类导出：
  - 标准导出（含所有字段，用于完整备份，需二次确认）
  - 脱敏导出（遮蔽密钥字段，用于分享/排障）
- 可选旁路：支持 `secrets.json` 覆盖某些字段（优先级高于 `config.json`），但默认不要求。

## 会话认证模型
- 单管理员账户（后续可扩展）：用户名/密码登录
- Cookie-Session（HttpOnly、SameSite=Lax/Strict），CSRF 令牌（双提交/同源）
- 密码散列：argon2id（或 bcrypt），登录失败限速
- 仅在 LAN 监听；防火墙自动放行 8080（可改）；WAN/IPv6 默认拒绝
- 审计范围：登录/登出、配置读取/修改、apply、备份/恢复、下载

## NixOS 模块集成
- 入口开关（建议命名）
  - `services.nixosRouter.webui.enable = true;`
  - `services.nixosRouter.webui.port = 8080;`
  - `services.nixosRouter.stateDir = "/var/lib/nixos-router";`
- 模块职责
  - 安装 API/Apply 二进制与前端静态资源；写入 systemd 单元（LAN-only 监听）
  - 定义目录与权限（`StateDirectory=nixos-router`）；确保 `/var/lib/nixos-router` 持久
  - 开机（activation）时从 `config.json` 触发一次幂等 apply（若存在）
  - 远程 `nixos-rebuild` 更新模块/程序时，不触碰 `stateDir`

## 可观测性与日志
- 结构化日志（JSON 行）：组件、作业 id、阶段、耗时、错误码
- journald 集成，必要时输出到文件（`/var/log/nixos-router/`）
- 指标：`/metrics`（Prometheus）——请求量/延迟、apply 用时、失败率、会话数
- 诊断包：导出最近 N 次作业的生成配置与日志

## 测试策略（概述）
- 单元测试
  - Schema 校验、字段语义校验（IP/端口/范围/冲突）
  - 生成器：将 `config.json` 映射为 nftables/dnsmasq/hostapd 等片段（Golden tests）
- 集成测试
  - NixOS VM 测试（QEMU）：API 可用、保存/应用成功、服务联动（dnsmasq/nftables/hostapd）
  - 断网/失败注入：生成失败、服务重启失败、回滚路径
- 端到端（E2E）
  - UI 自动化（只读 → 可写 → 状态机 → 作业完成）
  - 备份/恢复/重启链路

## 一致性说明
- 本架构与 `docs/requirements.md` 的 IA/功能与 UX 状态机一致
- 存储方案与 PR 中的 `docs/storage-choice.md`（混合：JSON + SQLite）一致；本文不再重复 rationale

