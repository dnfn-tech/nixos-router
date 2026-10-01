(() => {
  "use strict";

  const q = (sel, el = document) => el.querySelector(sel);
  const qc = (sel, el = document) => el.querySelectorAll(sel);

  function getQueryParam(name) {
    const url = new URL(window.location.href);
    return url.searchParams.get(name);
  }

  async function renderWiFi() {
    const content = clearMain();
    const grid = document.createElement("div");
    grid.className = "grid";
    content.appendChild(grid);
    let statusData = null;
    let configData = null;
    let wifiCaps = null;
    try {
      const [s, c, caps] = await Promise.allSettled([
        fetchJson("/api/v1/status"),
        fetchJson("/api/v1/config"),
        fetchJson("/api/v1/capabilities/wifi"),
      ]);
      if (s.status === "fulfilled") statusData = s.value;
      if (c.status === "fulfilled") configData = c.value;
      if (caps.status === "fulfilled") wifiCaps = caps.value;
    } catch (_) {}
    const cfg = (configData && (configData.config || configData)) || null;
    const wifiCfg = cfg?.wifi || {};
    const wifiSt = statusData?.wifi || {};
    const rows = [];
    if (wifiCfg.enable != null) rows.push(["启用", wifiCfg.enable ? "是" : "否"]);
    if (wifiCfg.bridgeToLan != null) rows.push(["桥接到 LAN", wifiCfg.bridgeToLan ? "是" : "否"]);
    if (wifiSt.enabled != null) rows.push(["状态", wifiSt.enabled ? "已启用" : "未启用"]);
    if (wifiSt.aps != null) rows.push(["AP 数量（状态）", String(wifiSt.aps)]);
    grid.appendChild(renderKeyValueCard("WiFi 概览（只读）", rows.length ? rows : [["信息", "未提供"]]));

    // AP 列表（来自 config）
    if (Array.isArray(wifiCfg.aps) && wifiCfg.aps.length) {
      const apCard = document.createElement("div");
      apCard.className = "card";
      const apItems = wifiCfg.aps.map((ap, idx) => {
        const lines = [
          ["SSID", ap?.ssid ?? "-"],
          ["频段", ap?.band ?? "-"],
          ["信道", ap?.channel != null ? String(ap.channel) : "-"],
          ["启用", ap?.enable ? "是" : "否"],
          // PSK 为脱敏字段，不展示明文
        ];
        const rows = lines.map(([k, v]) => html`<div class="k">${escapeHtml(k)}</div><div class="v">${escapeHtml(String(v))}</div>`).join("");
        return html`<div class="kv">${rows}</div>`;
      }).join("<hr class=\"sep\" />");
      apCard.innerHTML = html`<h3>接入点（AP）</h3><div>${apItems}</div>`;
      grid.appendChild(apCard);
    }

    // 能力
    if (wifiCaps) {
      const capsEntries = [];
      if (wifiCaps.maxAP != null) capsEntries.push(["最大 AP 数", String(wifiCaps.maxAP)]);
      if (Array.isArray(wifiCaps.bands)) capsEntries.push(["支持频段", wifiCaps.bands.join(", ") || "-"]);
      if (wifiCaps.driver) capsEntries.push(["驱动", String(wifiCaps.driver)]);
      grid.appendChild(renderKeyValueCard("WiFi 能力", capsEntries.length ? capsEntries : [["信息", "未提供"]]));
    }

    if (configData) grid.appendChild(renderJSONCard("配置 JSON", configData));
    if (statusData) grid.appendChild(renderJSONCard("状态 JSON", statusData));
  }

  async function renderDNS() {
    const content = clearMain();
    const grid = document.createElement("div");
    grid.className = "grid";
    content.appendChild(grid);
    let configData = null;
    try {
      configData = await fetchJson("/api/v1/config");
    } catch (_) {}
    const cfg = (configData && (configData.config || configData)) || null;
    const dns = cfg?.dns || {};
    const rows = [];
    if (dns.enableDnsmasq != null) rows.push(["启用 dnsmasq", dns.enableDnsmasq ? "是" : "否"]);
    if (dns.domain) rows.push(["域名", String(dns.domain)]);
    if (Array.isArray(dns.upstreams)) rows.push(["上游 DNS", dns.upstreams.join(", ") || "-"]);
    grid.appendChild(renderKeyValueCard("DNS（只读）", rows.length ? rows : [["信息", "未提供"]]));
    if (configData) grid.appendChild(renderJSONCard("配置 JSON", configData));
  }

  async function renderFirewall() {
    const content = clearMain();
    const grid = document.createElement("div");
    grid.className = "grid";
    content.appendChild(grid);
    let configData = null;
    try {
      configData = await fetchJson("/api/v1/config");
    } catch (_) {}
    const cfg = (configData && (configData.config || configData)) || null;
    const fw = cfg?.firewall || {};
    const rows = [];
    if (fw.enable != null) rows.push(["启用", fw.enable ? "是" : "否"]);
    if (fw.natEnabled != null) rows.push(["NAT", fw.natEnabled ? "启用" : "关闭"]);
    if (fw.description) rows.push(["说明", String(fw.description)]);
    rows.push(["端口转发", "未提供"]);
    grid.appendChild(renderKeyValueCard("防火墙（只读）", rows.length ? rows : [["信息", "未提供"]]));
    if (configData) grid.appendChild(renderJSONCard("配置 JSON", configData));
  }

  async function renderSSH() {
    const content = clearMain();
    const grid = document.createElement("div");
    grid.className = "grid";
    content.appendChild(grid);
    let configData = null;
    let statusData = null;
    try {
      const [c, s] = await Promise.allSettled([
        fetchJson("/api/v1/config"),
        fetchJson("/api/v1/status"),
      ]);
      if (c.status === "fulfilled") configData = c.value;
      if (s.status === "fulfilled") statusData = s.value;
    } catch (_) {}
    const cfg = (configData && (configData.config || configData)) || null;
    const ssh = cfg?.ssh || {};
    const sshStat = statusData?.ssh || {};
    const rows = [];
    if (ssh.enable != null) rows.push(["启用", ssh.enable ? "是" : "否"]);
    if (ssh.port != null) rows.push(["端口", String(ssh.port)]);
    if (ssh.passwordAuth != null) rows.push(["密码登录", ssh.passwordAuth ? "允许" : "禁止"]);
    if (Array.isArray(ssh.authorizedKeys)) rows.push(["AuthorizedKeys", ssh.authorizedKeys.length ? String(ssh.authorizedKeys.length) : "0"]);
    if (sshStat.enabled != null) rows.push(["运行状态", sshStat.enabled ? "已启用" : "未启用"]);
    if (sshStat.port != null) rows.push(["运行端口", String(sshStat.port)]);
    grid.appendChild(renderKeyValueCard("SSH（只读）", rows.length ? rows : [["信息", "未提供"]]));
    if (Array.isArray(ssh.authorizedKeys) && ssh.authorizedKeys.length) {
      const keysCard = document.createElement("div");
      keysCard.className = "card";
      keysCard.innerHTML = `<h3>AuthorizedKeys</h3><pre class="json-view">${escapeHtml(ssh.authorizedKeys.join("\n"))}</pre>`;
      grid.appendChild(keysCard);
    }
    if (configData) grid.appendChild(renderJSONCard("配置 JSON", configData));
    if (statusData) grid.appendChild(renderJSONCard("状态 JSON", statusData));
  }

  async function renderSystem() {
    const content = clearMain();
    const grid = document.createElement("div");
    grid.className = "grid";
    content.appendChild(grid);
    let configData = null;
    let statusData = null;
    let healthData = null;
    try {
      const [c, s, h] = await Promise.allSettled([
        fetchJson("/api/v1/config"),
        fetchJson("/api/v1/status"),
        fetchJson("/api/v1/health"),
      ]);
      if (c.status === "fulfilled") configData = c.value;
      if (s.status === "fulfilled") statusData = s.value;
      if (h.status === "fulfilled") healthData = h.value;
    } catch (_) {}
    const cfg = (configData && (configData.config || configData)) || null;
    const sys = cfg?.system || {};
    const sysSt = statusData?.system || {};
    const rows = [];
    const hostname = sysSt.hostname || sys.hostname;
    if (hostname) rows.push(["主机名", String(hostname)]);
    if (sys.timezone) rows.push(["时区", String(sys.timezone)]);
    if (healthData?.uptimeSecs != null) rows.push(["运行时间", `${healthData.uptimeSecs}s`]);
    if (healthData?.version) rows.push(["版本", String(healthData.version)]);
    if (healthData?.startedAt) rows.push(["启动时间", String(healthData.startedAt)]);
    grid.appendChild(renderKeyValueCard("系统（只读）", rows.length ? rows : [["信息", "未提供"]]));
    if (configData) grid.appendChild(renderJSONCard("配置 JSON", configData));
    if (statusData) grid.appendChild(renderJSONCard("状态 JSON", statusData));
    if (healthData) grid.appendChild(renderJSONCard("健康 JSON", healthData));
  }

  async function renderClients() {
    const content = clearMain();
    const grid = document.createElement("div");
    grid.className = "grid";
    content.appendChild(grid);
    try {
      const data = await fetchJson("/api/v1/clients");
      // 兼容形状：若返回数组则直接渲染，若对象含 clients/items 则取其数组
      const list = Array.isArray(data) ? data : (data?.clients || data?.items || []);
      if (!Array.isArray(list) || list.length === 0) {
        grid.appendChild(renderKeyValueCard("客户端（只读）", [["状态", "无数据"]]));
      } else {
        const card = document.createElement("div");
        card.className = "card";
        const items = list.map((c, i) => {
          if (c && typeof c === "object") {
            const pairs = Object.entries(c).slice(0, 8).map(([k, v]) => [String(k), String(v)]);
            const rows = pairs.map(([k, v]) => html`<div class="k">${escapeHtml(k)}</div><div class="v">${escapeHtml(v)}</div>`).join("");
            return html`<div class="kv">${rows}</div>`;
          }
          return html`<div class="kv"><div class="k">项</div><div class="v">${escapeHtml(String(c))}</div></div>`;
        }).join("<hr class=\"sep\" />");
        card.innerHTML = `<h3>客户端列表</h3>${items}`;
        grid.appendChild(card);
      }
      grid.appendChild(renderJSONCard("客户端 JSON", data));
    } catch (e) {
      if (e?.status === 404) {
        const card = document.createElement("div");
        card.className = "card";
        card.innerHTML = `<h3>客户端（只读）</h3><p class="muted">接口尚未提供</p>`;
        grid.appendChild(card);
      } else if (e?.status === 401) {
        // 已由全局 401 处理
      } else {
        grid.appendChild(renderJSONCard("错误", { error: e?.message || "请求失败" }));
      }
    }
  }

  function getApiBase() {
    const fromWin = typeof window.NIXOS_ROUTER_API === "string" && window.NIXOS_ROUTER_API.trim();
    const fromQuery = getQueryParam("api");
    // 默认使用页面同源（用户以局域网 IP 打开时，直接同源访问后端）
    const origin = (typeof window.location?.origin === "string" && window.location.origin) || "";
    const base = (fromQuery || fromWin || origin).replace(/\/+$/, "");
    return base;
  }

  const apiBase = getApiBase();
  const state = {
    backendConnected: false,
    loggedIn: false,
    currentUser: null,
    navItems: [],
    coreNav: [
      { id: "overview", label: "总览", path: "#/overview" },
      { id: "wan", label: "外网", path: "#/wan" },
      { id: "lan", label: "内网", path: "#/lan" },
      { id: "wifi", label: "无线", path: "#/wifi" },
      { id: "clients", label: "客户端", path: "#/clients" },
      { id: "dns", label: "DNS", path: "#/dns" },
      { id: "firewall", label: "防火墙", path: "#/firewall" },
      { id: "ssh", label: "SSH", path: "#/ssh" },
      { id: "system", label: "系统", path: "#/system" },
      { id: "plugins", label: "插件", path: "#/plugins" },
    ],
  };

  function withTimeout(promise, ms = 8000) {
    let timer;
    const timeout = new Promise((_, reject) => {
      timer = setTimeout(() => reject(new Error("fetch timeout")), ms);
    });
    return Promise.race([promise, timeout]).finally(() => clearTimeout(timer));
  }

  function onUnauthorized() {
    state.loggedIn = false;
    state.currentUser = null;
    showLoginView();
  }

  function showLoginView() {
    const layout = q(".layout");
    const login = q("#login-view");
    const banner = q("#status-banner");
    layout?.classList.add("hidden");
    login?.classList.remove("hidden");
    banner?.classList.add("hidden");
    const apiEl = q("#login-api-base");
    if (apiEl) apiEl.textContent = apiBase;
    updateBannerApiBase();
  }
  function hideLoginView() {
    const layout = q(".layout");
    const login = q("#login-view");
    layout?.classList.remove("hidden");
    login?.classList.add("hidden");
  }

  function setUserDisplay() {
    const el = q("#user-display");
    if (!el) return;
    const name = state?.currentUser?.username || state?.currentUser?.name || state?.currentUser?.user || "";
    el.textContent = name ? `已登录：${name}` : "";
  }

  async function fetchJson(path, init = {}) {
    const url = `${apiBase}${path}`;
    const {
      method = "GET",
      body,
      headers = {},
      ...rest
    } = init || {};
    const finalHeaders = {
      "Accept": "application/json",
      ...headers,
    };
    let finalBody = body;
    if (body && typeof body === "object" && !(body instanceof FormData)) {
      finalHeaders["Content-Type"] = "application/json";
      finalBody = JSON.stringify(body);
    }
    const resp = await withTimeout(fetch(url, {
      method,
      credentials: "include",
      headers: finalHeaders,
      body: finalBody,
      ...rest,
    }));
    if (resp.status === 401) {
      onUnauthorized();
      const text401 = await resp.text().catch(() => "");
      const err401 = new Error("unauthorized");
      err401.status = 401;
      err401.responseText = text401;
      throw err401;
    }
    if (!resp.ok) {
      const text = await resp.text().catch(() => "");
      const err = new Error(`HTTP ${resp.status} ${resp.statusText} for ${path}`);
      err.responseText = text;
      err.status = resp.status;
      throw err;
    }
    const ct = resp.headers.get("content-type") || "";
    if (ct.includes("application/json")) {
      return resp.json();
    }
    // Accept text as JSON fallback (for early stubs)
    const t = await resp.text();
    try { return JSON.parse(t); } catch { return { value: t }; }
  }

  function setBannerVisible(visible) {
    const banner = q("#status-banner");
    if (!banner) return;
    banner.classList.toggle("hidden", !visible);
  }

  function updateBannerApiBase() {
    const el = q("#banner-api-base");
    if (el) el.textContent = apiBase;
    const hint = q("#sidebar-api-hint");
    if (hint) {
      hint.innerHTML = `API: <code>${apiBase}</code>`;
    }
    const loginApi = q("#login-api-base");
    if (loginApi) loginApi.textContent = apiBase;
  }

  function toHashPath(p, fallbackId) {
    if (!p) return `#/${fallbackId ?? ""}`;
    if (p.startsWith("#")) return p;
    if (p === "/") return "#/overview";
    if (p.startsWith("/")) return `#${p}`;
    return `#/${p}`;
  }

  function normalizeNav(items) {
    // 平铺化导航，兼容 {nav:[{id,title,path,children:[]},...]} 或直接数组
    const src = Array.isArray(items) ? items : (items?.nav || items?.items || []);
    if (!Array.isArray(src)) return [];
    const out = [];
    const walk = (arr) => {
      for (const it of arr) {
        if (!it) continue;
        if (Array.isArray(it.children) && it.children.length) {
          walk(it.children);
          continue;
        }
        const id = it.id || it.key || it.name || (typeof it.path === "string" ? it.path.replace(/^\//, "") : "") || "";
        const label = it.title || it.label || it.name || id || "项目";
        const path = toHashPath(it.path, id);
        if (id) out.push({ id: String(id), label: String(label), path: String(path) });
      }
    };
    walk(src);
    return out;
  }

  function renderNav(items) {
    const nav = q("#nav-list");
    if (!nav) return;
    nav.innerHTML = "";
    const current = location.hash || "#/overview";
    const all = items && items.length ? items : state.coreNav;
    for (const item of all) {
      const li = document.createElement("li");
      const a = document.createElement("a");
      a.className = "nav-link";
      a.href = item.path || (`#/${item.id}`);
      a.textContent = item.label || item.id;
      if (current === a.href.split("#")[1] ? "#" + a.href.split("#")[1] : a.getAttribute("href")) {
        // no-op; rely on hashchange to set active
      }
      li.appendChild(a);
      nav.appendChild(li);
    }
    setActiveLink();
  }

  function setActiveLink() {
    const hash = location.hash || "#/overview";
    qc(".nav-link").forEach(a => {
      const href = a.getAttribute("href") || "";
      a.classList.toggle("active", href === hash);
    });
    const title = routeTitle(hash);
    q("#page-title").textContent = title;
  }

  function routeTitle(hash) {
    const id = (hash.replace(/^#\//, "") || "overview").split("?")[0];
    const found = [...state.coreNav, ...state.navItems].find(i => (i.id === id) || (i.path === hash));
    if (found?.label) return found.label;
    const map = {
      "overview": "总览",
      "wan": "外网",
      "lan": "内网",
      "wifi": "无线",
      "clients": "客户端",
      "dns": "DNS",
      "firewall": "防火墙",
      "ssh": "SSH",
      "system": "系统",
      "plugins": "插件",
    };
    return map[id] || id;
  }

  async function loadNav() {
    updateBannerApiBase();
    try {
      // Try ui/nav first
      const uiNav = await fetchJson("/api/v1/ui/nav");
      const normalized = normalizeNav(uiNav);
      if (normalized.length > 0) {
        state.navItems = mergeCoreAndExternal(state.coreNav, normalized);
        state.backendConnected = true;
        setBannerVisible(false);
        renderNav(state.navItems);
        return;
      }
      // If empty, try plugins list
      const plugins = await fetchJson("/api/v1/plugins");
      const list = Array.isArray(plugins) ? plugins : (plugins?.plugins || plugins?.items || []);
      const pluginItems = (Array.isArray(list) ? list : []).map((p) => {
        const name = (typeof p === "string") ? p : (p.name || p.id || p.key);
        if (!name) return null;
        return {
          id: `plugin:${name}`,
          label: `插件 ${name}`,
          path: `#/plugins/${encodeURIComponent(name)}`,
        };
      }).filter(Boolean);
      state.navItems = mergeCoreAndExternal(state.coreNav, pluginItems);
      state.backendConnected = true;
      setBannerVisible(false);
      renderNav(state.navItems);
    } catch (e) {
      console.warn("Nav load failed; fallback to core nav", e);
      state.backendConnected = false;
      setBannerVisible(true);
      renderNav(state.coreNav);
    }
  }

  function mergeCoreAndExternal(core, ext) {
    const ids = new Set(core.map(i => i.id));
    const merged = [...core];
    for (const it of ext) {
      if (!ids.has(it.id)) merged.push(it);
    }
    return merged;
  }

  function html(strings, ...values) {
    return strings.reduce((acc, s, i) => acc + s + (values[i] ?? ""), "");
  }

  function renderJSONCard(title, data) {
    const container = document.createElement("div");
    container.className = "card";
    container.innerHTML = html`
      <h3>${title}</h3>
      <pre class="json-view">${escapeHtml(JSON.stringify(data ?? {}, null, 2))}</pre>
    `;
    return container;
  }

  function escapeHtml(s) {
    return String(s)
      .replaceAll("&", "&amp;")
      .replaceAll("<", "&lt;")
      .replaceAll(">", "&gt;")
      .replaceAll("\"", "&quot;")
      .replaceAll("'", "&#039;");
  }

  function renderKeyValueCard(title, entries) {
    const container = document.createElement("div");
    container.className = "card";
    const rows = entries
      .filter(Boolean)
      .map(([k, v]) => html`
        <div class="k">${escapeHtml(k)}</div>
        <div class="v">${escapeHtml(v ?? "-")}</div>
      `)
      .join("");
    container.innerHTML = html`
      <h3>${title}</h3>
      <div class="kv">${rows}</div>
    `;
    return container;
  }

  function clearMain() {
    const content = q("#page-content");
    content.innerHTML = "";
    return content;
  }

  async function renderOverview() {
    const content = clearMain();
    const grid = document.createElement("div");
    grid.className = "grid";
    content.appendChild(grid);

    let statusData = null;
    let configData = null;
    let healthData = null;
    let wifiCaps = null;
    try {
      const [statusRes, configRes, healthRes, wifiCapsRes] = await Promise.allSettled([
        fetchJson("/api/v1/status"),
        fetchJson("/api/v1/config"),
        fetchJson("/api/v1/health"),
        fetchJson("/api/v1/capabilities/wifi"),
      ]);
      if (statusRes.status === "fulfilled") statusData = statusRes.value;
      if (configRes.status === "fulfilled") configData = configRes.value;
      if (healthRes.status === "fulfilled") healthData = healthRes.value;
      if (wifiCapsRes.status === "fulfilled") wifiCaps = wifiCapsRes.value;
      if (statusRes.status === "fulfilled" || configRes.status === "fulfilled") {
        state.backendConnected = true;
        setBannerVisible(false);
      }
    } catch (e) {
      // ignore; fallback to banner already set by nav loader
    }

    const summaryEntries = [];
    const cfg = (configData && (configData.config || configData)) || null;
    const hostname = (statusData?.system?.hostname || cfg?.system?.hostname);
    const uptime = (healthData?.uptimeSecs != null ? `${healthData.uptimeSecs}s` : undefined);
    const ifs = Array.isArray(statusData?.interfaces) ? statusData.interfaces : [];
    const lan = ifs.find(i => i?.role === "lan");
    const wan = ifs.find(i => i?.role === "wan");
    const lanCidr = lan?.cidr || cfg?.lan?.ipv4Cidr;
    const wanMode = wan?.mode || cfg?.wan?.mode;
    const wifiAps = (statusData?.wifi?.aps ?? (Array.isArray(cfg?.wifi?.aps) ? cfg.wifi.aps.length : undefined));

    if (hostname) summaryEntries.push(["主机名", String(hostname)]);
    if (uptime != null) summaryEntries.push(["运行时间", String(uptime)]);
    if (typeof wanMode === "string") summaryEntries.push(["外网模式", String(wanMode)]);
    if (lanCidr) summaryEntries.push(["内网", String(lanCidr)]);
    if (wifiAps != null) summaryEntries.push(["AP 数量", String(wifiAps)]);

    grid.appendChild(renderKeyValueCard("系统概览", summaryEntries.length ? summaryEntries : [["状态", state.backendConnected ? "连接正常" : "未知"]]));

    // 接口列表（按 role 简要展示）
    if (ifs.length > 0) {
      const ifaceCard = document.createElement("div");
      ifaceCard.className = "card";
      const items = ifs.map((it) => {
        const lines = [
          ["名称", it?.name ?? "-"],
          ["角色", it?.role ?? "-"],
          ["状态", (it?.up === true ? "已连接" : (it?.up === false ? "未连接" : "未知"))],
        ];
        if (it?.role === "wan" && it?.mode) lines.push(["模式", it.mode]);
        if (it?.role === "lan" && it?.cidr) lines.push(["CIDR", it.cidr]);
        const rows = lines.map(([k, v]) => html`<div class="k">${escapeHtml(k)}</div><div class="v">${escapeHtml(String(v))}</div>`).join("");
        return html`<div class="kv iface">${rows}</div>`;
      }).join("<hr class=\"sep\" />");
      ifaceCard.innerHTML = html`
        <h3>接口</h3>
        <div class="iface-list">${items}</div>
      `;
      grid.appendChild(ifaceCard);
    }

    // WiFi 能力摘要
    if (wifiCaps) {
      const capsEntries = [];
      if (wifiCaps.maxAP != null) capsEntries.push(["最大 AP 数", String(wifiCaps.maxAP)]);
      if (Array.isArray(wifiCaps.bands)) capsEntries.push(["支持频段", wifiCaps.bands.join(", ") || "-"]);
      if (wifiCaps.driver) capsEntries.push(["驱动", String(wifiCaps.driver)]);
      grid.appendChild(renderKeyValueCard("WiFi 能力", capsEntries.length ? capsEntries : [["信息", "未提供"]]));
    }

    if (statusData) {
      grid.appendChild(renderJSONCard("状态 JSON", statusData));
    } else {
      grid.appendChild(renderJSONCard("状态 JSON（无数据）", { error: "无法加载 /api/v1/status" }));
    }
    if (configData) {
      grid.appendChild(renderJSONCard("配置 JSON", configData));
    } else {
      grid.appendChild(renderJSONCard("配置 JSON（无数据）", { error: "无法加载 /api/v1/config" }));
    }
  }

  async function renderWAN() {
    const content = clearMain();
    const grid = document.createElement("div");
    grid.className = "grid";
    content.appendChild(grid);
    let statusData = null;
    let configData = null;
    try {
      const [s, c] = await Promise.allSettled([
        fetchJson("/api/v1/status"),
        fetchJson("/api/v1/config"),
      ]);
      if (s.status === "fulfilled") statusData = s.value;
      if (c.status === "fulfilled") configData = c.value;
    } catch (_) {}
    const cfg = (configData && (configData.config || configData)) || null;
    const wanIf = (Array.isArray(statusData?.interfaces) ? statusData.interfaces : []).find(i => i?.role === "wan") || {};
    const wanCfg = cfg?.wan || {};
    const mode = wanIf.mode || wanCfg.mode;
    const iface = wanCfg.interface || wanIf.name;
    const staticCfg = wanCfg.static || {};
    const pppoe = wanCfg.pppoe || {};
    const rows = [];
    if (mode) rows.push(["模式", String(mode)]);
    if (iface) rows.push(["接口", String(iface)]);
    if (mode === "static" && (staticCfg.addressCidr || staticCfg.gateway || (Array.isArray(staticCfg.dns) && staticCfg.dns.length))) {
      if (staticCfg.addressCidr) rows.push(["地址/CIDR", String(staticCfg.addressCidr)]);
      if (staticCfg.gateway) rows.push(["网关", String(staticCfg.gateway)]);
      if (Array.isArray(staticCfg.dns)) rows.push(["DNS", staticCfg.dns.join(", ") || "-"]);
    }
    if (mode === "pppoe" && (pppoe.username || pppoe.password)) {
      if (pppoe.username) rows.push(["PPPoE 用户名", String(pppoe.username)]);
      if (pppoe.password != null) rows.push(["PPPoE 密码", "••••"]);
    }
    if (wanIf.up != null) rows.push(["链路", wanIf.up ? "已连接" : "未连接"]);
    grid.appendChild(renderKeyValueCard("外网（只读）", rows.length ? rows : [["信息", "未提供"]]));
    // 原始 JSON 便于排错
    if (configData) grid.appendChild(renderJSONCard("配置 JSON", configData));
    if (statusData) grid.appendChild(renderJSONCard("状态 JSON", statusData));
  }

  async function renderLAN() {
    const content = clearMain();
    const grid = document.createElement("div");
    grid.className = "grid";
    content.appendChild(grid);
    let statusData = null;
    let configData = null;
    try {
      const [s, c] = await Promise.allSettled([
        fetchJson("/api/v1/status"),
        fetchJson("/api/v1/config"),
      ]);
      if (s.status === "fulfilled") statusData = s.value;
      if (c.status === "fulfilled") configData = c.value;
    } catch (_) {}
    const cfg = (configData && (configData.config || configData)) || null;
    const lanIf = (Array.isArray(statusData?.interfaces) ? statusData.interfaces : []).find(i => i?.role === "lan") || {};
    const lanCfg = cfg?.lan || {};
    const dhcp = lanCfg.dhcp || {};
    const rows = [];
    if (lanCfg.bridgeName) rows.push(["桥（Bridge）", String(lanCfg.bridgeName)]);
    if (lanCfg.ipv4Cidr) rows.push(["IPv4 CIDR", String(lanCfg.ipv4Cidr)]);
    if (lanIf.up != null) rows.push(["链路", lanIf.up ? "已连接" : "未连接"]);
    // 端口/静态租约未提供时优雅降级
    rows.push(["端口", "未提供"]);
    if (dhcp.enable != null) rows.push(["DHCP", dhcp.enable ? "启用" : "关闭"]);
    if (dhcp.rangeStart || dhcp.rangeEnd) rows.push(["DHCP 范围", `${dhcp.rangeStart || "-"} - ${dhcp.rangeEnd || "-"}`]);
    if (dhcp.leaseMins != null) rows.push(["租约（分钟）", String(dhcp.leaseMins)]);
    rows.push(["静态租约", "未提供"]);
    grid.appendChild(renderKeyValueCard("内网（只读）", rows.length ? rows : [["信息", "未提供"]]));
    if (configData) grid.appendChild(renderJSONCard("配置 JSON", configData));
    if (statusData) grid.appendChild(renderJSONCard("状态 JSON", statusData));
  }
  function renderPlaceholder(id) {
    const content = clearMain();
    const wrap = document.createElement("div");
    wrap.className = "card";
    const map = {
      "wan": "外网",
      "lan": "内网",
      "wifi": "无线",
      "clients": "客户端",
      "dns": "DNS",
      "firewall": "防火墙",
      "ssh": "SSH",
      "system": "系统",
      "plugins": "插件",
    };
    const title = map[id] || id;
    wrap.innerHTML = html`
      <h3>${title}（占位）</h3>
      <p class="muted">本里程碑为只读壳，后续提供编辑与应用配置。</p>
    `;
    content.appendChild(wrap);
  }

  const routes = {
    "overview": renderOverview,
    "wan": () => renderWAN(),
    "lan": () => renderLAN(),
    "wifi": () => renderWiFi(),
    "clients": () => renderClients(),
    "dns": () => renderDNS(),
    "firewall": () => renderFirewall(),
    "ssh": () => renderSSH(),
    "system": () => renderSystem(),
    "plugins": () => renderPlaceholder("plugins"),
  };

  function currentRoute() {
    return (location.hash.replace(/^#\//, "") || "overview").split("?")[0];
  }

  async function renderRoute() {
    if (!state.loggedIn) {
      // 登录视图下不渲染主壳内容
      return;
    }
    setActiveLink();
    const id = currentRoute();
    const fn = routes[id] || (() => renderPlaceholder(id));
    await fn();
  }

  function initEvents() {
    window.addEventListener("hashchange", renderRoute);
    const nav = q("#nav-list");
    nav.addEventListener("click", (e) => {
      const a = e.target.closest("a");
      if (!a) return;
      // Let browser handle hash change; no preventDefault needed
      setTimeout(setActiveLink, 0);
    });
    const retry = q("#retry-btn");
    retry?.addEventListener("click", async () => {
      await loadNav();
      await renderRoute();
    });
    const form = q("#login-form");
    form?.addEventListener("submit", async (e) => {
      e.preventDefault();
      const u = q("#login-username")?.value?.trim() || "";
      const p = q("#login-password")?.value || "";
      const errEl = q("#login-error");
      errEl?.classList.add("hidden");
      errEl.textContent = "";
      const btn = q("#login-submit");
      btn?.setAttribute("disabled", "true");
      try {
        await fetchJson("/api/v1/session", {
          method: "POST",
          body: { username: u, password: p },
        });
        // 登录成功后，重新检查会话并进入主壳
        await checkSession();
        if (state.loggedIn) {
          hideLoginView();
          await afterLoginEnter();
        }
      } catch (e) {
        const msg = (e?.status === 401) ? "用户名或密码错误" : `登录失败：${e?.message || "未知错误"}`;
        if (errEl) {
          errEl.textContent = msg;
          errEl.classList.remove("hidden");
        } else {
          alert(msg);
        }
      } finally {
        btn?.removeAttribute("disabled");
      }
    });
    const logout = q("#logout-btn");
    logout?.addEventListener("click", async () => {
      try {
        await fetchJson("/api/v1/session", { method: "DELETE" });
      } catch (_) {
        // ignore
      }
      onUnauthorized();
    });
    // Expose a tiny API for debugging
    window.app = {
      retryConnect: async () => { await loadNav(); await renderRoute(); },
      get apiBase() { return apiBase; },
      get state() { return state; },
      async logout() { await fetchJson("/api/v1/session", { method: "DELETE" }).catch(() => {}); onUnauthorized(); },
    };
  }

  async function checkSession() {
    try {
      const s = await fetchJson("/api/v1/session");
      state.loggedIn = true;
      state.currentUser = s?.user || s || null;
      hideLoginView();
      setUserDisplay();
      return true;
    } catch (e) {
      if (e?.status === 401) {
        onUnauthorized();
        return false;
      }
      // If endpoint not ready yet, treat as unauthorized to gate content until merged
      onUnauthorized();
      return false;
    }
  }

  async function afterLoginEnter() {
    setUserDisplay();
    await loadNav();
    if (!location.hash) location.hash = "#/overview";
    await renderRoute();
  }

  async function boot() {
    initEvents();
    updateBannerApiBase();
    const ok = await checkSession();
    if (ok) {
      await afterLoginEnter();
    } else {
      showLoginView();
    }
  }

  // Start
  document.addEventListener("DOMContentLoaded", boot, { once: true });
})(); 

