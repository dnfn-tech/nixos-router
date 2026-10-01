# 目标主机（gw）迁移说明

首个目标部署主机 `gw` 的产品化方向如下：

- 单一 LAN：将无线 AP 接口桥接入 `br-lan`，与有线口处于同一网段；同一 DHCP/DNS 由 dnsmasq 提供
- WiFi 能力驱动：依据硬件 `nl80211` 能力裁剪并发形态（`AP≤1` 则单 AP；若支持 BSS 则以附加 SSID 形式提供访客网络；若支持 DBDC/多 AP 则允许并发 2.4G+5G）
- DHCP/DNS 后端：dnsmasq 为主。现有运行在 Kea 的环境如需迁移，由运维操作者规划切换窗口；v1 不支持双栈并行 DHCP

上述方向已体现在 `docs/requirements.md` 与 `docs/backend-architecture.md`，并作为 v1 默认模型实施。后续如需 VLAN/Multi-LAN，将以模块/插件的形式在不破坏默认单一 LAN 的前提下演进。

