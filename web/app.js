(() => {
  "use strict";

  const q = (sel, el = document) => el.querySelector(sel);
  const qc = (sel, el = document) => el.querySelectorAll(sel);

  function getQueryParam(name) {
    const url = new URL(window.location.href);
    return url.searchParams.get(name);
  }

  function getApiBase() {
    const fromWin = typeof window.NIXOS_ROUTER_API === "string" && window.NIXOS_ROUTER_API.trim();
    const fromQuery = getQueryParam("api");
    const base = (fromQuery || fromWin || "http://127.0.0.1:8080").replace(/\/+$/, "");
    return base;
  }

  const apiBase = getApiBase();
  const state = {
    backendConnected: false,
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

  async function fetchJson(path, init = {}) {
    const url = `${apiBase}${path}`;
    const resp = await withTimeout(fetch(url, {
      method: "GET",
      headers: {
        "Accept": "application/json",
        ...init.headers,
      },
      ...init,
    }));
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
  }

  function normalizeNav(items) {
    // Try to support various likely shapes
    if (!Array.isArray(items)) return [];
    return items
      .map((it) => {
        if (!it) return null;
        const id = it.id || it.key || it.name || it.slug || it.route || it.path || "";
        const label = it.label || it.name || it.title || id || "项目";
        const path = it.path || it.route || (`#/plugins/${id}`);
        return id ? { id: String(id), label: String(label), path: String(path) } : null;
      })
      .filter(Boolean);
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
      const normalized = normalizeNav(uiNav?.items || uiNav);
      if (normalized.length > 0) {
        state.navItems = mergeCoreAndExternal(state.coreNav, normalized);
        state.backendConnected = true;
        setBannerVisible(false);
        renderNav(state.navItems);
        return;
      }
      // If empty, try plugins list
      const plugins = await fetchJson("/api/v1/plugins");
      const pluginItems = normalizeNav(
        (Array.isArray(plugins) ? plugins : plugins?.items || []).map((p) => ({
          id: typeof p === "string" ? p : (p.id || p.name || p.key),
          label: (typeof p === "string" ? p : (p.label || p.name || p.id)),
          path: `#/plugins?name=${encodeURIComponent(typeof p === "string" ? p : (p.id || p.name || ""))}`,
        }))
      );
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
    try {
      const [statusRes, configRes] = await Promise.allSettled([
        fetchJson("/api/v1/status"),
        fetchJson("/api/v1/config"),
      ]);
      if (statusRes.status === "fulfilled") statusData = statusRes.value;
      if (configRes.status === "fulfilled") configData = configRes.value;
      if (statusRes.status === "fulfilled" || configRes.status === "fulfilled") {
        state.backendConnected = true;
        setBannerVisible(false);
      }
    } catch (e) {
      // ignore; fallback to banner already set by nav loader
    }

    const summaryEntries = [];
    const hostname = (statusData?.hostname || configData?.hostname || configData?.system?.hostname);
    const uptime = (statusData?.uptime || statusData?.system?.uptime);
    const wanIp = (statusData?.wan?.ip || statusData?.wan?.publicIP);
    const lanCidr = (statusData?.lan?.cidr || statusData?.lan?.ip || configData?.lan?.cidr);
    const clients = (statusData?.clients?.count || (Array.isArray(statusData?.clients) ? statusData.clients.length : undefined));
    const wifiRadios = (statusData?.wifi?.radios || configData?.wifi?.radios);

    if (hostname) summaryEntries.push(["主机名", String(hostname)]);
    if (uptime != null) summaryEntries.push(["运行时间", String(uptime)]);
    if (wanIp) summaryEntries.push(["外网 IP", String(wanIp)]);
    if (lanCidr) summaryEntries.push(["内网", String(lanCidr)]);
    if (clients != null) summaryEntries.push(["在线客户端", String(clients)]);
    if (wifiRadios != null) summaryEntries.push(["无线射频", Array.isArray(wifiRadios) ? String(wifiRadios.length) : String(wifiRadios)]);

    grid.appendChild(renderKeyValueCard("系统概览", summaryEntries.length ? summaryEntries : [["状态", state.backendConnected ? "连接正常" : "未知"]]));

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
    "wan": () => renderPlaceholder("wan"),
    "lan": () => renderPlaceholder("lan"),
    "wifi": () => renderPlaceholder("wifi"),
    "clients": () => renderPlaceholder("clients"),
    "dns": () => renderPlaceholder("dns"),
    "firewall": () => renderPlaceholder("firewall"),
    "ssh": () => renderPlaceholder("ssh"),
    "system": () => renderPlaceholder("system"),
    "plugins": () => renderPlaceholder("plugins"),
  };

  function currentRoute() {
    return (location.hash.replace(/^#\//, "") || "overview").split("?")[0];
  }

  async function renderRoute() {
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
    // Expose a tiny API for debugging
    window.app = {
      retryConnect: async () => { await loadNav(); await renderRoute(); },
      get apiBase() { return apiBase; },
      get state() { return state; },
    };
  }

  async function boot() {
    initEvents();
    await loadNav();
    if (!location.hash) location.hash = "#/overview";
    await renderRoute();
  }

  // Start
  document.addEventListener("DOMContentLoaded", boot, { once: true });
})(); 

