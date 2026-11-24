(() => {
  "use strict";
  const $ = (selector, root = document) => root.querySelector(selector);
  const clone = (value) => JSON.parse(JSON.stringify(value ?? {}));
  const apiBase = (
    new URLSearchParams(location.search).get("api") ||
    window.NIXOS_ROUTER_API ||
    location.origin
  ).replace(/\/+$/, "");
  const pages = {
    overview: ["总览", "状态", "查看接口、管理服务与数据采集状态。"],
    clients: ["客户端", "状态", "查看租约或静态绑定，管理设备名称与访问策略。"],
    wan: ["外网 WAN", "网络", "选择上网方式，并填写运营商提供的连接参数。"],
    lan: ["内网 LAN", "网络", "设置家庭网段、网桥端口和 DHCP 地址分配。"],
    wifi: [
      "无线 WiFi",
      "网络",
      "编辑接入点配置；硬件能力与实际运行状态单独确认。",
    ],
    dns: ["DNS", "网络", "设置本地域名及上游 DNS 服务器。"],
    firewall: ["防火墙", "网络", "管理 NAT、端口转发与设备访问策略。"],
    ipv6: ["IPv6", "网络", "设置外网获取方式及内网前缀分配和路由通告。"],
    qos: [
      "带宽管理",
      "服务",
      "配置总带宽和设备优先级，留意 Mbps 与 Kbps 的区别。",
    ],
    parental: ["家长控制", "服务", "按设备与时间段保存访问规则。"],
    ddns: ["动态 DNS", "服务", "为动态公网地址配置域名更新服务。"],
    plugins: [
      "插件管理",
      "扩展",
      "配置可选扩展；基础网络服务使用各自的唯一配置入口。",
    ],
    adblock: ["广告拦截", "扩展", "管理本地 DNS 域名拦截规则。"],
    traffic: [
      "流量统计",
      "扩展",
      "设置统计保留策略；采集器尚未接入时不显示模拟图表。",
    ],
    mihomo: ["Mihomo", "扩展", "配置代理服务的基础参数。"],
    vlan: ["VLAN", "扩展", "定义虚拟网络标识及关联网桥。"],
    tailscale: ["Tailscale", "扩展", "选择控制平面并保存设备接入参数。"],
    zerotier: ["ZeroTier", "扩展", "配置控制器与需要加入的网络。"],
    system: ["系统", "系统", "维护设备配置、管理员账户、备份和操作记录。"],
  };
  const pluginIds = [
    "adblock",
    "traffic",
    "mihomo",
    "vlan",
    "tailscale",
    "zerotier",
  ];
  const groups = [
    ["状态", "overview"],
    ["网络", "wan"],
    ["服务", "qos"],
    ["扩展", "plugins"],
    ["系统", "system"],
  ];
  const state = {
    loggedIn: false,
    pageDirty: false,
    busy: false,
    loading: false,
    section: null,
    configCache: null,
    health: null,
    confirmedCapabilities: null,
    hostDDNS: false,
    modules: null,
    pending: false,
    epoch: 0,
    route: "overview",
    previousHash: "",
    skipHash: false,
  };
  let fieldId = 0,
    refreshTimer, overviewView, trafficTimer, overviewRequest, trafficRequest;
  function node(tag, attrs = {}, ...children) {
    const e = document.createElement(tag);
    for (const [key, value] of Object.entries(attrs)) {
      if (value == null || value === false) continue;
      if (key === "class") e.className = value;
      else if (key.startsWith("on")) e.addEventListener(key.slice(2), value);
      else e.setAttribute(key, value === true ? "" : String(value));
    }
    for (const child of children.flat(Infinity))
      if (child != null)
        e.append(
          child instanceof Node
            ? child
            : document.createTextNode(String(child)),
        );
    return e;
  }
  const btn = (text, action, cls = "") =>
    node(
      "button",
      { type: "button", class: `btn ${cls}`, onclick: action },
      text,
    );
  const notice = (text, tone = "") =>
    node("div", { class: `notice ${tone}` }, text);
  const card = (title, ...children) =>
    node("section", { class: "card" }, node("h3", {}, title), ...children);
  const empty = (title, detail) =>
    node("div", { class: "empty" }, node("strong", {}, title), detail);
  const stack = (...children) => node("div", { class: "stack" }, ...children);
  const link = (id, text) =>
    node(
      "a",
      { href: `#/${id}`, class: "btn quiet" },
      text || pages[id]?.[0] || id,
    );
  function kv(title, entries) {
    return card(
      title,
      node(
        "dl",
        { class: "kv" },
        entries.map(([key, value]) => [
          node("dt", {}, key),
          node("dd", {}, value ?? "未提供"),
        ]),
      ),
    );
  }
  const pathParts = (path) => Array.isArray(path) ? path : path.split(".");
  const get = (object, path) =>
    pathParts(path).reduce((v, k) => v?.[k], object);
  function set(object, path, value) {
    const keys = pathParts(path);
    let parent = object;
    keys.slice(0, -1).forEach((key) => {
      parent = parent[key] ||= {};
    });
    parent[keys.at(-1)] = value;
  }
  const split = (value) =>
    value
      .split(/[\n,，]+/)
      .map((s) => s.trim())
      .filter(Boolean);
  function objectSegments(root, target, prefix = []) {
    if (root === target) return prefix;
    if (!root || typeof root !== "object") return null;
    for (const [key, value] of Object.entries(root)) {
      if (!value || typeof value !== "object") continue;
      const found = objectSegments(value, target, [...prefix, key]);
      if (found !== null) return found;
    }
    return null;
  }
  function objectPath(root, target) {
    return objectSegments(root, target)?.join(".") ?? null;
  }
  function setSecretClear(section, owner, key, selected) {
    let keys = section.secretClears.get(owner);
    if (!keys) section.secretClears.set(owner, keys = new Set());
    if (selected) keys.add(key); else keys.delete(key);
    if (!keys.size) section.secretClears.delete(owner);
  }
  function secretClearChoice(section, owner, key, label, changed = () => {}) {
    const checkbox = node("input", { type: "checkbox" });
    checkbox.checked = !!section.secretClears.get(owner)?.has(key);
    checkbox.addEventListener("change", () => {
      setSecretClear(section, owner, key, checkbox.checked);
      changed(checkbox.checked);
      dirty();
    });
    return { checkbox, element: node("label", { class: "secret-clear" }, checkbox, node("span", {}, label)) };
  }
  function pendingSecretPaths(section) {
    const paths = [];
    for (const [owner, keys] of section.secretClears) {
      const current = objectSegments(section.data, owner);
      if (current === null) continue; // deleted collection item
      for (const key of keys) paths.push([...pathParts(section.path), ...current, key]);
    }
    return paths;
  }
  function clearFieldError(input) {
    const errorId = `${input.id}-error`;
    document.getElementById(errorId)?.remove();
    input.removeAttribute("aria-invalid");
    const descriptions = (input.getAttribute("aria-describedby") || "")
      .split(/\s+/).filter((id) => id && id !== errorId);
    if (descriptions.length) input.setAttribute("aria-describedby", descriptions.join(" "));
    else input.removeAttribute("aria-describedby");
  }
  function showFieldErrors(errors = {}) {
    const controls = [...document.querySelectorAll("#config-form [data-config-path]")];
    controls.forEach(clearFieldError);
    let first;
    const hidden = [];
    for (const [path, message] of Object.entries(errors)) {
      if (typeof message !== "string") continue;
      const input = controls.filter((el) => path === el.dataset.configPath || path.startsWith(`${el.dataset.configPath}.`))
        .sort((a, b) => b.dataset.configPath.length - a.dataset.configPath.length)[0];
      if (!input || !input.getClientRects().length || input.disabled) {
        hidden.push(`${path}: ${message}`);
        continue;
      }
      const errorId = `${input.id}-error`;
      let detail = document.getElementById(errorId);
      if (!detail) {
        detail = node("p", { id: errorId, class: "help field-error" });
        input.closest(".form-row").append(detail);
        input.setAttribute("aria-describedby", [input.getAttribute("aria-describedby"), errorId].filter(Boolean).join(" "));
      }
      detail.textContent += `${detail.textContent ? "；" : ""}${message}`;
      input.setAttribute("aria-invalid", "true");
      first ||= input;
    }
    first?.focus();
    return hidden.length ? `另有配置字段需要检查：${hidden.join("；")}` : "";
  }
  function field(data, path, label, options = {}) {
    const id = `field-${++fieldId}`,
      type = options.type || "text";
    const input = options.choices
      ? node("select", { id })
      : node(type === "textarea" ? "textarea" : "input", {
          id,
          type: type === "textarea" ? null : type,
          rows: type === "textarea" ? 4 : null,
        });
    if (options.choices)
      options.choices.forEach(([value, title]) =>
        input.append(node("option", { value }, title)),
      );
    const relative = state.section ? objectPath(state.section.data, data) : null;
    const configPath = options.configPath ?? (relative !== null ? [...pathParts(state.section.path), relative, ...pathParts(path)].filter(Boolean).join(".") : null);
    if (configPath) input.dataset.configPath = configPath;
    let value = get(data, path);
    if (value == null && (options.default != null || options.choices)) {
      value = options.default ?? options.choices[0][0];
      set(data, path, value);
    }
    if (type === "checkbox") input.checked = !!value;
    else if (options.list)
      input.value = Array.isArray(value) ? value.join("\n") : "";
    else input.value = value ?? options.default ?? "";
    if (type === "password") {
      input.value = "";
      input.placeholder = value ? "已设置；留空保留" : "请输入密钥";
      input.autocomplete = "new-password";
    }
    for (const k of [
      "min",
      "max",
      "step",
      "pattern",
      "required",
      "placeholder",
      "maxlength",
      "minlength",
    ])
      if (options[k] != null && options[k] !== false)
        input.setAttribute(k, options[k]);
    if (type === "number" && options.min == null) input.min = "0";
    const helpText =
      options.help ||
      (type === "password"
        ? "留空保留已保存的密钥；新增项目需要填写真实密钥。"
        : "");
    const row = node(
      "div",
      {
        class: `form-row ${type === "checkbox" ? "check" : ""} ${options.full ? "full" : ""}`,
      },
      node("label", { for: id }, label),
      input,
    );
    if (helpText) {
      const help = node("p", { id: `${id}-help`, class: "help" }, helpText);
      input.setAttribute("aria-describedby", help.id);
      row.append(help);
    }
    let clearChoice;
    if (type === "password" && value && relative !== null) {
      const section = state.section, keys = pathParts(path);
      const owner = keys.length === 1 ? data : get(data, keys.slice(0, -1));
      const key = keys.at(-1), required = input.required;
      clearChoice = secretClearChoice(section, owner, key, `清空${label}`, (selected) => {
        input.required = selected ? false : required;
        input.placeholder = selected ? "保存时清空；取消勾选可保留" : "已设置；留空保留";
      });
      if (clearChoice.checkbox.checked) input.required = false;
      row.append(clearChoice.element);
      clearChoice.cancel = () => {
        clearChoice.checkbox.checked = false;
        setSecretClear(section, owner, key, false);
        input.required = required;
        input.placeholder = "已设置；留空保留";
      };
    }
    input.addEventListener(
      type === "checkbox" || options.choices ? "change" : "input",
      () => {
        clearFieldError(input);
        const next =
          type === "checkbox"
            ? input.checked
            : options.list
              ? split(input.value)
              : type === "number"
                ? input.value === ""
                  ? 0
                  : Number(input.value)
                : input.value;
        if (type === "password" && next !== "") clearChoice?.cancel();
        // An empty password retains the existing secret; new credentials start empty.
        if (type !== "password" || next !== "") set(data, path, next);
        else if (!value) set(data, path, "");
        else set(data, path, value);
        dirty();
        options.change?.(input.value);
      },
    );
    return row;
  }
  function formCard(title, fields) {
    return card(title, node("div", { class: "form-grid" }, fields));
  }
  function collection(title, items, renderItem, makeItem) {
    const body = node("div");
    const add = btn("新增", () => {
      items.push(makeItem());
      draw();
      dirty();
      body.lastElementChild?.querySelector("input,select,textarea")?.focus();
    });
    const root = node(
      "section",
      { class: "card" },
      node("div", { class: "collection-head" }, node("h3", {}, title), add),
      body,
    );
    function draw() {
      body.replaceChildren();
      if (!items.length) {
        body.append(empty("尚未添加", `使用“新增”配置${title}。`));
        return;
      }
      items.forEach((item, index) => {
        const remove = btn(
          "删除",
          () => {
            items.splice(index, 1);
            draw();
            dirty();
            add.focus();
          },
          "quiet danger",
        );
        remove.setAttribute("aria-label", `删除${title}第 ${index + 1} 项`);
        body.append(
          node(
            "fieldset",
            { class: "collection-item" },
            node("legend", {}, `${title} · ${index + 1}`),
            node("div", { class: "item-head" }, remove),
            node("div", { class: "form-grid" }, renderItem(item, index)),
          ),
        );
      });
    }
    draw();
    return root;
  }
  function stringList(data, path, title, label, placeholder = "") {
    const arr = get(data, path) || [];
    set(data, path, arr);
    return collection(
      title,
      arr,
      (_, index) =>
        field(arr, String(index), label, { full: true, placeholder }),
      () => "",
    );
  }
  function table(headers, rows, caption) {
    const result = node(
      "table",
      {},
      node("caption", { class: "hidden" }, caption),
      node(
        "thead",
        {},
        node(
          "tr",
          {},
          headers.map((h) => node("th", { scope: "col" }, h)),
        ),
      ),
      node(
        "tbody",
        {},
        rows.map((row) =>
          node(
            "tr",
            {},
            row.map((cell) => node("td", {}, cell)),
          ),
        ),
      ),
    );
    return node(
      "div",
      { class: "table-wrap", tabindex: "0", "aria-label": caption },
      result,
    );
  }
  async function api(
    path,
    { method = "GET", body, raw = false, timeout = 12000, revision, onUnauthorized = showLogin } = {},
  ) {
    const controller = new AbortController(),
      timer = setTimeout(() => controller.abort(), timeout);
    try {
      const headers = { Accept: "application/json" };
      if (revision !== undefined) headers["If-Match"] = JSON.stringify(revision);
      if (body && !(body instanceof FormData)) {
        headers["Content-Type"] = "application/json";
        body = JSON.stringify(body);
      }
      const response = await fetch(apiBase + path, {
        method,
        headers,
        body,
        credentials: "include",
        signal: controller.signal,
        cache: "no-store",
      });
      if (response.status === 401 && path !== "/api/v1/session/password") {
        onUnauthorized();
        const error = new Error("登录已过期，请重新登录");
        error.status = 401;
        throw error;
      }
      if (!response.ok) {
        const detail = await response.text();
        let data;
        try {
          data = JSON.parse(detail);
        } catch {}
        const error = new Error(
          data?.message ||
            data?.error ||
            data?.errors?.join("；") ||
            detail.slice(0, 500) ||
            `请求失败（HTTP ${response.status}）`,
        );
        error.status = response.status;
        error.code = data?.code;
        error.fieldErrors = data?.fieldErrors && typeof data.fieldErrors === "object" && !Array.isArray(data.fieldErrors) ? data.fieldErrors : {};
        throw error;
      }
      if (raw) return response;
      if (response.status === 204) return {};
      const data = await response.json();
      const etag = response.headers.get("ETag");
      if (etag) data.revision = JSON.parse(etag);
      return data;
    } catch (error) {
      if (error.name === "AbortError") {
        const timeoutError = new Error(
          "请求超时；若已提交操作，请查看作业记录确认结果后再重试。",
          { cause: error },
        );
        timeoutError.name = "AbortError";
        throw timeoutError;
      }
      throw error;
    } finally {
      clearTimeout(timer);
    }
  }
  function feedback(text, tone = "") {
    const e = $("#apply-msg");
    e.textContent = text;
    e.dataset.tone = tone;
    e.classList.toggle("hidden", !text);
  }
  function dirty() {
    state.pageDirty = true;
    syncActions();
  }
  function syncActions() {
    $("#apply-dirty").classList.toggle("hidden", !state.pageDirty);
    $("#save-state").textContent = state.busy
      ? "正在处理，请稍候"
      : state.pageDirty
        ? "修改尚未保存"
        : state.pending
          ? "已保存，尚未生成或应用"
          : "没有未保存的修改";
    $("#apply-save").disabled =
      state.busy || state.loading || !state.section || !state.pageDirty || sectionReadOnly();
    $("#apply-undo").disabled = state.busy || state.loading || !state.pageDirty;
    $("#apply-run").disabled = state.busy || state.loading || state.hostDDNS;
    $("#page-refresh").disabled = state.busy || state.loading;
    $("#logout-btn").disabled = state.busy;
    const fields = $("#editor-fields");
    if (fields) fields.disabled = state.busy || sectionReadOnly();
    const restoreButton = $("#restore-submit");
    if (restoreButton) restoreButton.disabled = state.busy;
  }
  function hostManagedSection() {
    return !!state.section && (state.confirmedCapabilities?.hostManaged || []).includes(state.section.path);
  }
  function sectionReadOnly() {
    return hostManagedSection() ||
      (state.confirmedCapabilities === null && ["ssh", "system"].includes(state.section?.path));
  }
  function ownershipNotice() {
    return state.confirmedCapabilities === null
      ? "尚未确认部署能力，暂时只读。请刷新健康状态后再编辑 SSH 或系统设置。"
      : "此设置由 NixOS 管理。这里仅展示保存的配置，网络应用不会修改系统主机名、时区或 SSH；请通过设备的 NixOS 配置变更。";
  }
  function deployment() {
    const mode = state.health?.deployment?.mode;
    $("#deployment-mode").textContent =
      mode === "generate-only"
        ? "仅生成模式"
        : mode === "runtime"
          ? "运行应用模式"
          : "模式未报告";
    $("#deployment-description").textContent =
      mode === "generate-only"
        ? "保存配置后可生成文件；此部署不会修改实际网络。"
        : mode === "runtime"
          ? "应用会修改部署环境；生成和应用结果请以作业记录为准。"
          : "暂不能确认部署能力，请刷新连接。";
    if (state.health?.deployment?.profile === "managed-router") {
      const caps = state.health.capabilities || {};
      const modes = (caps.wanModes || []).map(mode => ({dhcp:"DHCP",static:"静态地址",pppoe:"PPPoE"})[mode] || mode).join(" / ");
      const ipv6 = (caps.ipv6WanModes || []).length ? "IPv4 / IPv6" : "IPv4";
      const networks = caps.independentWiFiLAN ? "有线内网与可选独立无线网段" : "有线内网";
      $("#deployment-description").textContent = `${ipv6} 配置框架，${networks}，WAN 支持 ${modes}，最多一个接入点。无线标准与带宽按实际设备能力校验；当前 5 GHz 仅支持非 DFS 信道。配置能力不代表当前联网状态，请查看接口与作业结果。SSH 与系统设置由 NixOS 管理。`;
    } else if (state.health?.deployment?.profile === "ipv4-single-lan") {
      const ppp = state.health?.capabilities?.wanModes?.includes("pppoe");
      const modes = ppp ? "DHCP / 静态地址 / PPPoE" : "DHCP / 静态地址";
      const unsupported = ppp ? "IPv6" : "PPPoE、IPv6";
      $("#deployment-description").textContent = `IPv4 单内网，WAN 支持 ${modes}，最多一个接入点，5 GHz 仅支持非 DFS 信道；不支持 ${unsupported}、QoS、DDNS、家长控制、插件、UPnP 和访客网络。SSH 与系统设置由 NixOS 管理，本页应用不会修改它们。`;
    }
    $("#apply-run").textContent =
      mode === "generate-only" ? "生成配置" : "应用配置";
  }
  function routeInfo() {
    const [raw, query = ""] = (
      location.hash.replace(/^#\//, "") || "overview"
    ).split("?");
    const id = raw.startsWith("plugins/")
      ? decodeURIComponent(raw.slice(8))
      : raw;
    return { id, tab: new URLSearchParams(query).get("tab") || "general" };
  }
  function navigation(id) {
    const info = pages[id] || [moduleSupport(id)?.title || id, "扩展", "管理扩展配置。"];
    $("#page-title").textContent = info[0];
    $("#page-description").textContent = info[2];
    document.title = `${info[0]} · NixOS 路由器`;
    $("#top-nav").replaceChildren(
      ...groups.map(([group, home]) =>
        node(
          "a",
          {
            href: `#/${home}`,
            "aria-current": info[1] === group ? "page" : null,
          },
          group,
        ),
      ),
    );
    const items = Object.entries(pages).filter(([key, v]) => v[1] === info[1] && (!pluginIds.includes(key) || state.modules?.find(m => m.id === key)?.available));
    items.push(...(state.modules || []).filter(m => m.available && !pages[m.id] && info[1] === "扩展").map(m => [m.id, [m.title, "扩展"]]));
    $("#section-tabs").replaceChildren(
      ...items.map(([key, v]) =>
        node(
          "a",
          { href: `#/${key}`, "aria-current": key === id ? "page" : null },
          v[0],
        ),
      ),
    );
    $("#section-tabs").classList.toggle("hidden", items.length < 2);
  }
  function beginEditor(cfg, path) {
    const data = clone(get(cfg, path));
    state.section = { path, data, base: clone(cfg), revision: cfg.revision, secretClears: new Map() };
    return data;
  }
  function editor(...cards) {
    return node(
      "form",
      {
        id: "config-form",
        onsubmit: (e) => {
          e.preventDefault();
          save();
        },
      },
      node("fieldset", { id: "editor-fields" }, stack(cards)),
    );
  }
  async function save() {
    if (state.busy) return false;
    if (!state.section) return true;
    if (sectionReadOnly()) {
      feedback(ownershipNotice(), "error");
      return false;
    }
    if (!$("#config-form")?.reportValidity()) {
      feedback("保存失败：请检查标出的表单字段。", "error");
      return false;
    }
    const section = state.section,
      candidate = clone(section.data);
    let rejected;
    showFieldErrors();
    state.busy = true;
    syncActions();
    try {
      const cfg = clone(section.base);
      set(cfg, section.path, candidate);
      const clearSecrets = pendingSecretPaths(section);
      const body = clearSecrets.length ? { config: cfg, clearSecrets } : cfg;
      const result = await api("/api/v1/config", { method: "PUT", body, revision: section.revision });
      cfg.revision = result.revision;
      section.revision = result.revision;
      section.base = clone(cfg);
      state.configCache = clone(cfg);
      state.pageDirty = false;
      state.pending = true;
      // Plugin secret references belong to a revision. Rebind the editor to
      // newly redacted values after saving, including advanced JSON closures.
      if (clearSecrets.length || Object.keys(cfg.plugins || {}).length) await renderRoute();
      feedback("配置已保存，尚未生成或应用。", "good");
      return true;
    } catch (e) {
      rejected = e;
      feedback(`保存失败：${e.message}`, "error");
      return false;
    } finally {
      state.busy = false;
      syncActions();
      if (rejected) {
        const hidden = showFieldErrors(rejected.fieldErrors);
        if (hidden) feedback(`保存失败：${rejected.message}；${hidden}`, "error");
      }
    }
  }
  let candidateStatus = null;
  let candidatePoll = null;
  async function refreshCandidate() {
    if (!state.loggedIn || state.health?.deployment?.profile !== "managed-router") return;
    const panel = $("#candidate-banner");
    try {
      const response = await api("/api/v1/apply/candidate", {timeout: 5000});
      candidateStatus = response.confirmation;
      panel.replaceChildren();
      panel.classList.toggle("hidden", !candidateStatus);
      if (!candidateStatus) return;
      const current = candidateStatus;
      if (current.state === "pending") {
        const seconds = Math.max(0, Math.ceil((Date.parse(current.deadline) - Date.now()) / 1000));
        panel.append(document.createTextNode(`候选配置尚未确认，约 ${seconds} 秒后自动恢复旧配置。请先验证所需网络与管理路径，再确认保留。`));
        const button = document.createElement("button");
        button.className = "btn"; button.textContent = "确认保留当前网络配置";
        button.disabled = seconds === 0;
        button.addEventListener("click", async () => {
          button.disabled = true;
          try {
            await api("/api/v1/apply/confirm", {method: "POST", body: {id: current.id}});
            feedback("当前网络配置已确认保留。", "good");
            state.pending = state.configCache?.revision !== current.revision;
            syncActions();
          } catch (error) { feedback(`确认未完成：${error.message}；请重新查询候选状态。`, "error"); }
          await refreshCandidate();
        });
        panel.append(button);
      } else {
        const labels = {confirmed:"配置已确认保留。", "rolled-back":"未确认的候选已恢复为上一份确认配置；保存的草稿仍保留。", "rollback-failed":"恢复失败，请使用独立控制台处理；已禁止继续应用。", activating:"候选配置正在应用。", confirming:"正在持久化确认。", "rolling-back":"正在恢复上一份确认配置。"};
        panel.textContent = labels[current.state] || "候选状态未知，请核查设备。";
      }
    } catch (error) {
      // Loss of observation never means confirmed; retain the last warning.
      if (candidateStatus) { panel.classList.remove("hidden"); panel.textContent = "暂时无法查询候选状态；请连接新 LAN 地址或获准管理路径后刷新。未确认配置仍可能超时恢复。"; }
    }
    clearTimeout(candidatePoll);
    if (state.loggedIn && candidateStatus && !["confirmed", "rolled-back"].includes(candidateStatus.state)) candidatePoll = setTimeout(refreshCandidate, 3000);
  }
  function resultMessage(result) {
    const notes = [
      ...(Array.isArray(result.warnings) ? result.warnings : []),
      ...(Array.isArray(result.notes) ? result.notes : []),
    ].join("；");
    if (result.error || result.status === "failed")
      return `应用失败：${result.message || result.error || "未知错误"}${notes ? "；" + notes : ""}`;
    if (result.confirmation?.state === "pending") return "候选网络配置已应用，尚未确认；请验证连接后使用上方按钮确认，否则自动恢复。";
    if (result.appliedRuntime === true)
      return `${result.mode === "managed-runtime" ? "网络配置应用完成（SSH 与系统设置仍由 NixOS 管理）" : "运行配置应用完成"}${notes ? "；提示：" + notes : ""}`;
    if (result.mode === "generate-only" && result.appliedRuntime === false)
      return `配置生成完成，未应用运行态${notes ? "；提示：" + notes : ""}`;
    return `作业尚未确认完成（${result.jobId || "编号未提供"}）；如 LAN 地址已改变，请连接到新地址；恢复网络后重新连接并在系统操作记录中核查。`;
  }
  async function apply() {
    if (state.busy) return;
    if (state.pageDirty && !(await save())) {
      feedback(
        `保存未完成，已取消应用。${$("#apply-msg").textContent || "请检查表单字段。"}`,
        "error",
      );
      return;
    }
    state.busy = true;
    syncActions();
    feedback("正在处理已保存的配置…");
    let submittedJob = null;
    try {
      let result = await api("/api/v1/apply", {
        method: "POST",
        timeout: 60000,
      });
      if (result.status === "queued" || result.status === "running") {
        submittedJob = result.jobId;
        sessionStorage.setItem("router-apply-job", submittedJob);
        for (let attempt = 0; attempt < 30; attempt++) {
          await new Promise((r) => setTimeout(r, 1000));
          let job;
          try {
            job = await api(
              `/api/v1/jobs/${encodeURIComponent(submittedJob)}`,
              { timeout: 5000 },
            );
          } catch (e) {
            if (
              e instanceof TypeError || e.name === "AbortError" ||
              [502, 503, 504].includes(e.status)
            ) {
              feedback(`连接暂时中断，正在重新查询作业 ${submittedJob}…`);
              continue;
            }
            throw e;
          }
          const item = job.job || job;
          if (item.status === "failed" || item.status === "success") {
            let payload = {};
            try {
              payload = JSON.parse(item.payload || "{}");
            } catch {}
            result = {
              ...result,
              ...item,
              ...payload,
              appliedRuntime:
                item.status === "success" && payload.mode === "generate-only"
                  ? false
                  : (payload.appliedRuntime ?? result.appliedRuntime),
            };
            break;
          }
        }
      }
      const failed = !!result.error || result.status === "failed";
      feedback(
        resultMessage(result),
        failed
          ? "error"
          : result.appliedRuntime === true ||
              (result.mode === "generate-only" &&
                result.appliedRuntime === false)
            ? "good"
            : "",
      );
      if (failed) sessionStorage.removeItem("router-apply-job");
      if (
        !failed &&
        (result.appliedRuntime === true ||
          (result.mode === "generate-only" && result.appliedRuntime === false))
      ) {
        state.pending = false;
        sessionStorage.removeItem("router-apply-job");
      }
    } catch (e) {
      feedback(
        submittedJob
          ? `${resultMessage({ jobId: submittedJob })}（查询中断：${e.message}）`
          : `应用失败：${e.message}`,
        submittedJob ? "" : "error",
      );
    } finally {
      state.busy = false;
      syncActions();
      await refreshCandidate();
    }
  }
  const unavailable = (text) =>
    notice(text || "当前环境未提供运行数据；以下为配置值，不代表设备已生效。");
  function interfaceStatus(status, role) {
    const item = status?.interfaces?.find((v) => v.role === role);
    return kv("运行信息", [
      ["接口", item?.name || "未提供"],
      [
        "链路",
        item?.source === "sysfs" && typeof item.up === "boolean"
          ? item.up
            ? "已连接"
            : "未连接"
          : "未采集",
      ],
      [
        "数据说明",
        item?.source === "sysfs"
          ? "来自系统接口；链路连接不等于互联网可达。"
          : "接口未被采集，名称来自配置。",
      ],
    ]);
  }
  function wifiCapabilitiesNotice() {
    const caps = state.wifiCaps;
    if (!caps?.available || caps.source !== "iw") {
      return unavailable(`无线硬件能力未确认；不会把预览值当作硬件检测结果。${caps?.reason ? "检测说明：" + caps.reason : "请检查设备和 iw 工具后刷新。"}`);
    }
    const usable = (caps.channels || []).filter((c) => !c.disabled && !c.noIR && !c.radar && !c.restricted).length;
    return notice(`来源：iw · 接口 ${caps.interface} · ${caps.phy}。硬件报告频段：${(caps.bands || []).join("、")}；AP 模式：${caps.apSupported ? "支持" : "不支持"}；当前未标记禁用或禁止发射的信道：${usable} 个。管理配置最多 ${caps.managedMaxAP} 个接入点（不是硬件上限）。首版 5 GHz 仅支持 36/40/44/48/149/153/157/161/165 非 DFS 信道；仍需确认监管域、驱动和实际关联结果。`);
  }
  function coreEditor(id, cfg, status) {
    const d = beginEditor(cfg, id),
      fields = (...defs) => defs.map((args) => field(d, ...args));
    if (id === "wan") {
      d.static ||= {};
      d.pppoe ||= {};
      const staticFields = node(
        "div",
        { class: "form-grid" },
        fields(
          [
            "static.addressCidr",
            "静态地址 / CIDR",
            { placeholder: "203.0.113.10/24", required: true },
          ],
          ["static.gateway", "网关"],
          [
            "static.dns",
            "DNS 服务器",
            {
              type: "textarea",
              list: true,
              help: "每行或逗号分隔一个 IP 地址。",
              full: true,
            },
          ],
        ),
      );
      const pppFields = node(
        "div",
        { class: "form-grid" },
        fields(
          ["pppoe.username", "PPPoE 用户名", { required: true }],
          [
            "pppoe.password",
            "PPPoE 密码",
            { type: "password", required: !d.pppoe.password },
          ],
        ),
      );
      const refresh = () => {
        for (const [group, mode] of [
          [staticFields, "static"],
          [pppFields, "pppoe"],
        ]) {
          group.hidden = d.mode !== mode;
          group
            .querySelectorAll("input,textarea")
            .forEach((e) => (e.disabled = d.mode !== mode));
        }
      };
      const main = formCard(
        "连接设置",
        fields(
          [
            "mode",
            "上网方式",
            {
              choices: [
                ["dhcp", "自动获取（DHCP）"],
                ["static", "静态地址"],
                ["pppoe", "宽带拨号（PPPoE）"],
              ],
              change: refresh,
            },
          ],
          [
            "interface",
            "外网接口",
            { placeholder: "例如 eth0", help: "填写目标路由器实际网口名称。" },
          ],
        ),
      );
      const details = card("连接参数", staticFields, pppFields);
      const refreshAll = () => {
        refresh();
        details.hidden = d.mode === "dhcp";
      };
      main.querySelector("select").addEventListener("change", refreshAll);
      refreshAll();
      return editor(main, details, interfaceStatus(status, "wan"));
    }
    if (id === "lan") {
      d.dhcp ||= {};
      d.ports ||= [];
      d.staticLeases ||= [];
      return editor(
        notice(
          "更改内网地址并应用后，需使用新地址重新连接。本页面不会自动回退旧地址。",
          "warning",
        ),
        formCard(
          "内网与地址分配",
          fields(
            ["bridgeName", "网桥名称", { required: true }],
            [
              "ipv4Cidr",
              "路由器地址 / CIDR",
              { required: true, placeholder: "192.168.1.1/24" },
            ],
            ["dhcp.enable", "启用 DHCP", { type: "checkbox" }],
            ["dhcp.leaseMins", "租约时间（分钟）", { type: "number", min: 1 }],
            ["dhcp.rangeStart", "地址池起始地址"],
            ["dhcp.rangeEnd", "地址池结束地址"],
          ),
        ),
        stringList(d, "ports", "网桥端口", "物理接口", "例如 eth1"),
        collection(
          "静态租约",
          d.staticLeases,
          (item) => [
            field(item, "hostname", "设备名称"),
            field(item, "ip", "IP 地址", { required: true }),
            field(item, "mac", "MAC 地址", { required: true }),
            field(item, "comment", "备注"),
          ],
          () => ({ hostname: "", ip: "", mac: "", comment: "" }),
        ),
        interfaceStatus(status, "lan"),
      );
    }
    if (id === "dns")
      return editor(
        formCard(
          "DNS 设置",
          fields(
            ["enableDnsmasq", "启用本地 DNS 服务", { type: "checkbox" }],
            ["domain", "本地域名", { placeholder: "lan" }],
          ),
        ),
        stringList(d, "upstreams", "上游 DNS", "服务器 IP", "1.1.1.1"),
      );
    if (id === "wifi") {
      d.aps ||= [];
      d.network ||= { ipv4Cidr: "", dhcp: { enable: true, rangeStart: "", rangeEnd: "", leaseMins: 1440 }, allowManagement: false, allowWiredAccess: false };
      d.network.dhcp ||= {};
      d.network.staticLeases ||= [];
      const refreshNetwork = () => {
        const independent = !d.bridgeToLan;
        for (const panel of [networkCard, networkLeases]) {
          panel.hidden = !independent;
          panel.querySelectorAll("input,select,textarea,button").forEach(input => {
            const path = input.dataset.configPath || "";
            const dhcpParameter = path.startsWith("wifi.network.dhcp.") && !path.endsWith(".enable");
            input.disabled = !independent || (dhcpParameter && !d.network.dhcp.enable);
          });
        }
        for (const [path, required] of [
          ["wifi.network.ipv4Cidr", independent && d.enable],
          ["wifi.network.dhcp.rangeStart", independent && d.enable && d.network.dhcp.enable],
          ["wifi.network.dhcp.rangeEnd", independent && d.enable && d.network.dhcp.enable],
        ]) networkCard.querySelector(`[data-config-path="${path}"]`).required = !!required;
      };
      const networkCard = formCard("独立无线网段", fields(
        ["network.ipv4Cidr", "无线网关 / CIDR", {help:"使用与有线内网及外网不重叠的独立网段；地址由部署方配置。"}],
        ["network.dhcp.enable", "启用无线 DHCP", {type:"checkbox",change:refreshNetwork}],
        ["network.dhcp.rangeStart", "无线地址池起始地址"],
        ["network.dhcp.rangeEnd", "无线地址池结束地址"],
        ["network.dhcp.leaseMins", "无线租约时间（分钟）", {type:"number",min:1}],
        ["network.allowManagement", "允许无线管理路由器", {type:"checkbox",help:"允许此网段访问 Web 管理和宿主 SSH 端口。关闭后仍可使用 DHCP、DNS 和网关连通性检查。"}],
        ["network.allowWiredAccess", "允许有线与无线互访", {type:"checkbox",help:"显式允许两个内网双向发起连接；关闭后仅保留各自到外网的访问。"}],
      ));
      const networkLeases = collection("无线静态租约", d.network.staticLeases, lease => [
        field(lease,"hostname","无线设备名称"), field(lease,"ip","无线租约 IP"),
        field(lease,"mac","无线租约 MAC"), field(lease,"comment","无线租约备注"),
      ], () => ({hostname:"",ip:"",mac:"",comment:""}));
      const originalNames = new WeakMap(d.aps.map((ap) => [ap, ap.ssid]));
      const channelStatus = state.wifiChannel;
      const stations = state.wifiStations;
      const channelReasons = {
        "last legal channel": "沿用上次仍合法的信道",
        "deterministic legal fallback": "按固定顺序选择合法信道",
        "survey and neighbor interference": "根据忙碌度与邻居干扰选择",
        "fixed configuration": "手动固定配置",
        "current channel remains preferred": "当前信道仍较合适",
        "waiting for sustained congestion": "等待确认持续拥塞",
        "channel change cooling down": "换台冷却中",
        "associated clients present; change deferred": "存在关联客户端，已延后换台",
        "scan unavailable; kept current channel": "扫描不可用，保持当前信道",
        "observed channel differs from last selection": "实际信道与上次选择不同",
      };
      const channelReason = channelStatus?.reason?.startsWith("sustained congestion;") ? "持续拥塞，已选择更合适的信道" : channelReasons[channelStatus?.reason] || channelStatus?.reason || "未提供";
      const live = card("无线运行状态",
        channelStatus?.available ? notice(`模式：${channelStatus.mode === "auto" ? "自动" : "固定"} · 当前信道：${channelStatus.channel || "未知"} · 原因：${channelReason}`) : unavailable(channelStatus?.reason || "当前信道无法查询"),
        btn("立即重新评估", async () => {
          try { await api("/api/v1/wifi/reevaluate", { method: "POST", timeout: 45000 }); await renderRoute(); }
          catch (e) { alert(e.message || "重新评估失败"); }
        }),
        stations?.available ? (stations.stations?.length ? table(["MAC", "IP", "主机名", "信号", "连接时长"], stations.stations.map(s => [s.mac, s.ip || "—", s.hostname || "—", s.signalDBm ? `${s.signalDBm} dBm` : "—", `${s.connectedSeconds || 0} 秒`]), "真实关联客户端") : notice("当前没有关联客户端")) : unavailable(stations?.reason || "关联客户端查询失败"),
      );
      const form = editor(
        wifiCapabilitiesNotice(),
        live,
        formCard(
          "无线设置",
          fields(
            ["enable", "启用 WiFi", { type: "checkbox", change: refreshNetwork }],
            ["bridgeToLan", "桥接到内网", { type: "checkbox", change: refreshNetwork }],
          ),
        ),
        networkCard,
        networkLeases,
        collection(
          "接入点",
          d.aps,
          (ap) => {
            ap.channelMode ||= ap.channel ? "fixed" : "auto";
            const fixedChannel = field(ap, "channel", "固定信道", { type: "number", help: "固定模式只允许监管域和硬件支持的信道。" });
            const fixedInput = fixedChannel.querySelector("input");
            fixedInput.disabled = ap.channelMode !== "fixed";
            const channelMode = field(ap, "channelMode", "信道模式", { choices: [["auto", "自动"], ["fixed", "固定"]], change: () => {
              fixedInput.disabled = ap.channelMode !== "fixed";
              if (ap.channelMode === "auto") { ap.channel = 0; fixedInput.value = "0"; }
            } });
            const originalSSID = originalNames.get(ap);
            const password = field(ap, "psk", "无线密码", {
              type: "password",
              required:
                !ap.psk || (ap.psk === "****" && ap.ssid !== originalSSID),
              minlength: 8,
              help: "留空保留当前密码；修改无线名称后需要重新输入密码。",
            });
            return [
              field(ap, "ssid", "无线名称（SSID）", {
                required: true,
                change: () => {
                  password.querySelector("input").required =
                    !ap.psk || (ap.psk === "****" && ap.ssid !== originalSSID);
                },
              }),
              field(ap, "band", "频段", {
                choices: [
                  ["2g", "2.4 GHz"],
                  ["5g", "5 GHz"],
                  ["6g", "6 GHz"],
                ],
              }),
              field(ap, "standard", "无线标准", {
                choices: [["", "兼容现有配置"], ["wifi4", "WiFi 4（HT）"], ["wifi5", "WiFi 5（VHT）"], ["wifi6", "WiFi 6（HE）"], ["wifi7", "WiFi 7（EHT）"]],
                help: "应用前会核对对应频段的 AP 能力；不支持时明确拒绝，不会自动降级。",
              }),
              field(ap, "channelWidthMHz", "信道带宽", {
                type: "number",
                choices: [[0, "默认（20 MHz）"], [-1, "自动"], [20, "20 MHz"], [40, "40 MHz"], [80, "80 MHz"]],
                help: "自动选择实时可用的非 DFS 20、40 或 80 MHz 带宽；全部子信道均须允许发射。",
              }),
              node("details", {}, node("summary", {}, "高级信道设置"),
                channelMode, fixedChannel,
              ),
              field(ap, "security", "安全模式", {
                choices: [
                  ["", "WPA2-PSK（兼容现有配置）"],
                  ["wpa2-psk", "WPA2-PSK"],
                  ["wpa3-sae", "WPA3-SAE"],
                ],
                help: "WPA3-SAE 要求客户端支持受保护管理帧，不会自动降级为 WPA2。",
              }),
              password,
              field(ap, "enable", "启用此接入点", { type: "checkbox" }),
              field(ap, "guest", "访客网络", {
                type: "checkbox",
                help: "配置标记不等于已验证访客隔离。",
              }),
              field(ap, "isolate", "隔离无线客户端", { type: "checkbox" }),
            ];
          },
          () => ({
            ssid: "",
            security: "wpa2-psk",
            standard: "",
            channelWidthMHz: 0,
            band: "2g",
            channel: 0,
            channelMode: "auto",
            psk: "",
            enable: true,
            guest: false,
            isolate: false,
          }),
        ),
      );
      refreshNetwork();
      return form;
    }
    if (id === "firewall") {
      d.portForwards ||= [];
      return editor(
        formCard(
          "访问策略",
          fields(
            ["enable", "启用防火墙", { type: "checkbox" }],
            ["natEnabled", "启用 NAT", { type: "checkbox" }],
            [
              "upnpEnable",
              "启用 UPnP",
              {
                type: "checkbox",
                help: "需在目标环境部署 UPnP 服务；保存开关不代表服务已启动。",
              },
            ],
          ),
        ),
        collection(
          "端口转发",
          d.portForwards,
          (p) => [
            field(p, "description", "规则说明"),
            field(p, "protocol", "协议", {
              choices: [
                ["tcp", "TCP"],
                ["udp", "UDP"],
              ],
            }),
            field(p, "externalPort", "外部端口", {
              type: "number",
              min: 1,
              max: 65535,
              required: true,
            }),
            field(p, "externalPortEnd", "外部结束端口", {
              type: "number",
              max: 65535,
              help: "0 表示单个端口。",
            }),
            field(p, "destIp", "目标 IP", { required: true }),
            field(p, "destPort", "目标端口", {
              type: "number",
              min: 1,
              max: 65535,
              required: true,
            }),
          ],
          () => ({
            description: "",
            protocol: "tcp",
            externalPort: 0,
            externalPortEnd: 0,
            destIp: "",
            destPort: 0,
          }),
        ),
        stringList(d, "blockedMacs", "阻止的设备", "MAC 地址"),
      );
    }
    if (id === "qos") {
      d.devicePolicy ||= [];
      return editor(
        notice(
          "总带宽使用 Mbps，设备限速使用 Kbps。实际限速需要目标设备启用流量控制。",
        ),
        formCard(
          "总带宽",
          fields(
            ["enable", "启用带宽管理", { type: "checkbox" }],
            ["upMbps", "上行带宽（Mbps）", { type: "number" }],
            ["downMbps", "下行带宽（Mbps）", { type: "number" }],
          ),
        ),
        collection(
          "设备策略",
          d.devicePolicy,
          (p) => [
            field(p, "mac", "MAC 地址"),
            field(p, "ip", "IP 地址", { help: "MAC 和 IP 至少填写一个。" }),
            field(p, "priority", "优先级", {
              choices: [
                ["low", "低"],
                ["normal", "普通"],
                ["high", "高"],
              ],
            }),
            field(p, "limitKbps", "限速（Kbps）", {
              type: "number",
              help: "0 表示不设置设备限速。",
            }),
          ],
          () => ({ mac: "", ip: "", priority: "normal", limitKbps: 0 }),
        ),
      );
    }
    if (id === "parental") {
      d.rules ||= [];
      return editor(
        notice(
          "当前可保存规则，定时执行引擎尚未完成。不会将保存成功显示为已限制设备上网。",
        ),
        formCard(
          "家长控制",
          fields(["enable", "启用家长控制", { type: "checkbox" }]),
        ),
        collection(
          "访问规则",
          d.rules,
          (p) => [
            field(p, "group", "分组名称"),
            field(p, "action", "动作", {
              choices: [
                ["block", "禁止访问"],
                ["allow", "允许访问"],
              ],
            }),
            field(p, "targetMacs", "设备 MAC 地址", {
              type: "textarea",
              list: true,
              help: "每行一个设备地址。",
            }),
            field(p, "schedule", "生效时间段", {
              placeholder: "Mon-Fri 22:00-07:00",
              help: "保留完整时间规则；实际执行仍需定时引擎支持。",
            }),
          ],
          () => ({ group: "", action: "block", targetMacs: [], schedule: "" }),
        ),
      );
    }
    if (id === "ipv6")
      return editor(
        notice(
          "保存 IPv6 意图；地址获取、前缀分配及实际转发仍需目标设备支持。",
        ),
        formCard(
          "IPv6 设置",
          fields(
            ["enable", "启用 IPv6", { type: "checkbox" }],
            [
              "wanMode",
              "外网获取方式",
              {
                choices: [
                  ["disabled", "禁用"],
                  ["dhcpv6", "DHCPv6"],
                  ["slaac", "SLAAC"],
                  ["pppoe6", "PPPoE IPv6"],
                ],
              },
            ],
            [
              "lanPrefixDelegation",
              "为内网分配前缀（PD）",
              { type: "checkbox" },
            ],
            [
              "lanRouterAdvertisement",
              "向内网发送路由通告（RA）",
              { type: "checkbox" },
            ],
          ),
        ),
      );
    if (id === "ddns") {
      const providerFields = node("div");
      const specs = {
        cloudflare: [
          ["apiToken", "API Token", "password"],
          ["zone", "Zone"],
          ["record", "记录名称"],
        ],
        duckdns: [
          ["token", "Token", "password"],
          ["domain", "域名"],
        ],
        aliyun: [
          ["accessKey", "Access Key", "password"],
          ["secretKey", "Secret Key", "password"],
          ["domain", "域名"],
          ["record", "记录名称"],
        ],
        custom: [
          ["url", "更新 URL", "url"],
          ["token", "Token", "password"],
        ],
      };
      const draw = () => {
        const p = d.provider || "cloudflare";
        d[p] ||= {};
        providerFields.replaceChildren(
          formCard(
            "服务商参数",
            specs[p].map(([key, label, type]) =>
              field(d[p], key, label, {
                type: type || "text",
                required:
                  d.enable &&
                  (type !== "password" || !d[p][key]) &&
                  !(p === "custom" && key === "token"),
              }),
            ),
          ),
        );
      };
      if (!specs[d.provider]) d.provider = "cloudflare";
      const basic = formCard(
        "动态 DNS",
        fields(
          ["enable", "启用动态 DNS", { type: "checkbox", change: draw }],
          [
            "provider",
            "服务提供商",
            {
              choices: [
                ["cloudflare", "Cloudflare"],
                ["duckdns", "DuckDNS"],
                ["aliyun", "阿里云"],
                ["custom", "自定义 URL"],
              ],
              change: draw,
            },
          ],
        ),
      );
      draw();
      return editor(
        notice(
          "提供商配置可保存；周期更新服务尚未完成，请勿将保存结果当作 DNS 记录已更新。",
        ),
        basic,
        providerFields,
      );
    }
    return editor(
      formCard(
        "基本配置",
        fields(
          ["hostname", "主机名", { required: true }],
          [
            "timezone",
            "时区",
            {
              placeholder: "Asia/Shanghai",
              help: "保存后仍需应用；此值不证明目标系统时区已改变。",
            },
          ],
        ),
      ),
    );
  }
  function moduleSupport(id) {
    return state.modules?.find(m => m.id === id);
  }
  function moduleDescription(m) {
    if (!m) return "模块能力暂不可用，请刷新后重试。";
    if (m.ownership === "system") return "系统管理：请通过 NixOS 配置此服务。";
    if (!m.available) return "当前部署不支持此模块；历史配置可保留或导出。";
    const observed = {"not-applied":"尚未应用运行态",unknown:"运行状态未知",disabled:"已应用停用", "service-active":"已应用，DNS 服务运行中"}[m.status] || "运行状态未知";
    return `${m.runtimeSupported ? "支持运行应用" : "仅支持生成文件"}；${observed}。`;
  }
  function moduleToggle(id, data) {
    const control=field(data,"enable",`启用 ${moduleSupport(id)?.title || pages[id]?.[0] || id}`,{type:"checkbox"});
    const input=control.querySelector("input");
    // Allow an existing unsupported enabled draft to be explicitly disabled.
    if (!moduleSupport(id)?.available && !data.enable) input.disabled=true;
    return control;
  }
  function pluginsEditor(cfg) {
    const d = beginEditor(cfg, "plugins");
    const ids = [...new Set([...(state.modules || []).map(m=>m.id), ...Object.keys(d)])];
    return editor(
      notice("插件能力由当前部署声明。保存、生成与实际应用状态分别展示。"),
      stack(ids.map(id => {
        d[id] ||= {enable:false,config:{}};
        return card(moduleSupport(id)?.title || id,
          moduleToggle(id,d[id]),
          node("p",{class:"help"},moduleDescription(moduleSupport(id))),
          link(`plugins/${encodeURIComponent(id)}`,"配置详情"));
      })),
      card("基础服务",node("p",{},"IPv6 等核心网络功能保持独立配置，不受插件开关影响。"))
    );
  }
  function pluginEditor(id, cfg) {
    const d = beginEditor(cfg, ["plugins", id]);
    d.config ||= {};
    const c = d.config,
      fields = (...defs) => defs.map((args) => field(c, ...args));
    const parts = [
      notice(
        moduleDescription(moduleSupport(id)),
      ),
      formCard("插件开关", [
        moduleToggle(id, d),
      ]),
    ];
    if (id === "adblock")
      parts.push(
        notice("仅支持域名规则，同时拦截域名及子域的 IPv4/IPv6 解析；订阅下载暂不支持。依赖本地 DNS 服务。"),
        stringList(c, "entries", "拦截域名", "每行一个域名", "ads.example.com"),
        c.lists?.length ? notice("检测到历史订阅来源；启用前请先清空订阅。订阅不会被下载。") : null,
        c.lists?.length ? node("button",{type:"button",onclick:()=>{c.lists=[];dirty();}},"清空历史订阅") : null,
      );
    else if (id === "traffic")
      parts.push(
        unavailable("流量采集器尚未接入，因此不展示模拟曲线或虚构流量。"),
        formCard(
          "保留策略",
          fields([
            "retentionDays",
            "保留天数",
            { type: "number", min: 1, default: 7 },
          ]),
        ),
      );
    else if (id === "mihomo")
      parts.push(
        formCard(
          "代理配置",
          fields(
            ["profile", "配置文件 / Profile"],
            [
              "mode",
              "工作模式",
              {
                choices: [
                  ["rule", "规则"],
                  ["global", "全局"],
                  ["direct", "直连"],
                ],
              },
            ],
            [
              "dns.port",
              "DNS 监听端口",
              { type: "number", min: 1, max: 65535, default: 1053 },
            ],
            ["tun.enable", "启用 TUN", { type: "checkbox" }],
          ),
        ),
      );
    else if (id === "vlan") {
      c.vlans ||= [];
      parts.push(
        unavailable("当前仅生成 VLAN 定义说明，尚未创建实际 VLAN 接口。"),
        collection(
          "VLAN 定义",
          c.vlans,
          (v) => [
            field(v, "vid", "VLAN ID", {
              type: "number",
              min: 1,
              max: 4094,
              required: true,
            }),
            field(v, "name", "接口名称", { placeholder: "vlan10" }),
            field(v, "bridge", "关联网桥", { placeholder: "br-lan" }),
          ],
          () => ({ vid: 10, name: "", bridge: "" }),
        ),
      );
    } else if (id === "tailscale")
      parts.push(
        formCard(
          "接入配置",
          fields(
            [
              "controlPlane",
              "控制平面",
              {
                choices: [
                  ["official", "Tailscale 官方"],
                  ["headscale", "Headscale"],
                ],
              },
            ],
            [
              "loginServer",
              "登录服务器",
              {
                type: "url",
                placeholder: "https://headscale.example.com",
                help: "使用 Headscale 时填写其完整地址。",
              },
            ],
            ["authKey", "认证密钥", { type: "password" }],
          ),
        ),
      );
    else if (id === "zerotier")
      parts.push(
        formCard(
          "控制器",
          fields(
            [
              "controlPlane",
              "控制平面",
              {
                choices: [
                  ["public", "公共控制器"],
                  ["private", "私有控制器"],
                ],
              },
            ],
            ["controllerUrl", "控制器 URL", { type: "url" }],
            ["apiToken", "API Token", { type: "password" }],
          ),
        ),
        stringList(c, "networks", "加入网络", "网络 ID"),
      );
    else {
      const input = node("textarea", { id: "plugin-advanced", rows: 12 });
      // References belong to secrets, not array positions. Reordering a JSON
      // array must not move a different item's credential into that position.
      const secretRefs = new Map();
      input.value = JSON.stringify(
        c,
        (key, value) => {
          if (
            /password|passwd|psk|secret|token|authkey|privatekey|accesskey|apikey/i.test(
              key,
            ) &&
            value
          ) {
            const ref = `__保留密钥_${secretRefs.size + 1 + "_" + Math.random().toString(36).slice(2)}__`;
            secretRefs.set(ref, value);
            return ref;
          }
          return value;
        },
        2,
      );
      const section = state.section;
      const clearOptions = node("div", { class: "form-row" });
      const references = (value, path = [], found = []) => {
        if (!value || typeof value !== "object") return found;
        for (const [key, item] of Object.entries(value)) {
          if (typeof item === "string" && item.startsWith("__router_secret_ref_v1_"))
            found.push({ owner: value, key, ref: item, path: [...path, key] });
          else references(item, [...path, key], found);
        }
        return found;
      };
      const drawClearOptions = () => {
        clearOptions.replaceChildren(...references(d.config).map(({ owner, key, path }) =>
          secretClearChoice(section, owner, key, `清空密钥：${path.join(".")}`).element));
      };
      drawClearOptions();
      input.addEventListener("input", () => {
        dirty();
        try {
          const selected = new Set(references(d.config)
            .filter(({ owner, key }) => section.secretClears.get(owner)?.has(key))
            .map(({ ref }) => ref));
          const value = JSON.parse(input.value, (_, item) =>
            typeof item === "string" && secretRefs.has(item)
              ? clone(secretRefs.get(item))
              : item,
          );
          if (!value || Array.isArray(value) || typeof value !== "object")
            throw new Error("参数必须是 JSON 对象");
          const next = references(value);
          for (const ref of selected) {
            if (next.filter((item) => item.ref === ref).length > 1)
              throw new Error("待清空密钥的标记被复制，请撤销清空或删除重复标记");
          }
          section.secretClears.clear();
          for (const { owner, key, ref } of next)
            if (selected.has(ref)) setSecretClear(section, owner, key, true);
          d.config = value;
          drawClearOptions();
          input.setCustomValidity("");
        } catch (e) {
          input.setCustomValidity(`参数格式错误：${e.message}`);
        }
      });
      parts.push(
        card(
          "扩展参数",
          notice(
            "此扩展尚无专用表单，请按插件定义编辑 JSON 对象。保留密钥标记会还原对应密钥，移动数组项时请保留该项标记。",
          ),
          node(
            "div",
            { class: "form-row" },
            node("label", { for: input.id }, "高级配置（JSON）"),
            input,
          ),
          clearOptions,
        ),
      );
    }
    return editor(parts);
  }
  function replaceIfChanged(container, content) {
    if (!container.contains(document.activeElement) &&
        !container.firstElementChild?.isEqualNode(content))
      container.replaceChildren(content);
  }

  function liveNetwork(wanConfig) {
    const period = node("select", { id: "traffic-period" },
      node("option", { value: 60 }, "最近 60 秒"),
      node("option", { value: 300 }, "最近 5 分钟"));
    const sampleNote = node("p", { class: "help" }, "约每 2 秒采样");
    const ports = node("div", { class: "traffic-ports" },
      node("p", { class: "help" }, "物理网口未采集"));
    let paused = false, lastTime = performance.now(), sampleGeneration = 0;
    const pause = btn("暂停", () => {
      paused = !paused;
      pause.textContent = paused ? "继续" : "暂停";
      pause.setAttribute("aria-pressed", String(paused));
      sampleNote.textContent = paused ? "已暂停" : "约每 2 秒采样";
      breakSample();
    }, "quiet");
    pause.setAttribute("aria-pressed", "false");

    function panel(title, rxLabel, txLabel, unit = "Mbps") {
      const name = node("span", { class: "traffic-interface" });
      const rx = node("strong", {}, "未采集"), tx = node("strong", {}, "未采集");
      const chart = node("canvas", { class: "traffic-chart", role: "img",
        "aria-label": title + " " + (txLabel ? rxLabel + "与" + txLabel : rxLabel) + "趋势",
        width: 600, height: 150 });
      const ago = node("span", {}, "60 秒前");
      const scale = node("span", {}, "");
      const detail = node("p", { class: "help" });
      const element = card(title,
        name,
        node("div", { class: "traffic-rates" },
          node("span", { class: "traffic-rx" }, rxLabel, " ", rx),
          txLabel ? node("span", { class: "traffic-tx" }, txLabel, " ", tx) : null),
        chart,
        node("div", { class: "traffic-axis help" }, ago, scale, node("span", {}, "现在")),
        detail);
      return { element, name, rx, tx, chart, ago, scale, detail, unit, points: [], previous: null };
    }
    const wan = panel("外网 WAN", "下载", "上传");
    const lan = panel("内网 LAN", "接收", "发送");
    const wifi = panel("独立 Wi‑Fi 网段", "接收", "发送");
    wifi.element.hidden = true;
    wifi.detail.textContent = "以路由器为方向基准，统计独立无线接口流量。";
    const cpu = panel("CPU", "使用率", null, "%");
    const memory = panel("内存", "使用量", null, "%");
    cpu.detail.textContent = "主机 CPU 总使用率";
    memory.detail.textContent = "趋势显示已使用内存占比";
    const panels = [wan, lan, wifi, cpu, memory];
    let previousSnapshot, timeOffset, gap = true;
    function breakSample() {
      gap = true;
      sampleGeneration++;
      for (const panel of panels) panel.previous = null;
    }
    updateWANDetail(wanConfig);
    lan.detail.textContent = "以路由器为方向基准，仅统计 LAN 桥接口。桥内端口间转发不在统计范围内。";

    function draw(panel) {
      const { chart, points, ago, scale } = panel;
      const ctx = chart.getContext("2d");
      if (!ctx) return;
      const seconds = Number(period.value);
      const from = lastTime - seconds * 1000;
      ago.textContent = seconds === 60 ? "60 秒前" : "5 分钟前";
      const visible = points.filter(p => p.time >= from);
      const { width, height } = chart;
      const max = panel.unit === "%" ? 100 :
        Math.ceil(Math.max(1, ...visible.flatMap(p => [p.rx ?? 0, p.tx ?? 0])));
      ctx.clearRect(0, 0, width, height);
      const hasRates = visible.some(p => p.rx !== null || p.tx !== null);
      scale.textContent = hasRates ? "0–" + max + (panel.unit === "%" ? "%" : " Mbps") : "";
      if (!hasRates) return;
      ctx.strokeStyle = "rgba(150, 165, 185, 0.25)";
      ctx.lineWidth = 1;
      ctx.beginPath();
      for (const y of [4, height / 2, height - 4]) {
        ctx.moveTo(0, y);
        ctx.lineTo(width, y);
      }
      ctx.stroke();
      for (const [key, color] of [["rx", "#38bdf8"], ["tx", "#34d399"]]) {
        ctx.strokeStyle = ctx.fillStyle = color;
        ctx.lineWidth = 2;
        let previous = null;
        for (const point of visible) {
          if (point.breakBefore) previous = null;
          if (point[key] === null) {
            previous = null;
            continue;
          }
          const x = 3 + (point.time - from) / (seconds * 1000) * (width - 6);
          const y = height - 4 - point[key] / max * (height - 8);
          if (previous) {
            ctx.beginPath();
            ctx.moveTo(previous.x, previous.y);
            ctx.lineTo(x, y);
            ctx.stroke();
          }
          ctx.fillRect(x - 1, y - 1, 2, 2);
          previous = { x, y };
        }
      }
    }
    period.addEventListener("change", () => panels.forEach(draw));
    const counter = value => typeof value === "string" && /^\d+$/.test(value) ? BigInt(value) : null;
    const format = n => n === 0 ? "0 Mbps" : n < 0.01 ? "<0.01 Mbps" : n.toFixed(2) + " Mbps";
    function appendPoint(panel, point) {
      panel.points.push(point);
      panel.points = panel.points.filter(p => lastTime - p.time <= 300000);
      draw(panel);
    }
    function updatePanel(panel, sample, data, pointTime) {
      if (sample?.name && sample.name !== panel.interfaceName) {
        panel.points = [];
        panel.interfaceName = sample?.name;
      }
      if (sample) panel.name.textContent = sample.name || "接口未采集";
      const current = sample?.identity ? {
        identity: sample.identity, name: sample.name, clock: data.clock,
        ms: sample.sampleMs, rx: counter(sample.rxBytes), tx: counter(sample.txBytes),
      } : null;
      const prev = panel.previous;
      panel.previous = current;
      const elapsed = current && prev ? current.ms - prev.ms : 0;
      const continuous = elapsed > 0 && elapsed <= 6000 && current.clock === prev.clock &&
        current.identity === prev.identity && current.name === prev.name &&
        !["rx", "tx"].some(key => current[key] !== null && prev[key] !== null && current[key] < prev[key]);
      const measuredTime = current && Number.isFinite(timeOffset)
        ? current.ms + timeOffset : pointTime;
      const point = { time: measuredTime, rx: null, tx: null, breakBefore: gap };
      for (const key of ["rx", "tx"]) {
        if (continuous && current[key] !== null && prev[key] !== null)
          point[key] = Number(current[key] - prev[key]) * 0.008 / elapsed;
        panel[key].textContent = point[key] === null
          ? (sample?.link === "down" ? "未连接" : "未采集")
          : format(point[key]);
      }
      appendPoint(panel, point);
    }
    function updateWANDetail(configured, observedPorts) {
      const contract = [];
      if (configured?.contractDownMbps > 0) contract.push("下载 " + configured?.contractDownMbps + " Mbps");
      if (configured?.contractUpMbps > 0) contract.push("上传 " + configured?.contractUpMbps + " Mbps");
      wan.detail.textContent = "运营商签约带宽：" + (contract.join(" / ") || "未配置");
      const physicalWAN = observedPorts?.find(p => p.role === "wan");
      if (physicalWAN) wan.detail.textContent += " · 配置的 WAN 物理口：" + physicalWAN.name;
    }
    const percent = n => Number.isFinite(n) && n >= 0 && n <= 100 ? n : null;
    const formatPercent = n => n === 0 ? "0%" : n < 0.1 ? "<0.1%" : n.toFixed(1) + "%";
    const formatBytes = n => {
      const units = ["B", "KiB", "MiB", "GiB", "TiB"];
      const index = n === 0 ? 0 : Math.min(units.length - 1, Math.floor(Math.log(n) / Math.log(1024)));
      return (n / 1024 ** index).toLocaleString(undefined, { maximumFractionDigits: 1 }) + " " + units[index];
    };
    function update(data) {
      if (paused) return;
      const now = performance.now();
      let pointTime = now;
      if (Number.isFinite(data?.sampleMs)) {
        const sameClock = previousSnapshot?.clock === data.clock;
        if (sameClock && data.sampleMs <= previousSnapshot.ms) return;
        if (!sameClock) timeOffset = now - data.sampleMs;
        gap ||= !sameClock || data.sampleMs - previousSnapshot?.ms > 6000;
        previousSnapshot = { clock: data.clock, ms: data.sampleMs };
        pointTime = data.sampleMs + timeOffset;
      }
      lastTime = Math.max(lastTime, now, pointTime);
      updatePanel(wan, data?.wan, data, pointTime);
      updatePanel(lan, data?.lan, data, pointTime);
      if (data) wifi.element.hidden = !data.wifi;
      if (!wifi.element.hidden) updatePanel(wifi, data?.wifi, data, pointTime);
      else {
        wifi.previous = null;
        wifi.points.length = 0;
      }
      const cpuUse = percent(data?.cpuPercent);
      const { totalBytes, usedBytes } = data?.memory || {};
      const memoryUse = totalBytes > 0 ? percent(100 * usedBytes / totalBytes) : null;
      cpu.rx.textContent = cpuUse === null ? "未采集" : formatPercent(cpuUse);
      memory.rx.textContent = memoryUse === null ? "未采集" :
        formatBytes(usedBytes) + " / " + formatBytes(totalBytes) + "（" + formatPercent(memoryUse) + "）";
      appendPoint(cpu, { time: pointTime, rx: cpuUse, tx: null, breakBefore: gap });
      appendPoint(memory, { time: pointTime, rx: memoryUse, tx: null, breakBefore: gap });
      gap = !data;
      if (!data) {
        replaceIfChanged(ports, node("p", { class: "help" }, "物理网口未采集"));
        return;
      }
      updateWANDetail(data, data.ports);
      replaceIfChanged(ports, data.ports?.length ? table(
        ["用途", "物理网口", "链路状态", "协商速率", "双工"],
        data.ports.map(p => [
          { wan: "外网 WAN", lan: "内网 LAN" }[p.role] || "其他",
          p.name,
          { up: "已连接", down: "未连接" }[p.link] || "未采集",
          p.speedMbps ? p.speedMbps + " Mbps" : (p.link === "down" ? "—" : "未采集"),
          { full: "全双工", half: "半双工" }[p.duplex] || (p.link === "down" ? "—" : "未采集"),
        ]), "物理网口链路协商") : node("p", { class: "help" }, "物理网口未采集"));
    }
    return {
      content: node("section", { class: "card stack traffic" },
        node("div", { class: "traffic-heading" },
          node("h3", {}, "实时流量与系统资源"),
          node("div", { class: "toolbar" },
            node("label", { for: period.id }, "时间范围"), period, pause)),
        node("div", { class: "grid" }, wan.element, lan.element, wifi.element, cpu.element, memory.element), sampleNote),
      ports, update,
      breakSample,
      get paused() { return paused; },
      get sampleGeneration() { return sampleGeneration; },
    };
  }

  const trafficInterval = 2100;
  function scheduleTraffic(epoch, delay = trafficInterval) {
    trafficTimer = setTimeout(async () => {
      const current = () => epoch === state.epoch && state.loggedIn &&
        state.route === "overview" && overviewView;
      if (!current()) return;
      const { traffic } = overviewView;
      const started = performance.now();
      try {
        if (traffic.paused || trafficRequest) return;
        if (document.visibilityState !== "visible") {
          traffic.breakSample();
          return;
        }
        const generation = traffic.sampleGeneration;
        trafficRequest = api("/api/v1/network/live", {
          timeout: 4000,
          onUnauthorized: () => { if (current()) showLogin(); },
        }).catch(() => null);
        const data = await trafficRequest;
        trafficRequest = null;
        if (current() && generation === traffic.sampleGeneration) {
          if (document.visibilityState === "visible") traffic.update(data);
          else traffic.breakSample();
        }
      } finally {
        if (current()) scheduleTraffic(epoch, Math.max(250, trafficInterval - (performance.now() - started)));
      }
    }, delay);
  }

  function overview(cfg, status, clients) {
    const metric = (label, value, detail) =>
      node(
        "section",
        { class: "card metric" },
        node("div", { class: "metric-label" }, label),
        node("div", { class: "metric-value" }, value),
        node("p", { class: "help" }, detail),
      );
    const unitNames = {
      active: "运行中",
      inactive: "未运行",
      failed: "失败",
      missing: "未获取 / 不可用",
      unknown: "未采集",
    };
    const leaseValue = node("span");
    const leaseDetail = node("span");
    const interfaceBody = node("div");
    const servicesBody = node("div");
    const traffic = liveNetwork(cfg.wan);
    function update(nextStatus, nextClients) {
      if (nextClients) {
        const measured = ["leases", "mixed"].includes(nextClients.source);
        leaseValue.textContent = measured ? String((nextClients.clients || []).length) : "未采集";
        leaseDetail.textContent = measured ? "租约数量不等于当前在线数量" : "未接入有效租约数据";
      }
      if (nextStatus) refreshStatus(nextStatus);
    }
    function refreshStatus(nextStatus) {
      const interfaces = (nextStatus.interfaces || []).map(i => [
        i.role === "wan" ? "外网 WAN" : "内网 LAN",
        i.name || "未提供",
        i.source === "sysfs" && typeof i.up === "boolean"
          ? (i.up ? "已连接" : "未连接") : "未采集",
        i.source === "sysfs" ? "系统接口" : "配置值",
      ]);
      replaceIfChanged(interfaceBody, interfaces.length
        ? table(["用途", "接口", "链路状态", "来源"], interfaces, "网络接口状态")
        : empty("运行信息不可用", "请检查管理服务连接。"));
      replaceIfChanged(servicesBody, nextStatus.sources?.units === "systemctl"
        ? table(["服务", "状态"], (nextStatus.units || []).map(u => [
            u.unit, unitNames[u.state] || "未采集",
          ]), "服务运行状态")
        : empty("未采集服务状态", "当前部署没有提供系统服务查询结果。"));
    }
    update(status || {}, clients || {});
    overviewView = { update, traffic };
    return node(
      "div",
      { class: "dashboard stack" },
      node(
        "div",
        { class: "metrics" },
        metric(
          "管理服务",
          state.health ? "可访问" : "不可用",
          "仅表示管理 API 可访问",
        ),
        metric(
          "客户端租约",
          leaseValue,
          leaseDetail,
        ),
        metric(
          "已配置接入点",
          String((cfg.wifi?.aps || []).length),
          "配置数量，不是硬件检测结果",
        ),
        metric(
          "配置主机名",
          cfg.system?.hostname || "未设置",
          "保存值，不代表已应用到主机",
        ),
      ),
      traffic.content,
      card(
        "网络接口",
        interfaceBody,
        traffic.ports,
        node("div", { class: "toolbar" }, link("wan"), link("lan")),
      ),
      node(
        "div",
        { class: "grid" },
        kv("家庭网络配置", [
          ["内网网段", cfg.lan?.ipv4Cidr || "未设置"],
          [
            "地址分配",
            cfg.lan?.dhcp?.enable ? "已配置启用 DHCP" : "未配置 DHCP",
          ],
          ["上网方式", cfg.wan?.mode || "未设置"],
          ["无线配置", cfg.wifi?.enable ? "已配置启用" : "已配置关闭"],
        ]),
        card("服务运行状态", servicesBody),
      ),
      card(
        "数据与生效说明",
        notice(
          "接口链路连接不代表互联网可达。无线硬件和设备在线状态尚未完整采集。",
        ),
        node(
          "div",
          { class: "toolbar" },
          link("clients", "查看设备记录"),
          link("system?tab=activity", "查看作业记录"),
        ),
      ),
    );
  }
  function clientsPage(data) {
    const items = data?.clients || [];
    const query = node("input", {
        id: "client-search",
        type: "search",
        placeholder: "名称、IP 或 MAC",
      }),
      body = node("div");
    function draw() {
      const q = query.value.toLowerCase();
      const rows = items.filter((c) =>
        [c.hostname, c.ip, c.mac].some((v) =>
          String(v || "")
            .toLowerCase()
            .includes(q),
        ),
      );
      body.replaceChildren(
        rows.length
          ? table(
              ["名称", "IP 地址", "MAC 地址", "来源", "访问策略", "操作"],
              rows.map((c) => [
                c.hostname || "未命名",
                c.ip || "—",
                c.mac || "—",
                c.source === "static" ? "静态绑定" : "DHCP 租约",
                c.blockedSource === "last-good" && c.blockedRuntimeKnown
                  ? (c.blocked ? "已应用阻止" : "未应用阻止")
                  : c.blockedRuntimeKnown === false
                    ? "尚未确认运行阻断状态（仅有配置）"
                    : (c.blocked ? "已配置阻止" : "未配置阻止"),
                c.mac ? btn("编辑", () => editClient(c), "quiet") : "—",
              ]),
              "客户端记录",
            )
          : empty(
              q ? "没有匹配的设备" : "没有设备记录",
              "静态绑定或 DHCP 租约不等于设备当前在线。",
            ),
      );
    }
    query.addEventListener("input", draw);
    draw();
    return stack(
      notice(
        !data
          ? "客户端数据获取失败，请刷新重试。"
          : data.source === "stub"
            ? "当前没有实时租约采集；列表仅可能包含静态绑定配置。"
            : "列表来自 DHCP 租约；租约有效不代表设备当前在线。",
      ),
      card(
        "设备记录",
        node(
          "div",
          { class: "form-row" },
          node("label", { for: query.id }, "搜索设备"),
          query,
        ),
        body,
      ),
    );
  }
  function editClient(client) {
    const revision = state.configCache?.revision;
    const d = { hostname: client.hostname || "", blocked: !!(client.draftBlocked ?? client.blocked) };
    // This dialog owns its draft independently of the page configuration editor.
    const name = node("input", { id: "client-name", value: d.hostname }),
      blocked = node("input", { id: "client-blocked", type: "checkbox" });
    blocked.checked = d.blocked;
    const message = notice("修改访问策略后需要生成或应用配置。");
    const dialog = node("dialog", { "aria-labelledby": "client-dialog-title" }),
      close = () => {
        dialog.close();
        dialog.remove();
      };
    const submit = btn(
      "保存设备",
      async () => {
        submit.disabled = true;
        state.busy = true;
        syncActions();
        try {
          const changes = {};
          if (name.value !== d.hostname) changes.hostname = name.value;
          if (blocked.checked !== d.blocked) changes.blocked = blocked.checked;
          await api(`/api/v1/clients/${encodeURIComponent(client.mac)}`, {
            method: "PATCH",
            body: changes,
            revision,
          });
          state.pending = true;
          close();
          await renderRoute();
          feedback("设备配置已保存，尚未生成或应用。", "good");
        } catch (e) {
          message.textContent = e.message;
          message.classList.add("error");
        } finally {
          submit.disabled = false;
          state.busy = false;
          syncActions();
        }
      },
      "primary",
    );
    dialog.append(
      node("h3", { id: "client-dialog-title" }, "编辑设备"),
      node("p", {}, client.mac),
      node(
        "div",
        { class: "form-row" },
        node("label", { for: name.id }, "设备名称"),
        name,
      ),
      node(
        "div",
        { class: "form-row check" },
        node("label", { for: blocked.id }, "阻止访问"),
        blocked,
      ),
      message,
      node("div", { class: "toolbar" }, btn("取消", close), submit),
    );
    dialog.addEventListener("close", () => dialog.remove());
    document.body.append(dialog);
    dialog.showModal();
  }
  function diagnostic(title, value) {
    return node(
      "details",
      { class: "diagnostic" },
      node("summary", {}, title),
      node("pre", {}, JSON.stringify(value, null, 2)),
    );
  }
  function redact(value) {
    if (Array.isArray(value)) return value.map(redact);
    if (value && typeof value === "object")
      return Object.fromEntries(
        Object.entries(value).map(([k, v]) => [
          k,
          /password|passwd|psk|secret|token|authkey|privatekey|accesskey|apikey/i.test(
            k,
          )
            ? v
              ? "已设置"
              : "未设置"
            : redact(v),
        ]),
      );
    return value;
  }
  function systemPage(cfg, status, tab, activity) {
    const tabs = [
      ["general", "基本设置"],
      ["account", "账户与备份"],
      ["activity", "操作记录"],
      ["diagnostics", "诊断"],
    ];
    if (!tabs.some(([key]) => key === tab)) tab = "general";
    const nav = node(
      "nav",
      { class: "system-tabs", "aria-label": "系统功能" },
      tabs.map(([key, title]) =>
        node(
          "a",
          {
            href: `#/system?tab=${key}`,
            "aria-current": tab === key ? "page" : null,
          },
          title,
        ),
      ),
    );
    if (tab === "general") return stack(nav, coreEditor("system", cfg, status));
    if (tab === "account") return stack(nav, accountPage(cfg));
    if (tab === "diagnostics")
      return stack(
        nav,
        notice(
          "诊断信息默认折叠。配置中的密钥字段已遮盖；分享前仍请检查地址、设备标识等信息。",
        ),
        card(
          "诊断信息",
          diagnostic("管理服务健康状态", state.health || { error: "未获取" }),
          diagnostic("运行状态与来源", status || { error: "未获取" }),
          diagnostic("配置诊断（密钥已遮盖）", redact(cfg)),
        ),
      );
    const jobs = activity?.jobs,
      audit = activity?.audit;
    const search = node("input", {
        type: "search",
        id: "activity-search",
        placeholder: "作业、状态、操作者或动作",
      }),
      lists = node("div", { class: "stack" });
    const date = (v) => (v ? new Date(v).toLocaleString() : "—");
    const draw = () => {
      const q = search.value.toLowerCase(),
        match = (row) =>
          Object.values(row).some((v) =>
            String(v ?? "")
              .toLowerCase()
              .includes(q),
          );
      const j = (jobs?.jobs || []).filter(match),
        a = (audit?.audit || []).filter(match);
      const jobResult = (v) => {
        let payload = {};
        try {
          payload = JSON.parse(v.payload || "{}");
        } catch {}
        return node(
          "details",
          {},
          node(
            "summary",
            {},
            v.error
              ? "查看失败原因"
              : v.status === "success"
                ? payload.mode === "generate-only"
                  ? "仅生成完成"
                  : "查看执行结果"
                : "查看作业",
          ),
          node("p", {}, v.error || "作业状态：" + v.status),
          diagnostic("作业详情", redact(v)),
        );
      };
      lists.replaceChildren(
        card(
          "最近 20 条配置作业",
          !jobs
            ? notice("作业获取失败，请刷新重试。", "error")
            : j.length
              ? table(
                  ["时间", "作业", "状态", "结果"],
                  j.map((v) => [
                    date(v.createdAt),
                    `${v.kind || "作业"} · ${v.id}`,
                    v.status,
                    jobResult(v),
                  ]),
                  "配置作业",
                )
              : empty("没有匹配的作业", "生成或应用后可在这里查看执行记录。"),
        ),
        card(
          "最近 50 条操作审计",
          !audit || audit.source === "stub"
            ? notice("审计获取失败，请刷新重试。", "error")
            : a.length
              ? table(
                  ["时间", "操作者", "动作", "详情"],
                  a.map((v) => [date(v.at), v.actor, v.action, v.detail]),
                  "操作审计",
                )
              : empty("没有匹配的记录", "这里展示服务器记录的操作。"),
        ),
      );
    };
    search.addEventListener("input", draw);
    draw();
    return stack(
      nav,
      node(
        "div",
        { class: "form-row" },
        node("label", { for: search.id }, "搜索操作记录"),
        search,
      ),
      lists,
    );
  }
  function accountPage(cfg) {
    let revision = cfg.revision;
    const current = node("input", {
      id: "password-current",
      type: "password",
      autocomplete: "current-password",
      required: true,
    });
    const next = node("input", {
      id: "password-next",
      type: "password",
      autocomplete: "new-password",
      required: true,
      minlength: 8,
    });
    const again = node("input", {
      id: "password-again",
      type: "password",
      autocomplete: "new-password",
      required: true,
      minlength: 8,
    });
    const passwordMessage = notice("新密码至少 8 位。"),
      passwordForm = node("form");
    const passwordButton = node(
      "button",
      { type: "submit", class: "btn primary" },
      "修改密码",
    );
    passwordForm.append(
      node(
        "div",
        { class: "form-grid" },
        [
          [current, "当前密码"],
          [next, "新密码"],
          [again, "再次输入新密码"],
        ].map(([input, label]) =>
          node(
            "div",
            { class: "form-row" },
            node("label", { for: input.id }, label),
            input,
          ),
        ),
      ),
      passwordMessage,
      passwordButton,
    );
    passwordForm.addEventListener("submit", async (e) => {
      e.preventDefault();
      if (next.value !== again.value) {
        passwordMessage.textContent = "两次输入的新密码不一致。";
        return;
      }
      passwordButton.disabled = true;
      try {
        await api("/api/v1/session/password", {
          method: "POST",
          body: { currentPassword: current.value, newPassword: next.value },
        });
        passwordForm.reset();
        passwordMessage.textContent = "密码已修改，其他登录会话已失效。";
      } catch (e) {
        passwordMessage.textContent = e.message;
      } finally {
        passwordButton.disabled = false;
      }
    });
    const backupMessage = notice(
      "备份包含配置和密钥，请保存在可信位置。恢复只保存配置，之后需生成或应用。",
    );
    const download = btn("下载配置备份", async () => {
      download.disabled = true;
      try {
        const response = await api("/api/v1/backup", { raw: true });
        const blob = await response.blob(),
          url = URL.createObjectURL(blob),
          a = node("a", {
            href: url,
            download: `router-backup-${new Date().toISOString().slice(0, 10)}.json`,
          });
        document.body.append(a);
        a.click();
        a.remove();
        setTimeout(() => URL.revokeObjectURL(url), 1000);
      } catch (e) {
        backupMessage.textContent = e.message;
      } finally {
        download.disabled = false;
      }
    });
    const file = node("input", {
      id: "restore-file",
      type: "file",
      accept: ".json,application/json",
    });
    const restore = btn("恢复所选备份", async () => {
      if (state.busy) return;
      if (!file.files.length) {
        backupMessage.textContent = "请先选择备份文件。";
        return;
      }
      if (
        !confirm("恢复会覆盖当前保存的配置，但不会立即应用到网络。是否继续？")
      )
        return;
      restore.disabled = true;
      state.busy = true;
      syncActions();
      try {
        const form = new FormData();
        form.append("file", file.files[0]);
        const result = await api("/api/v1/backup/restore", { method: "POST", body: form, revision });
        revision = result.revision;
        state.configCache = null;
        state.pending = true;
        file.value = "";
        backupMessage.textContent = "备份已恢复，尚未生成或应用。";
        syncActions();
      } catch (e) {
        backupMessage.textContent = `恢复失败：${e.message}`;
      } finally {
        restore.disabled = false;
        state.busy = false;
        syncActions();
      }
    });
    restore.id = "restore-submit";
    const reboot = btn(
      "重启设备",
      async () => {
        if (
          !state.health?.deployment?.rebootAllowed ||
          !confirm("确认重启当前部署设备？连接将暂时中断。")
        )
          return;
        reboot.disabled = true;
        try {
          await api("/api/v1/system/reboot", { method: "POST" });
          feedback("重启请求已提交，等待设备重新连接。");
        } catch (e) {
          feedback(e.message, "error");
        } finally {
          reboot.disabled = !state.health?.deployment?.rebootAllowed;
        }
      },
      "danger",
    );
    reboot.disabled = !state.health?.deployment?.rebootAllowed;
    return stack(
      card("管理员密码", passwordForm),
      card(
        "配置备份与恢复",
        backupMessage,
        download,
        node(
          "div",
          { class: "form-row" },
          node("label", { for: file.id }, "选择配置备份"),
          file,
        ),
        restore,
      ),
      card(
        "设备维护",
        node(
          "p",
          { class: "help" },
          reboot.disabled
            ? "此部署未开放设备重启能力。"
            : "重启会中断当前连接。",
        ),
        reboot,
      ),
    );
  }
  function managedDDNSSettings(ddns, onSaved) {
    const input = (title, attr) => {
      const id = `field-${++fieldId}`;
      const control = node("input", { id, ...attr });
      return { control, element: node("div", { class: "stack" }, node("label", { for: id }, title), control) };
    };
    const domain = input("DDNS 域名", { type: "text", maxlength: 254, placeholder: "edge.example.net", spellcheck: "false" });
    const zone = input("Cloudflare Zone ID", { type: "text", maxlength: 32, autocomplete: "off", spellcheck: "false" });
    const token = input("Cloudflare API Token", { type: "password", maxlength: 512, autocomplete: "new-password" });
    const result = node("p", { role: "status", class: "help" });
    const hint = node("p", { class: "help" });
    const secure = window.isSecureContext;
    let saved = ddns;
    let saving = false;
    token.control.disabled = !secure;
    const sync = () => {
      const dirty = domain.control.value.trim() !== (saved.record || "") ||
        zone.control.value.trim() !== (saved.zoneId || "") || token.control.value !== "";
      save.disabled = saving || !dirty;
    };
    const save = btn("保存域名与凭据", async () => {
      saving = true;
      for (const field of [domain, zone, token]) field.control.disabled = true;
      sync();
      try {
        const current = await api("/api/v1/ddns", { method: "PATCH", body: {
          record: domain.control.value.trim(),
          zoneId: zone.control.value.trim(),
          apiToken: token.control.value,
        } });
        render(current);
        onSaved(current);
        result.textContent = "DDNS 参数已保存，下次检查生效。";
      } catch (e) {
        result.textContent = `保存失败：${e.message}`;
      } finally {
        saving = false;
        domain.control.disabled = zone.control.disabled = false;
        token.control.disabled = !secure;
        sync();
      }
    });
    function render(current) {
      saved = current;
      domain.control.value = current.record || "";
      zone.control.value = current.zoneId || "";
      token.control.value = "";
      hint.textContent = !secure ? "如需输入令牌，请使用 HTTPS 或本地 SSH 隧道。" :
        current.tokenConfigured ? "已保存令牌。留空保留，填写新令牌可替换。" :
          "留空沿用主机凭据（如有）。填写新令牌时需提供所属 Zone ID。";
      sync();
    }
    for (const field of [domain, zone, token]) field.control.addEventListener("input", sync);
    render(ddns);
    return stack([domain.element, zone.element, token.element, hint, save, result]);
  }

  function managedDDNSPage(ddns) {
    if (!ddns) return card("动态 DNS", notice("无法读取 DDNS 开关状态，请刷新重试。", "error"));
    const id = `field-${++fieldId}`;
    const enabled = node("input", { id, type: "checkbox" });
    enabled.checked = ddns.enabled;
    const result = node("p", { role: "status", class: "help" });
    let saved = ddns.enabled;
    const summaryText = current => [current.provider, current.record, current.recordType &&
      `DNS-only ${current.recordType} 记录`].filter(Boolean).join(" · ");
    const summary = node("p", {}, summaryText(ddns));
    const save = btn("保存 DDNS", async () => {
      enabled.disabled = save.disabled = true;
      try {
        const current = await api("/api/v1/ddns", { method: "PUT", body: { enabled: enabled.checked } });
        saved = enabled.checked = current.enabled;
        result.textContent = saved ? "已启用，等待下一次定时检查。" : "已停用后续定时检查。";
      } catch (e) {
        result.textContent = `保存失败：${e.message}`;
      } finally {
        enabled.disabled = false;
        save.disabled = enabled.checked === saved;
      }
    });
    save.disabled = true;
    enabled.addEventListener("change", () => { save.disabled = enabled.checked === saved; });
    return card(
      "动态 DNS",
      summary,
      ddns.interface || ddns.intervalMinutes ? node("p", { class: "help" },
        [ddns.interface && `地址来源 ${ddns.interface}`,
          ddns.intervalMinutes && `每 ${ddns.intervalMinutes} 分钟检查`].filter(Boolean).join(" · ")) : null,
      ddns.settingsEditable
        ? managedDDNSSettings(ddns, current => { summary.textContent = summaryText(current); })
        : node("p", { class: "help" }, "令牌由主机管理。保存后影响下一次定时检查。"),
      node("label", { for: id }, enabled, "启用动态 DNS"),
      save,
      result,
    );
  }
  async function renderRoute() {
    clearTimeout(refreshTimer);
    clearTimeout(trafficTimer);
    overviewView = null;
    if (!state.loggedIn) return;
    const epoch = ++state.epoch;
    state.loading = true;
    state.section = null;
    state.pageDirty = false;
    syncActions();
    let route;
    try {
      route = routeInfo();
    } catch {
      route = { id: "overview", tab: "general" };
    }
    const { id, tab } = route;
    state.route = id;
    navigation(id);
    const target = $("#page-content");
    target.setAttribute("aria-busy", "true");
    target.replaceChildren(notice("正在加载…"));
    $("#status-banner").classList.add("hidden");
    const optional = (promise) =>
      promise.catch((e) => {
        if (e.status === 401) throw e;
        return null;
      });
    try {
      const needsStatus = ["overview", "wan", "lan", "system"].includes(id);
      const [response, health, status, clients, jobs, audit, wifiCaps, manifest, wifiChannel, wifiStations, ddns] =
        await Promise.all([
          api("/api/v1/config"),
          optional(api("/api/v1/health")),
          needsStatus ? optional(api("/api/v1/status")) : null,
          ["overview", "clients"].includes(id)
            ? optional(api("/api/v1/clients"))
            : null,
          id === "system" && tab === "activity"
            ? optional(api("/api/v1/jobs"))
            : null,
          id === "system" && tab === "activity"
            ? optional(api("/api/v1/audit"))
            : null,
          id === "wifi" ? optional(api("/api/v1/capabilities/wifi")) : null,
          optional(api("/api/v1/plugins")),
          id === "wifi" ? optional(api("/api/v1/wifi/channel")) : null,
          id === "wifi" ? optional(api("/api/v1/wifi/stations")) : null,
          id === "ddns" ? optional(api("/api/v1/ddns")) : null,
        ]);
      if (epoch !== state.epoch || !state.loggedIn) return;
      const cfg = response.config || response;
      state.configCache = clone(cfg);
      state.health = health;
      // A failed optional health request must not remove confirmed ownership.
      if (health?.deployment?.mode)
        state.confirmedCapabilities = clone(health.capabilities || {});
      state.wifiCaps = wifiCaps;
      state.wifiChannel = wifiChannel;
      state.wifiStations = wifiStations;
      state.modules = manifest?.plugins || null;
      navigation(id);
      deployment();
      refreshCandidate();
      let content;
      state.hostDDNS = id === "ddns" && Boolean(ddns || state.confirmedCapabilities?.managedDDNS);
      if (state.hostDDNS) content = managedDDNSPage(ddns);
      else if (id === "ssh") content = card("SSH 由系统管理", notice("请通过 NixOS 的 services.openssh 配置监听端口、认证策略和授权公钥。"));
      else if (id === "overview") content = overview(cfg, status, clients);
      else if (id === "clients") content = clientsPage(clients);
      else if (id === "plugins") content = pluginsEditor(cfg);
      else if (id === "system")
        content = systemPage(cfg, status, tab, { jobs, audit });
      else if (
        moduleSupport(id) ||
        pluginIds.includes(id) ||
        (!pages[id] && Object.hasOwn(cfg.plugins || {}, id))
      )
        content = pluginEditor(id, cfg);
      else if (pages[id]) content = coreEditor(id, cfg, status);
      else content = card("页面不存在", link("overview", "返回总览"));
      if (sectionReadOnly()) {
        content = stack(notice(ownershipNotice()), content);
      } else if ((!state.hostDDNS && (state.confirmedCapabilities?.unsupported || []).includes(id)) || (state.confirmedCapabilities?.managedNetwork && (pluginIds.includes(id) || moduleSupport(id)) && !moduleSupport(id)?.runtimeSupported)) {
        content = stack(notice("当前网络版本不支持应用此功能。可保留或导出历史设置；启用将被拒绝。"), content);
      }
      target.replaceChildren(content);
      target.dataset.route = id;
      state.previousHash = location.hash;
      if (health?.startupWarning) {
        $("#status-banner").textContent = `启动时发现保存的草稿异常，已使用上次成功配置提供管理服务；磁盘上的异常草稿未覆盖。必须修复并保存配置后才能应用。启动诊断：${health.startupWarning}`;
        $("#status-banner").classList.remove("hidden");
      } else if (!health) {
        $("#status-banner").textContent =
          "管理服务健康状态暂时无法获取；配置读取已完成。";
        $("#status-banner").classList.remove("hidden");
      }
    } catch (e) {
      if (epoch !== state.epoch) return;
      state.section = null;
      target.replaceChildren(
        card(
          "页面加载失败",
          notice(e.message, "error"),
          btn("重新加载", renderRoute),
        ),
      );
    } finally {
      if (epoch === state.epoch) {
        state.loading = false;
        target.setAttribute("aria-busy", "false");
        syncActions();
      }
    }
    if (epoch === state.epoch && overviewView && id === "overview") {
      scheduleOverviewRefresh(epoch);
      scheduleTraffic(epoch, 0);
    }
  }
  function scheduleOverviewRefresh(epoch) {
    refreshTimer = setTimeout(async () => {
      const current = () =>
        epoch === state.epoch && state.loggedIn && state.route === "overview";
      const canRefresh = () =>
        document.visibilityState === "visible" &&
        !state.busy &&
        !document.querySelector("dialog[open]");
      if (!current()) return;
      try {
        if (!canRefresh() || overviewRequest) return;
        const options = { onUnauthorized: () => { if (current()) showLogin(); } };
        overviewRequest = Promise.allSettled([
          api("/api/v1/status", options),
          api("/api/v1/clients", options),
        ]);
        const [status, clients] = await overviewRequest;
        overviewRequest = null;
        if (!current() || !canRefresh()) return;
        overviewView?.update(
          status.status === "fulfilled" ? status.value : null,
          clients.status === "fulfilled" ? clients.value : null,
        );
      } finally {
        if (current()) scheduleOverviewRefresh(epoch);
      }
    }, 15000);
  }
  function showLogin() {
    clearTimeout(refreshTimer);
    clearTimeout(trafficTimer);
    overviewView = null;
    state.epoch++;
    state.loggedIn = false;
    state.pageDirty = false;
    state.section = null;
    state.loading = false;
    $("#main-view").classList.add("hidden");
    $("#login-view").classList.remove("hidden");
    $("#page-content").replaceChildren();
    document.querySelectorAll("dialog").forEach((d) => {
      d.close();
      d.remove();
    });
    $("#login-username").focus();
  }
  async function loggedIn(session) {
    state.loggedIn = true;
    $("#login-view").classList.add("hidden");
    $("#main-view").classList.remove("hidden");
    $("#user-display").textContent =
      session.username || session.user || "admin";
    await renderRoute();
    const job = sessionStorage.getItem("router-apply-job");
    if (job) feedback(`上次作业 ${job} 尚未确认完成，请在系统操作记录中核查。`);
  }
  function discard() {
    return !state.pageDirty || confirm("当前修改尚未保存，是否放弃修改？");
  }
  function closeNav() {
    $("#top-nav").classList.remove("open");
    $("#nav-toggle").setAttribute("aria-expanded", "false");
  }
  function boot() {
    $(".skip-link").addEventListener("click", (e) => {
      e.preventDefault();
      $("#page-title").focus();
    });
    new ResizeObserver((entries) => {
      document.documentElement.style.setProperty(
        "--actionbar-height",
        `${entries[0].target.getBoundingClientRect().height}px`,
      );
    }).observe($(".actionbar"));
    $("#login-form").addEventListener("submit", async (e) => {
      e.preventDefault();
      const button = $("#login-submit");
      button.disabled = true;
      $("#login-error").textContent = "";
      $("#login-error").classList.add("hidden");
      try {
        const session = await api("/api/v1/session", {
          method: "POST",
          body: {
            username: $("#login-username").value,
            password: $("#login-password").value,
          },
        });
        $("#login-password").value = "";
        await loggedIn(session);
      } catch (e) {
        $("#login-error").classList.remove("hidden");
        $("#login-error").textContent =
          e.status === 401 ? "用户名或密码不正确。" : e.message;
      } finally {
        button.disabled = false;
      }
    });
    $("#logout-btn").addEventListener("click", async () => {
      if (!discard()) return;
      try {
        await api("/api/v1/session", { method: "DELETE" });
        feedback("");
        showLogin();
      } catch (e) {
        feedback(e.message, "error");
      }
    });
    $("#apply-save").addEventListener("click", () => {
      if (!state.busy) save();
    });
    $("#apply-run").addEventListener("click", apply);
    $("#apply-undo").addEventListener("click", () => {
      if (discard()) renderRoute();
    });
    $("#page-refresh").addEventListener("click", () => {
      if (discard()) renderRoute();
    });
    $("#nav-toggle").addEventListener("click", () => {
      const open = $("#top-nav").classList.toggle("open");
      $("#nav-toggle").setAttribute("aria-expanded", String(open));
    });
    document.addEventListener("visibilitychange", () => overviewView?.traffic.breakSample());
    document.addEventListener("keydown", (e) => {
      if (e.key === "Escape") closeNav();
    });
    window.addEventListener("hashchange", () => {
      if (state.skipHash) {
        state.skipHash = false;
        return;
      }
      if (state.busy || !discard()) {
        state.skipHash = true;
        location.hash = state.previousHash;
        return;
      }
      closeNav();
      renderRoute();
    });
    window.addEventListener("beforeunload", (e) => {
      if (state.pageDirty || state.busy) {
        e.preventDefault();
        e.returnValue = "";
      }
    });
    api("/api/v1/session")
      .then(loggedIn)
      .catch((e) => {
        showLogin();
        if (e.status !== 401) {
          $("#login-error").textContent = e.message;
          $("#login-error").classList.remove("hidden");
        }
      });
  }
  window.app = { state, apiBase };
  if (document.readyState === "loading")
    document.addEventListener("DOMContentLoaded", boot);
  else boot();
})();
