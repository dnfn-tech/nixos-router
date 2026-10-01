(() => {
  "use strict";

  const q = (sel, el = document) => el.querySelector(sel);
  const qc = (sel, el = document) => el.querySelectorAll(sel);

  function getQueryParam(name) {
    const url = new URL(window.location.href);
    return url.searchParams.get(name);
  }

  async function renderPlugins() {
    const content = clearMain();
    const grid = document.createElement("div");
    grid.className = "grid";
    content.appendChild(grid);

    // Load from API and config
    const [base, listResp] = await Promise.all([
      loadConfigCached(false),
      fetchJson("/api/v1/plugins").catch(() => ({})),
    ]);
    const draftPlugins = deepClone(base.plugins || {});
    const list = Array.isArray(listResp) ? listResp : (listResp?.plugins || []);
    const card = document.createElement("div");
    card.className = "card";
    const body = document.createElement("div");
    card.innerHTML = `<h3>插件管理</h3>`;

    if (!Array.isArray(list) || list.length === 0) {
      const empty = document.createElement("p");
      empty.className = "muted";
      empty.textContent = "未提供插件列表";
      card.appendChild(empty);
    } else {
      list.forEach((p) => {
        const name = p?.name || p?.id || p?.key;
        if (!name) return;
        const enabled = !!(p?.enabled ?? draftPlugins?.[name]?.enable);
        if (!draftPlugins[name]) draftPlugins[name] = { enable: enabled, config: {} };
        const row = document.createElement("div");
        row.className = "form-row";
        const lab = document.createElement("label");
        lab.textContent = name;
        const chk = createInput("checkbox", { checked: enabled });
        chk.addEventListener("change", () => {
          draftPlugins[name].enable = chk.checked;
          markDirty();
        });
        row.appendChild(lab);
        row.appendChild(chk);
        body.appendChild(row);
      });
      card.appendChild(body);
    }
    const hint = document.createElement("div");
    hint.className = "muted small mt8";
    hint.textContent = "切换启用状态后保存。保存成功将刷新导航。核心内置页面只读列出，不在此处开关。";
    card.appendChild(hint);
    grid.appendChild(card);

    mountUxBar(content, {
      onSave: async () => {
        // 尝试逐个 PUT /plugins/{id}；若全部 404 则回退到 PUT /config
        const names = new Set([
          ...Object.keys(draftPlugins || {}),
          ...list.map(p => (typeof p === "string" ? p : (p?.name || p?.id || p?.key))).filter(Boolean),
        ]);
        let anySuccess = false;
        let all404 = true;
        let lastErr = null;
        for (const name of names) {
          try {
            await putPlugin(name, draftPlugins[name]);
            anySuccess = true;
            all404 = false;
          } catch (e) {
            lastErr = e;
            if (e?.status === 404) {
              // keep all404 only if all fail as 404
            } else {
              all404 = false;
            }
          }
        }
        if (all404) {
          const res = await saveSection("plugins", draftPlugins, { andGenerate: false });
          if (!res.saved) { alert(`保存失败：${res.error?.message || "未知错误"}`); return; }
        } else if (!anySuccess && lastErr) {
          alert(`保存失败：${lastErr?.message || "未知错误"}`);
          return;
        }
        await loadNav(); setActiveLink();
      },
      onSaveAndGenerate: async () => {
        const names = new Set([
          ...Object.keys(draftPlugins || {}),
          ...list.map(p => (typeof p === "string" ? p : (p?.name || p?.id || p?.key))).filter(Boolean),
        ]);
        let anySuccess = false;
        let all404 = true;
        let lastErr = null;
        for (const name of names) {
          try {
            await putPlugin(name, draftPlugins[name]);
            anySuccess = true;
            all404 = false;
          } catch (e) {
            lastErr = e;
            if (e?.status === 404) {
              // will consider fallback
            } else {
              all404 = false;
            }
          }
        }
        if (all404) {
          const res = await saveSection("plugins", draftPlugins, { andGenerate: true });
          if (!res.saved) { alert(`保存失败：${res.error?.message || "未知错误"}`); return; }
        } else if (!anySuccess && lastErr) {
          alert(`保存失败：${lastErr?.message || "未知错误"}`);
          return;
        } else {
          await fetchJson("/api/v1/apply", { method: "POST" }).catch(() => {});
        }
        await loadNav(); setActiveLink(); alert("已保存并提交生成任务（不应用运行态）");
      },
    });
  }

  async function renderPluginGeneric(name) {
    const content = clearMain();
    const grid = document.createElement("div");
    grid.className = "grid";
    content.appendChild(grid);
    const cfg = await loadConfigCached(false);
    const plug = deepClone((cfg.plugins || {})[name] || null);
    if (!plug) {
      const card = document.createElement("div");
      card.className = "card";
      card.innerHTML = `<h3>插件 ${escapeHtml(name)}</h3><p class="muted">插件未启用或未提供配置</p>`;
      grid.appendChild(card);
      return;
    }
    if (!plug.config) plug.config = {};
    const card = document.createElement("div");
    card.className = "card";
    const form = document.createElement("div");
    form.className = "form";
    const chk = createInput("checkbox", { checked: !!plug.enable });
    chk.addEventListener("change", () => { plug.enable = chk.checked; markDirty(); });
    form.appendChild(createRow("启用插件", chk));
    // Render known simple KV editor for generic maps
    Object.entries(plug.config).forEach(([k, v]) => {
      const isSecret = /password|psk|secret|token/i.test(k);
      const input = createInput(isSecret ? "password" : "text", { value: isSecret ? "****" : (v ?? "") });
      input.addEventListener("input", () => { plug.config[k] = input.value; markDirty(); });
      form.appendChild(createRow(k, input));
    });
    const addK = createInput("text", { placeholder: "键名" });
    const addV = createInput("text", { placeholder: "键值" });
    const addBtn = document.createElement("button");
    addBtn.className = "btn";
    addBtn.textContent = "新增键";
    addBtn.addEventListener("click", () => {
      const k = addK.value.trim();
      if (!k) return;
      plug.config[k] = addV.value;
      addK.value = ""; addV.value = "";
      markDirty();
      // simple rerender append
      const isSecret = /password|psk|secret|token/i.test(k);
      const input = createInput(isSecret ? "password" : "text", { value: isSecret ? "****" : (plug.config[k] ?? "") });
      input.addEventListener("input", () => { plug.config[k] = input.value; markDirty(); });
      form.appendChild(createRow(k, input));
    });
    const addRow = document.createElement("div");
    addRow.className = "form-row";
    addRow.appendChild(addK);
    addRow.appendChild(addV);
    addRow.appendChild(addBtn);
    form.appendChild(addRow);
    card.appendChild(form);
    grid.appendChild(card);

    mountUxBar(content, {
      onSave: async () => {
        try {
          await putPlugin(name, plug);
          alert("已保存");
        } catch (e) {
          if (e?.status === 404) {
            const res = await saveSection(`plugins.${name}`, plug, { andGenerate: false });
            if (!res.saved) alert(`保存失败：${res.error?.message || "未知错误"}`); else alert("已保存");
          } else {
            alert(`保存失败：${e?.message || "未知错误"}`);
          }
        }
      },
      onSaveAndGenerate: async () => {
        try {
          await putPlugin(name, plug);
          await fetchJson("/api/v1/apply", { method: "POST" });
          alert("已保存并提交生成任务（不应用运行态）");
        } catch (e) {
          if (e?.status === 404) {
            const res = await saveSection(`plugins.${name}`, plug, { andGenerate: true });
            if (!res.saved) alert(`保存失败：${res.error?.message || "未知错误"}`);
            else alert("已保存并提交生成任务（不应用运行态）");
          } else {
            alert(`保存失败：${e?.message || "未知错误"}`);
          }
        }
      },
    });
  }

async function renderWiFi() {
  const content = clearMain();
  const grid = document.createElement("div");
  grid.className = "grid";
  content.appendChild(grid);
  const [base, status, caps] = await Promise.all([
    loadConfigCached(false),
    fetchJson("/api/v1/status").catch(() => ({})),
    fetchJson("/api/v1/capabilities/wifi").catch(() => ({})),
  ]);
  const draft = deepClone(base.wifi || { enable: false, bridgeToLan: true, aps: [] });
  if (!Array.isArray(draft.aps)) draft.aps = [];
  const maxAP = caps?.maxAP;

  const card = document.createElement("div");
  card.className = "card";
  const form = document.createElement("div");
  form.className = "form";
  const chkEnable = createInput("checkbox", { checked: !!draft.enable });
  chkEnable.addEventListener("change", () => { draft.enable = chkEnable.checked; markDirty(); });
  form.appendChild(createRow("启用 WiFi", chkEnable));
  const chkBridge = createInput("checkbox", { checked: !!draft.bridgeToLan });
  chkBridge.addEventListener("change", () => { draft.bridgeToLan = chkBridge.checked; markDirty(); });
  form.appendChild(createRow("桥接到 LAN", chkBridge));
  if (maxAP != null) {
    const note = document.createElement("div");
    note.className = "muted small";
    note.textContent = `能力限制：最多 ${maxAP} 个 AP`;
    form.appendChild(note);
  }
  card.appendChild(form);
  grid.appendChild(card);

  const apsEditor = createArrayEditor({
    title: "接入点（APs）",
    items: () => draft.aps,
    renderItem: (ap, idx) => {
      const w = document.createElement("div");
      const inSsid = createInput("text", { value: ap?.ssid || "" });
      inSsid.addEventListener("input", () => { draft.aps[idx].ssid = inSsid.value.trim(); markDirty(); });
      const selBand = createSelect([["2g","2.4GHz"],["5g","5GHz"],["6g","6GHz"]], ap?.band || "2g");
      selBand.addEventListener("change", () => { draft.aps[idx].band = selBand.value; markDirty(); });
      const inChan = createInput("number", { value: ap?.channel || "" });
      inChan.addEventListener("input", () => { draft.aps[idx].channel = Number(inChan.value || 0); markDirty(); });
      const inPSK = createInput("password", { value: "****" });
      inPSK.addEventListener("input", () => { draft.aps[idx].psk = inPSK.value; markDirty(); });
      const chkAPEn = createInput("checkbox", { checked: !!ap?.enable });
      chkAPEn.addEventListener("change", () => { draft.aps[idx].enable = chkAPEn.checked; markDirty(); });
      const chkGuest = createInput("checkbox", { checked: !!ap?.guest });
      chkGuest.addEventListener("change", () => { draft.aps[idx].guest = chkGuest.checked; markDirty(); });
      const chkIsolate = createInput("checkbox", { checked: !!ap?.isolate });
      chkIsolate.addEventListener("change", () => { draft.aps[idx].isolate = chkIsolate.checked; markDirty(); });
      w.appendChild(createRow("SSID", inSsid));
      w.appendChild(createRow("频段", selBand));
      w.appendChild(createRow("信道", inChan));
      w.appendChild(createRow("PSK（未更改留空或 ****）", inPSK));
      w.appendChild(createRow("启用", chkAPEn));
      w.appendChild(createRow("访客网络", chkGuest));
      w.appendChild(createRow("隔离客户端", chkIsolate));
      return w;
    },
    onAdd: () => {
      if (typeof maxAP === "number" && draft.aps.length >= maxAP) return;
      draft.aps.push({ ssid: "", band: "2g", channel: 0, enable: true, guest: false, isolate: false, psk: "****" });
    },
  });
  grid.appendChild(apsEditor);

  // 状态只读卡
  const wifiSt = status?.wifi || {};
  const rows = [];
  if (wifiSt.enabled != null) rows.push(["状态", wifiSt.enabled ? "已启用" : "未启用"]);
  if (wifiSt.aps != null) rows.push(["AP 数量（状态）", String(wifiSt.aps)]);
  if (rows.length) grid.appendChild(renderKeyValueCard("当前状态（只读）", rows));

  mountUxBar(content, {
    onSave: async () => {
      const res = await saveSection("wifi", draft, { andGenerate: false });
      if (!res.saved) alert(`保存失败：${res.error?.message || "未知错误"}`);
    },
    onSaveAndGenerate: async () => {
      const res = await saveSection("wifi", draft, { andGenerate: true });
      if (!res.saved) alert(`保存失败：${res.error?.message || "未知错误"}`);
      else alert("已保存并提交生成任务（不应用运行态）");
    },
  });
}

async function renderDNS() {
  const content = clearMain();
  const grid = document.createElement("div");
  grid.className = "grid";
  content.appendChild(grid);
  const base = await loadConfigCached(false);
  const draft = deepClone(base.dns || { enableDnsmasq: true, upstreams: [], domain: "" });
  if (!Array.isArray(draft.upstreams)) draft.upstreams = [];

  const card = document.createElement("div");
  card.className = "card";
  const form = document.createElement("div");
  form.className = "form";
  const chk = createInput("checkbox", { checked: !!draft.enableDnsmasq });
  chk.addEventListener("change", () => { draft.enableDnsmasq = chk.checked; markDirty(); });
  form.appendChild(createRow("启用 dnsmasq", chk));
  const inDomain = createInput("text", { value: draft.domain || "" });
  inDomain.addEventListener("input", () => { draft.domain = inDomain.value.trim(); markDirty(); });
  form.appendChild(createRow("域名", inDomain));

  const upsEditor = createArrayEditor({
    title: "上游 DNS（upstreams）",
    items: () => draft.upstreams,
    renderItem: (it, idx) => {
      const w = document.createElement("div");
      const inp = createInput("text", { value: it || "" });
      inp.addEventListener("input", () => { draft.upstreams[idx] = inp.value.trim(); markDirty(); });
      w.appendChild(createRow(`上游 #${idx+1}`, inp));
      return w;
    },
    onAdd: () => { draft.upstreams.push(""); },
  });

  card.appendChild(form);
  grid.appendChild(card);
  grid.appendChild(upsEditor);

  mountUxBar(content, {
    onSave: async () => {
      const res = await saveSection("dns", draft, { andGenerate: false });
      if (!res.saved) alert(`保存失败：${res.error?.message || "未知错误"}`);
    },
    onSaveAndGenerate: async () => {
      const res = await saveSection("dns", draft, { andGenerate: true });
      if (!res.saved) alert(`保存失败：${res.error?.message || "未知错误"}`);
      else alert("已保存并提交生成任务（不应用运行态）");
    },
  });
}

async function renderFirewall() {
  const content = clearMain();
  const grid = document.createElement("div");
  grid.className = "grid";
  content.appendChild(grid);
  const base = await loadConfigCached(false);
  const draft = deepClone(base.firewall || { enable: true, natEnabled: true, portForwards: [], upnp: false });
  if (!Array.isArray(draft.portForwards)) draft.portForwards = [];

  const card = document.createElement("div");
  card.className = "card";
  const form = document.createElement("div");
  form.className = "form";
  const chkEnable = createInput("checkbox", { checked: !!draft.enable });
  chkEnable.addEventListener("change", () => { draft.enable = chkEnable.checked; markDirty(); });
  form.appendChild(createRow("启用防火墙", chkEnable));
  const chkNAT = createInput("checkbox", { checked: !!draft.natEnabled });
  chkNAT.addEventListener("change", () => { draft.natEnabled = chkNAT.checked; markDirty(); });
  form.appendChild(createRow("NAT", chkNAT));
  const chkUPnP = createInput("checkbox", { checked: !!draft.upnp });
  chkUPnP.addEventListener("change", () => { draft.upnp = chkUPnP.checked; markDirty(); });
  form.appendChild(createRow("UPnP", chkUPnP));

  const pfEditor = createArrayEditor({
    title: "端口转发（portForwards）",
    items: () => draft.portForwards,
    renderItem: (it, idx) => {
      const w = document.createElement("div");
      const inName = createInput("text", { value: it?.name || "" });
      inName.addEventListener("input", () => { draft.portForwards[idx].name = inName.value.trim(); markDirty(); });
      const inProto = createInput("text", { value: it?.proto || "tcp" });
      inProto.addEventListener("input", () => { draft.portForwards[idx].proto = inProto.value.trim(); markDirty(); });
      const inExt = createInput("number", { value: it?.externalPort || "" });
      inExt.addEventListener("input", () => { draft.portForwards[idx].externalPort = Number(inExt.value || 0); markDirty(); });
      const inIntIP = createInput("text", { value: it?.internalIP || "" });
      inIntIP.addEventListener("input", () => { draft.portForwards[idx].internalIP = inIntIP.value.trim(); markDirty(); });
      const inIntPort = createInput("number", { value: it?.internalPort || "" });
      inIntPort.addEventListener("input", () => { draft.portForwards[idx].internalPort = Number(inIntPort.value || 0); markDirty(); });
      w.appendChild(createRow("名称", inName));
      w.appendChild(createRow("协议", inProto));
      w.appendChild(createRow("外部端口", inExt));
      w.appendChild(createRow("内部 IP", inIntIP));
      w.appendChild(createRow("内部端口", inIntPort));
      return w;
    },
    onAdd: () => { draft.portForwards.push({ name: "", proto: "tcp", externalPort: 0, internalIP: "", internalPort: 0 }); },
  });

  card.appendChild(form);
  grid.appendChild(card);
  grid.appendChild(pfEditor);

  mountUxBar(content, {
    onSave: async () => {
      const res = await saveSection("firewall", draft, { andGenerate: false });
      if (!res.saved) alert(`保存失败：${res.error?.message || "未知错误"}`);
    },
    onSaveAndGenerate: async () => {
      const res = await saveSection("firewall", draft, { andGenerate: true });
      if (!res.saved) alert(`保存失败：${res.error?.message || "未知错误"}`);
      else alert("已保存并提交生成任务（不应用运行态）");
    },
  });
}

async function renderSSH() {
    const content = clearMain();
    const grid = document.createElement("div");
    grid.className = "grid";
    content.appendChild(grid);
  const [base, status] = await Promise.all([
    loadConfigCached(false),
    fetchJson("/api/v1/status").catch(() => ({})),
  ]);
  const draft = deepClone(base.ssh || { enable: true, port: 22, passwordAuth: false, authorizedKeys: [] });
  if (!Array.isArray(draft.authorizedKeys)) draft.authorizedKeys = [];

  const card = document.createElement("div");
  card.className = "card";
  const form = document.createElement("div");
  form.className = "form";
  const chkEn = createInput("checkbox", { checked: !!draft.enable });
  chkEn.addEventListener("change", () => { draft.enable = chkEn.checked; markDirty(); });
  form.appendChild(createRow("启用 SSH", chkEn));
  const inPort = createInput("number", { value: draft.port ?? 22, min: 1, max: 65535 });
  inPort.addEventListener("input", () => { draft.port = Number(inPort.value || 0); markDirty(); });
  form.appendChild(createRow("端口", inPort));
  const chkPwd = createInput("checkbox", { checked: !!draft.passwordAuth });
  chkPwd.addEventListener("change", () => { draft.passwordAuth = chkPwd.checked; markDirty(); });
  form.appendChild(createRow("允许密码登录", chkPwd));

  const keysEditor = createArrayEditor({
    title: "AuthorizedKeys",
    items: () => draft.authorizedKeys,
    renderItem: (it, idx) => {
      const w = document.createElement("div");
      const ta = document.createElement("textarea");
      ta.value = String(it || "");
      ta.style.width = "100%";
      ta.rows = 3;
      ta.addEventListener("input", () => { draft.authorizedKeys[idx] = ta.value; markDirty(); });
      w.appendChild(ta);
      return w;
    },
    onAdd: () => { draft.authorizedKeys.push(""); },
  });

  card.appendChild(form);
  grid.appendChild(card);
  grid.appendChild(keysEditor);

  const sshStat = status?.ssh || {};
  const rows = [];
  if (sshStat.enabled != null) rows.push(["运行状态", sshStat.enabled ? "已启用" : "未启用"]);
  if (sshStat.port != null) rows.push(["运行端口", String(sshStat.port)]);
  if (rows.length) grid.appendChild(renderKeyValueCard("当前状态（只读）", rows));

  mountUxBar(content, {
    onSave: async () => {
      const res = await saveSection("ssh", draft, { andGenerate: false });
      if (!res.saved) alert(`保存失败：${res.error?.message || "未知错误"}`);
    },
    onSaveAndGenerate: async () => {
      const res = await saveSection("ssh", draft, { andGenerate: true });
      if (!res.saved) alert(`保存失败：${res.error?.message || "未知错误"}`);
      else alert("已保存并提交生成任务（不应用运行态）");
    },
  });
  }

  async function renderDDNS() {
    const content = clearMain();
    const grid = document.createElement("div");
    grid.className = "grid";
    content.appendChild(grid);
    const base = await loadConfigCached(false);
    const draft = deepClone(base.ddns || { provider: "", account: "", password: "****", apiToken: "****", domain: "", hostname: "" });
    const card = document.createElement("div");
    card.className = "card";
    const form = document.createElement("div");
    form.className = "form";
    const inProv = createInput("text", { value: draft.provider || "" });
    inProv.addEventListener("input", () => { draft.provider = inProv.value.trim(); markDirty(); });
    form.appendChild(createRow("服务提供商", inProv));
    const inAcc = createInput("text", { value: draft.account || "" });
    inAcc.addEventListener("input", () => { draft.account = inAcc.value.trim(); markDirty(); });
    form.appendChild(createRow("账号", inAcc));
    const inPwd = createInput("password", { value: "****" });
    inPwd.addEventListener("input", () => { draft.password = inPwd.value; markDirty(); });
    form.appendChild(createRow("密码（未更改留空或 ****）", inPwd));
    const inTok = createInput("password", { value: "****" });
    inTok.addEventListener("input", () => { draft.apiToken = inTok.value; markDirty(); });
    form.appendChild(createRow("API Token（未更改留空或 ****）", inTok));
    const inDom = createInput("text", { value: draft.domain || "" });
    inDom.addEventListener("input", () => { draft.domain = inDom.value.trim(); markDirty(); });
    form.appendChild(createRow("域名", inDom));
    const inHost = createInput("text", { value: draft.hostname || "" });
    inHost.addEventListener("input", () => { draft.hostname = inHost.value.trim(); markDirty(); });
    form.appendChild(createRow("主机名", inHost));
    card.appendChild(form);
    grid.appendChild(card);

    mountUxBar(content, {
      onSave: async () => {
        const res = await saveSection("ddns", draft, { andGenerate: false });
        if (!res.saved) alert(`保存失败：${res.error?.message || "未知错误"}`);
      },
      onSaveAndGenerate: async () => {
        const res = await saveSection("ddns", draft, { andGenerate: true });
        if (!res.saved) alert(`保存失败：${res.error?.message || "未知错误"}`);
        else alert("已保存并提交生成任务（不应用运行态）");
      },
    });
  }

  async function renderQoS() {
    const content = clearMain();
    const grid = document.createElement("div");
    grid.className = "grid";
    content.appendChild(grid);
    const base = await loadConfigCached(false);
    const draft = deepClone(base.qos || { enable: false, uploadKbps: 0, downloadKbps: 0, rules: [] });
    if (!Array.isArray(draft.rules)) draft.rules = [];

    const card = document.createElement("div");
    card.className = "card";
    const form = document.createElement("div");
    form.className = "form";
    const chk = createInput("checkbox", { checked: !!draft.enable });
    chk.addEventListener("change", () => { draft.enable = chk.checked; markDirty(); });
    form.appendChild(createRow("启用 QoS", chk));
    const inUp = createInput("number", { value: draft.uploadKbps || 0, min: 0 });
    inUp.addEventListener("input", () => { draft.uploadKbps = Number(inUp.value || 0); markDirty(); });
    form.appendChild(createRow("上行带宽（kbps）", inUp));
    const inDown = createInput("number", { value: draft.downloadKbps || 0, min: 0 });
    inDown.addEventListener("input", () => { draft.downloadKbps = Number(inDown.value || 0); markDirty(); });
    form.appendChild(createRow("下行带宽（kbps）", inDown));
    card.appendChild(form);
    grid.appendChild(card);

    const rulesEditor = createArrayEditor({
      title: "设备规则（rules）",
      items: () => draft.rules,
      renderItem: (r, idx) => {
        const w = document.createElement("div");
        const inDev = createInput("text", { value: r?.device || r?.mac || r?.ip || "" });
        inDev.addEventListener("input", () => { draft.rules[idx].device = inDev.value.trim(); markDirty(); });
        const inUpK = createInput("number", { value: r?.upKbps || 0, min: 0 });
        inUpK.addEventListener("input", () => { draft.rules[idx].upKbps = Number(inUpK.value || 0); markDirty(); });
        const inDownK = createInput("number", { value: r?.downKbps || 0, min: 0 });
        inDownK.addEventListener("input", () => { draft.rules[idx].downKbps = Number(inDownK.value || 0); markDirty(); });
        const inPri = createSelect([["low","低"],["normal","普通"],["high","高"]], r?.priority || "normal");
        inPri.addEventListener("change", () => { draft.rules[idx].priority = inPri.value; markDirty(); });
        w.appendChild(createRow("设备（MAC/IP）", inDev));
        w.appendChild(createRow("上行（kbps）", inUpK));
        w.appendChild(createRow("下行（kbps）", inDownK));
        w.appendChild(createRow("优先级", inPri));
        return w;
      },
      onAdd: () => { draft.rules.push({ device: "", upKbps: 0, downKbps: 0, priority: "normal" }); },
    });
    grid.appendChild(rulesEditor);

    mountUxBar(content, {
      onSave: async () => {
        const res = await saveSection("qos", draft, { andGenerate: false });
        if (!res.saved) alert(`保存失败：${res.error?.message || "未知错误"}`);
      },
      onSaveAndGenerate: async () => {
        const res = await saveSection("qos", draft, { andGenerate: true });
        if (!res.saved) alert(`保存失败：${res.error?.message || "未知错误"}`);
        else alert("已保存并提交生成任务（不应用运行态）");
      },
    });
  }

  async function renderParental() {
    const content = clearMain();
    const grid = document.createElement("div");
    grid.className = "grid";
    content.appendChild(grid);
    const base = await loadConfigCached(false);
    const draft = deepClone(base.parental || { enable: false, rules: [] });
    if (!Array.isArray(draft.rules)) draft.rules = [];
    const card = document.createElement("div");
    card.className = "card";
    const form = document.createElement("div");
    form.className = "form";
    const chk = createInput("checkbox", { checked: !!draft.enable });
    chk.addEventListener("change", () => { draft.enable = chk.checked; markDirty(); });
    form.appendChild(createRow("启用家长控制", chk));
    card.appendChild(form);
    grid.appendChild(card);

    const rulesEditor = createArrayEditor({
      title: "规则（仅占位）",
      items: () => draft.rules,
      renderItem: (r, idx) => {
        const w = document.createElement("div");
        const inName = createInput("text", { value: r?.name || "" });
        inName.addEventListener("input", () => { draft.rules[idx].name = inName.value.trim(); markDirty(); });
        const inDev = createInput("text", { value: r?.device || "" });
        inDev.addEventListener("input", () => { draft.rules[idx].device = inDev.value.trim(); markDirty(); });
        const inDays = createInput("text", { value: r?.days || "Mon-Fri" });
        inDays.addEventListener("input", () => { draft.rules[idx].days = inDays.value.trim(); markDirty(); });
        const inStart = createInput("text", { value: r?.start || "21:00" });
        inStart.addEventListener("input", () => { draft.rules[idx].start = inStart.value.trim(); markDirty(); });
        const inEnd = createInput("text", { value: r?.end || "07:00" });
        inEnd.addEventListener("input", () => { draft.rules[idx].end = inEnd.value.trim(); markDirty(); });
        w.appendChild(createRow("名称", inName));
        w.appendChild(createRow("设备", inDev));
        w.appendChild(createRow("天（示例 Mon-Fri）", inDays));
        w.appendChild(createRow("开始时间", inStart));
        w.appendChild(createRow("结束时间", inEnd));
        return w;
      },
      onAdd: () => { draft.rules.push({ name: "", device: "", days: "Mon-Fri", start: "21:00", end: "07:00" }); },
    });
    grid.appendChild(rulesEditor);

    mountUxBar(content, {
      onSave: async () => {
        const res = await saveSection("parental", draft, { andGenerate: false });
        if (!res.saved) alert(`保存失败：${res.error?.message || "未知错误"}`);
      },
      onSaveAndGenerate: async () => {
        const res = await saveSection("parental", draft, { andGenerate: true });
        if (!res.saved) alert(`保存失败：${res.error?.message || "未知错误"}`);
        else alert("已保存并提交生成任务（不应用运行态）");
      },
    });
  }

  async function renderIPv6() {
    const content = clearMain();
    const grid = document.createElement("div");
    grid.className = "grid";
    content.appendChild(grid);
    const base = await loadConfigCached(false);
    const draft = deepClone(base.ipv6 || { enable: false, wan: { mode: "auto", addressCidr: "" }, lan: { enable: true, prefix: "" } });
    if (!draft.wan) draft.wan = { mode: "auto", addressCidr: "" };
    if (!draft.lan) draft.lan = { enable: true, prefix: "" };
    const card = document.createElement("div");
    card.className = "card";
    const form = document.createElement("div");
    form.className = "form";
    const chk = createInput("checkbox", { checked: !!draft.enable });
    chk.addEventListener("change", () => { draft.enable = chk.checked; markDirty(); });
    form.appendChild(createRow("启用 IPv6", chk));
    const selWan = createSelect([["auto","自动"],["static","静态"]], draft.wan.mode || "auto");
    selWan.addEventListener("change", () => { draft.wan.mode = selWan.value; markDirty(); refresh(); });
    form.appendChild(createRow("WAN 模式", selWan));
    const inWanCIDR = createInput("text", { value: draft.wan.addressCidr || "" });
    inWanCIDR.addEventListener("input", () => { draft.wan.addressCidr = inWanCIDR.value.trim(); markDirty(); });
    const rowWanCIDR = createRow("WAN 静态地址/CIDR", inWanCIDR);
    form.appendChild(rowWanCIDR);
    const chkLan = createInput("checkbox", { checked: !!draft.lan.enable });
    chkLan.addEventListener("change", () => { draft.lan.enable = chkLan.checked; markDirty(); });
    form.appendChild(createRow("LAN 启用 IPv6", chkLan));
    const inPrefix = createInput("text", { value: draft.lan.prefix || "" , placeholder: "fd00::/64"});
    inPrefix.addEventListener("input", () => { draft.lan.prefix = inPrefix.value.trim(); markDirty(); });
    form.appendChild(createRow("LAN 前缀/RA", inPrefix));
    card.appendChild(form);
    grid.appendChild(card);
    function refresh() { rowWanCIDR.style.display = (selWan.value === "static") ? "" : "none"; }
    refresh();

    mountUxBar(content, {
      onSave: async () => {
        const res = await saveSection("ipv6", draft, { andGenerate: false });
        if (!res.saved) alert(`保存失败：${res.error?.message || "未知错误"}`);
      },
      onSaveAndGenerate: async () => {
        const res = await saveSection("ipv6", draft, { andGenerate: true });
        if (!res.saved) alert(`保存失败：${res.error?.message || "未知错误"}`);
        else alert("已保存并提交生成任务（不应用运行态）");
      },
    });
  }

  async function renderSystem() {
    const content = clearMain();
    const grid = document.createElement("div");
    grid.className = "grid";
    content.appendChild(grid);
  let statusData = null;
  let healthData = null;
  const cfg = await loadConfigCached(false);
  try {
    const [s, h] = await Promise.allSettled([
      fetchJson("/api/v1/status"),
      fetchJson("/api/v1/health"),
    ]);
    if (s.status === "fulfilled") statusData = s.value;
    if (h.status === "fulfilled") healthData = h.value;
  } catch (_) {}
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

  // 可编辑：hostname/timezone
  const draft = deepClone(cfg.system || {});
  const sysCard = document.createElement("div");
  sysCard.className = "card";
  const form = document.createElement("div");
  form.className = "form";
  const inHost = createInput("text", { value: draft.hostname || "" });
  inHost.addEventListener("input", () => { draft.hostname = inHost.value.trim(); markDirty(); });
  const inTz = createInput("text", { value: draft.timezone || "" });
  inTz.addEventListener("input", () => { draft.timezone = inTz.value.trim(); markDirty(); });
  form.appendChild(createRow("主机名（保存生效）", inHost));
  form.appendChild(createRow("时区（保存生效）", inTz));
  sysCard.appendChild(form);
  grid.appendChild(sysCard);

  // 修改密码
  const pwdCard = document.createElement("div");
  pwdCard.className = "card";
  pwdCard.innerHTML = `
    <h3>修改密码</h3>
    <div class="form">
      <div class="form-row">
        <label>当前密码</label>
        <input id="pwd-cur" type="password" autocomplete="current-password" />
      </div>
      <div class="form-row">
        <label>新密码</label>
        <input id="pwd-new" type="password" autocomplete="new-password" />
      </div>
      <div class="form-row">
        <label>确认新密码</label>
        <input id="pwd-new2" type="password" autocomplete="new-password" />
      </div>
      <div class="form-actions">
        <button id="pwd-save" class="btn">修改密码</button>
      </div>
      <div id="pwd-msg" class="mt8 small muted"></div>
    </div>
  `;
  grid.appendChild(pwdCard);
  q("#pwd-save", pwdCard)?.addEventListener("click", async () => {
    const cur = q("#pwd-cur", pwdCard)?.value || "";
    const nw = q("#pwd-new", pwdCard)?.value || "";
    const nw2 = q("#pwd-new2", pwdCard)?.value || "";
    const msg = q("#pwd-msg", pwdCard);
    const setMsg = (t, ok=false) => { if (msg) { msg.textContent = t; msg.style.color = ok ? "var(--accent)" : "var(--muted)"; } };
    if (!nw || nw !== nw2) { setMsg("新密码不一致或为空"); return; }
    try {
      // 优先尝试 /session/password
      const body1 = { current: cur, new: nw, currentPassword: cur, newPassword: nw };
      let tried = false;
      try {
        tried = true;
        await fetchJson("/api/v1/session/password", { method: "POST", body: body1 });
        setMsg("密码已更新（可能需要重新登录）", true);
        return;
      } catch (e1) {
        if (!(e1?.status === 404 || e1?.status === 501 || e1?.status === 405)) throw e1;
      }
      try {
        await fetchJson("/api/v1/account/password", { method: "PUT", body: body1 });
        setMsg("密码已更新（可能需要重新登录）", true);
      } catch (e2) {
        if (e2?.status === 404 || e2?.status === 501) {
          setMsg("接口尚未提供");
          return;
        }
        throw e2;
      }
    } catch (e) {
      setMsg(`修改失败：${e?.status === 401 ? "未登录或会话已过期" : (e?.message || "未知错误")}`);
    }
  });

  // 备份 / 恢复
  const backupCard = document.createElement("div");
  backupCard.className = "card";
  backupCard.innerHTML = `
    <h3>配置备份 / 恢复</h3>
    <div class="form-actions">
      <button id="backup-btn" class="btn">下载备份</button>
    </div>
    <div class="form mt8">
      <div class="form-row">
        <label>恢复备份（可能覆盖配置，谨慎操作）</label>
        <input id="restore-file" type="file" />
      </div>
      <div class="form-actions">
        <button id="restore-btn" class="btn">上传并恢复</button>
      </div>
    </div>
    <div id="backup-msg" class="mt8 small muted"></div>
  `;
  grid.appendChild(backupCard);
  const setBackupMsg = (t, ok=false) => { const el = q("#backup-msg", backupCard); if (el) { el.textContent = t; el.style.color = ok ? "var(--accent)" : "var(--muted)"; } };
  q("#backup-btn", backupCard)?.addEventListener("click", async () => {
    setBackupMsg("正在请求备份...");
    try {
      const tryPaths = ["/api/v1/backup", "/api/v1/system/backup"];
      let resp = null;
      for (const p of tryPaths) {
        try {
          const r = await fetchRaw(p, { method: "GET" });
          if (r.ok) { resp = r; break; }
          if (r.status === 404 || r.status === 501) continue;
          throw new Error(`HTTP ${r.status}`);
        } catch (_) {}
      }
      if (!resp) { setBackupMsg("接口尚未提供"); return; }
      const blob = await resp.blob();
      const cd = resp.headers.get("Content-Disposition") || "";
      const m = /filename=\"?([^\";]+)\"?/i.exec(cd);
      const name = m?.[1] || `nixos-router-backup-${Date.now()}.tar.gz`;
      const a = document.createElement("a");
      a.href = URL.createObjectURL(blob);
      a.download = name;
      document.body.appendChild(a);
      a.click();
      a.remove();
      setBackupMsg(`已下载：${name}`, true);
    } catch (e) {
      setBackupMsg(`下载失败：${e?.message || "未知错误"}`);
    }
  });
  q("#restore-btn", backupCard)?.addEventListener("click", async () => {
    const f = q("#restore-file", backupCard)?.files?.[0];
    if (!f) { setBackupMsg("请选择备份文件"); return; }
    const ok = confirm("将上传并恢复备份，可能覆盖当前配置。确定继续吗？");
    if (!ok) return;
    setBackupMsg("正在上传并恢复...");
    try {
      const fd = new FormData();
      fd.append("file", f, f.name);
      let resp = await fetchRaw("/api/v1/backup/restore", { method: "POST", body: fd });
      if (resp.status === 404 || resp.status === 501) {
        resp = await fetchRaw("/api/v1/system/restore", { method: "POST", body: fd });
      }
      if (resp.status === 404 || resp.status === 501) { setBackupMsg("接口尚未提供"); return; }
      if (!resp.ok && resp.status !== 202) {
        const t = await resp.text().catch(() => "");
        throw new Error(t || `HTTP ${resp.status}`);
      }
      setBackupMsg("已提交恢复（可能需要稍候）", true);
    } catch (e) {
      setBackupMsg(`恢复失败：${e?.message || "未知错误"}`);
    }
  });
    // 生成配置（不应用运行态）
    const applyCard = document.createElement("div");
    applyCard.className = "card";
    applyCard.innerHTML = `
      <h3>生成配置（不应用运行态）</h3>
      <p class="muted small">
        该操作只会在服务器 <code>stateDir</code> 下生成/更新配置片段，
        不会重载网络服务或切换当前运行态。后续应用将于下个里程碑实现。
      </p>
      <div class="form-actions">
        <button id="apply-generate-btn" class="btn-primary" type="button">生成配置（不应用运行态）</button>
      </div>
      <div id="apply-result" class="mt8"></div>
    `;
    grid.appendChild(applyCard);

    const resultEl = applyCard.querySelector("#apply-result");
    const btn = applyCard.querySelector("#apply-generate-btn");
    btn?.addEventListener("click", async () => {
      btn.setAttribute("disabled", "true");
      resultEl.innerHTML = `<span class="muted">正在请求生成...</span>`;
      try {
        // 最小对接：POST /api/v1/apply（不带请求体，后端默认为 generate-only）
        const res = await fetchJson("/api/v1/apply", { method: "POST" });
        const jobId = res?.jobId || res?.id;
        const mode = res?.mode || "generate-only";
        const appliedRuntime = res?.appliedRuntime;

        const head = document.createElement("div");
        head.className = "kv";
        const headRows = [
          ["jobId", String(jobId ?? "-")],
          ["mode", String(mode)],
          ["appliedRuntime", String(appliedRuntime ?? false)],
        ].map(([k, v]) => `<div class="k">${escapeHtml(k)}</div><div class="v">${escapeHtml(v)}</div>`).join("");
        head.innerHTML = headRows;

        // 展示返回 JSON
        const raw = document.createElement("pre");
        raw.className = "json-view";
        raw.textContent = JSON.stringify(res, null, 2);

        resultEl.innerHTML = "";
        const resCard = document.createElement("div");
        resCard.className = "card";
        resCard.innerHTML = `<h3>作业已提交</h3>`;
        resCard.appendChild(head);
        resCard.appendChild(raw);
        resultEl.appendChild(resCard);

        // 单次再拉取作业状态
        if (jobId) {
          try {
            const job = await fetchJson(`/api/v1/jobs/${encodeURIComponent(jobId)}`);
            const jobCard = document.createElement("div");
            jobCard.className = "card";
            const status = job?.status || job?.state || "-";
            const errMsg = job?.error || job?.err || null;
            const lines = [
              ["状态", String(status)],
              ...(job?.startedAt ? [["开始时间", String(job.startedAt)]] : []),
              ...(job?.finishedAt ? [["完成时间", String(job.finishedAt)]] : []),
              ...(errMsg ? [["错误", String(errMsg)]] : []),
            ];
            jobCard.appendChild(renderKeyValueCard("作业状态", lines));
            const jobRaw = document.createElement("pre");
            jobRaw.className = "json-view";
            jobRaw.textContent = JSON.stringify(job, null, 2);
            jobCard.appendChild(jobRaw);
            resultEl.appendChild(jobCard);
          } catch (e) {
            const errCard = document.createElement("div");
            errCard.className = "card";
            errCard.innerHTML = `<h3>作业状态查询失败</h3><p class="muted">${escapeHtml(e?.message || "未知错误")}</p>`;
            resultEl.appendChild(errCard);
          }
        }
      } catch (e) {
        const err = document.createElement("div");
        err.className = "error";
        err.textContent = `生成失败：${e?.status === 401 ? "未登录或会话已过期" : (e?.message || "未知错误")}`;
        resultEl.innerHTML = "";
        resultEl.appendChild(err);
      } finally {
        btn.removeAttribute("disabled");
      }
    });

    // 最近作业
    try {
      const jobs = await fetchJson("/api/v1/jobs?limit=5");
      const list = Array.isArray(jobs) ? jobs : (jobs?.jobs || []);
      if (Array.isArray(list) && list.length) {
        const jcard = document.createElement("div");
        jcard.className = "card";
        const items = list.map((j) => {
          const kvv = [
            ["id", j?.id ?? j?.jobId ?? "-"],
            ["状态", j?.status || j?.state || "-"],
            ...(j?.mode ? [["mode", j.mode]] : []),
            ...(j?.appliedRuntime != null ? [["appliedRuntime", String(j.appliedRuntime)]] : []),
            ...(j?.error ? [["错误", j.error]] : []),
          ];
          return `<div class="kv">${kvv.map(([k,v]) => `<div class="k">${escapeHtml(k)}</div><div class="v">${escapeHtml(String(v))}</div>`).join("")}</div>`;
        }).join("<hr class=\"sep\" />");
        jcard.innerHTML = `<h3>最近作业</h3>${items}`;
        grid.appendChild(jcard);
      }
    } catch (_) {}

  // 审计日志（若提供）
  try {
    const audit = await fetchJson("/api/v1/audit?limit=50");
    const arr = Array.isArray(audit) ? audit : (audit?.audit || audit?.items || []);
    if (Array.isArray(arr) && arr.length) {
      const acard = document.createElement("div");
      acard.className = "card";
      const rowsA = arr.map((e) => {
        const kv = [
          ["ID", e?.id || e?.uuid || "-"],
          ["时间", e?.at || e?.time || e?.ts || e?.timestamp || "-"],
          ["用户", e?.actor || e?.user || "-"],
          ["动作", e?.action || e?.event || "-"],
          ["结果", e?.result || e?.status || "-"],
          ["对象", e?.resource || e?.path || "-"],
          ["详情", e?.detail || e?.message || "-"],
          ["IP", e?.ip || e?.addr || "-"],
        ];
        return `<div class="kv">${kv.map(([k,v]) => `<div class="k">${escapeHtml(k)}</div><div class="v">${escapeHtml(String(v))}</div>`).join("")}</div>`;
      }).join("<hr class=\"sep\" />");
      acard.innerHTML = `<h3>审计日志</h3>${rowsA}`;
      grid.appendChild(acard);
    }
  } catch (e) {
    if (e?.status === 404 || e?.status === 501) {
      const acard = document.createElement("div");
      acard.className = "card";
      acard.innerHTML = `<h3>审计日志</h3><p class="muted">接口尚未提供</p>`;
      grid.appendChild(acard);
    }
  }

  // 重启（若提供）
  const reboot = document.createElement("div");
  reboot.className = "card";
  reboot.innerHTML = `
    <h3>系统重启</h3>
    <div class="form-actions">
      <button id="reboot-btn" class="btn">发送重启命令</button>
    </div>
    <div id="reboot-msg" class="mt8 small muted"></div>
  `;
  grid.appendChild(reboot);
  const setRebootMsg = (t, ok=false) => { const el = q("#reboot-msg", reboot); if (el) { el.textContent = t; el.style.color = ok ? "var(--accent)" : "var(--muted)"; } };
  q("#reboot-btn", reboot)?.addEventListener("click", async () => {
    const ok = confirm("确定要重启系统吗？这将中断网络连接。");
    if (!ok) return;
    setRebootMsg("正在发送重启命令...");
    try {
      const resp = await fetchRaw("/api/v1/system/reboot", { method: "POST" });
      if (resp.status === 404 || resp.status === 501) {
        setRebootMsg("接口尚未提供");
        return;
      }
      if (!resp.ok && resp.status !== 202) {
        const t = await resp.text().catch(() => "");
        throw new Error(t || `HTTP ${resp.status}`);
      }
      setRebootMsg("已发送重启命令（设备可能即将重启）", true);
    } catch (e) {
      setRebootMsg(`发送失败：${e?.message || "未知错误"}`);
    }
  });

    grid.appendChild(renderJSONCard("配置 JSON", { config: cfg }));
    if (statusData) grid.appendChild(renderJSONCard("状态 JSON", statusData));
    if (healthData) grid.appendChild(renderJSONCard("健康 JSON", healthData));

    // UX bar for system save
    mountUxBar(content, {
      onSave: async () => {
        const nextAll = deepClone(cfg);
        nextAll.system = draft;
        try {
          const res = await putConfig(nextAll);
          state.configCache = res?.config || nextAll;
          clearDirty();
          alert("已保存系统配置");
        } catch (e) {
          alert(`保存失败：${e?.message || "未知错误"}`);
        }
      },
      onSaveAndGenerate: async () => {
        const nextAll = deepClone(cfg);
        nextAll.system = draft;
        try {
          await putConfig(nextAll);
          state.configCache = nextAll;
          clearDirty();
          await fetchJson("/api/v1/apply", { method: "POST" });
          alert("已保存并提交生成任务（不应用运行态）");
        } catch (e) {
          alert(`保存失败：${e?.message || "未知错误"}`);
        }
      },
    });
  }

  async function renderClients() {
    const content = clearMain();
    const grid = document.createElement("div");
    grid.className = "grid";
    content.appendChild(grid);
    try {
    const data = await fetchJson("/api/v1/clients");
    const list = Array.isArray(data) ? data : (data?.clients || data?.items || []);
    const src = data?.source || "-";
    if (!Array.isArray(list) || list.length === 0) {
      grid.appendChild(renderKeyValueCard("客户端（只读）", [["状态", "无数据"], ["来源", String(src)]]));
      // Fallback: staticLeases
      const cfg = await loadConfigCached(false);
      const leases = Array.isArray(cfg?.lan?.staticLeases) ? cfg.lan.staticLeases : [];
      if (leases.length) {
        const card = document.createElement("div");
        card.className = "card";
        const items = leases.map((c) => {
          const rows = [
            ["IP", c?.ip || "-"],
            ["MAC", c?.mac || "-"],
            ["主机名", c?.hostname || "-"],
          ].map(([k,v]) => `<div class="k">${escapeHtml(k)}</div><div class="v">${escapeHtml(String(v))}</div>`).join("");
          return `<div class="kv">${rows}</div>`;
        }).join("<hr class=\"sep\" />");
        card.innerHTML = `<h3>静态租约（来自配置）</h3>${items}`;
        grid.appendChild(card);
      }
    } else {
      const card = document.createElement("div");
      card.className = "card";
      const items = list.map((c) => {
        const rows = [
          ["IP", c?.ip || "-"],
          ["MAC", c?.mac || "-"],
          ["主机名", c?.hostname || "-"],
          ["来源", c?.source || src || "-"],
        ].map(([k,v]) => `<div class="k">${escapeHtml(k)}</div><div class="v">${escapeHtml(String(v))}</div>`).join("");
        return `<div class="kv">${rows}</div>`;
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
    configCache: null,
    pageDirty: false,
    prevHash: null,
    suppressHashRevert: false,
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
      { id: "ddns", label: "DDNS", path: "#/ddns" },
      { id: "qos", label: "QoS", path: "#/qos" },
      { id: "parental", label: "家长控制", path: "#/parental" },
      { id: "ipv6", label: "IPv6", path: "#/ipv6" },
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

  function deepClone(obj) {
    return JSON.parse(JSON.stringify(obj ?? {}));
  }

  async function loadConfigCached(force = false) {
    if (!state.configCache || force) {
      const res = await fetchJson("/api/v1/config");
      state.configCache = res?.config || res || {};
    }
    return state.configCache;
  }

  function markDirty() {
    if (!state.pageDirty) {
      state.pageDirty = true;
      const ind = q("#ux-dirty");
      if (ind) ind.classList.remove("hidden");
    }
  }
  function clearDirty() {
    state.pageDirty = false;
    const ind = q("#ux-dirty");
    if (ind) ind.classList.add("hidden");
  }

  function mountUxBar(container, { onSave, onSaveAndGenerate }) {
    const bar = document.createElement("div");
    bar.className = "ux-bar";
    bar.innerHTML = `
      <span id="ux-dirty" class="dirty-indicator hidden">有未保存的更改</span>
      <span class="spacer"></span>
      <button id="ux-save" class="btn">保存</button>
      <button id="ux-save-generate" class="btn primary">保存并生成配置</button>
    `;
    container.appendChild(bar);
    q("#ux-save", bar)?.addEventListener("click", async () => {
      await onSave?.();
    });
    q("#ux-save-generate", bar)?.addEventListener("click", async () => {
      await onSaveAndGenerate?.();
    });
  }

  async function putConfig(fullConfig) {
    // 返回后端响应；错误由调用方显示
    return await fetchJson("/api/v1/config", { method: "PUT", body: fullConfig });
  }

  async function putPlugin(id, pluginDraft) {
    // 优先使用每插件端点；404 时由调用方决定回退
    const payload = {
      enabled: !!(pluginDraft?.enable ?? pluginDraft?.enabled),
      config: pluginDraft?.config ?? {},
    };
    preserveSecrets(payload);
    return await fetchJson(`/api/v1/plugins/${encodeURIComponent(id)}`, {
      method: "PUT",
      body: payload,
    });
  }

  function preserveSecrets(obj) {
    // 将空字符串也视为“保持原值”
    const walk = (node) => {
      if (!node || typeof node !== "object") return;
      for (const k of Object.keys(node)) {
        const v = node[k];
        if (typeof v === "string") {
          const lk = k.toLowerCase();
          if (lk.includes("password") || lk.includes("psk") || lk.includes("secret") || lk.includes("token")) {
            if (v === "" || v === "****") node[k] = "****";
          }
        } else if (v && typeof v === "object") {
          walk(v);
        }
      }
    };
    walk(obj);
  }

  async function saveSection(sectionKey, draft, { andGenerate = false } = {}) {
    try {
      const base = await loadConfigCached(false);
      const next = deepClone(base);
      // 支持嵌套路径：如 "plugins.ddns"
      const parts = sectionKey.split(".");
      let cur = next;
      for (let i = 0; i < parts.length - 1; i++) {
        const p = parts[i];
        if (!cur[p] || typeof cur[p] !== "object") cur[p] = {};
        cur = cur[p];
      }
      cur[parts[parts.length - 1]] = draft;
      preserveSecrets(next);
      const res = await putConfig(next);
      state.configCache = res?.config || next; // 若后端返回保存后的 config，则用之
      clearDirty();
      if (andGenerate) {
        const applyRes = await fetchJson("/api/v1/apply", { method: "POST" });
        return { saved: true, apply: applyRes };
      }
      return { saved: true };
    } catch (e) {
      return { saved: false, error: e };
    }
  }

  function createRow(label, inputEl) {
    const row = document.createElement("div");
    row.className = "form-row";
    const lab = document.createElement("label");
    lab.textContent = label;
    row.appendChild(lab);
    row.appendChild(inputEl);
    return row;
  }

  function createInput(type = "text", opts = {}) {
    const el = document.createElement("input");
    el.type = type;
    if (opts.placeholder) el.placeholder = opts.placeholder;
    if (opts.value != null) el.value = String(opts.value);
    if (opts.checked != null) el.checked = !!opts.checked;
    if (opts.min != null) el.min = String(opts.min);
    if (opts.max != null) el.max = String(opts.max);
    return el;
  }

  function createSelect(options, value) {
    const sel = document.createElement("select");
    for (const [val, text] of options) {
      const opt = document.createElement("option");
      opt.value = val;
      opt.textContent = text;
      if (val === value) opt.selected = true;
      sel.appendChild(opt);
    }
    return sel;
  }

  function createArrayEditor({ title, items, renderItem, onAdd }) {
    const card = document.createElement("div");
    card.className = "card";
    const header = document.createElement("div");
    header.style.display = "flex";
    header.style.justifyContent = "space-between";
    header.style.alignItems = "center";
    const h3 = document.createElement("h3");
    h3.textContent = title;
    const addBtn = document.createElement("button");
    addBtn.className = "btn";
    addBtn.textContent = "新增";
    addBtn.addEventListener("click", () => {
      onAdd?.();
      rerender();
      markDirty();
    });
    header.appendChild(h3);
    header.appendChild(addBtn);
    const body = document.createElement("div");
    const rerender = () => {
      body.innerHTML = "";
      (items() || []).forEach((it, idx) => {
        const row = document.createElement("div");
        row.style.borderTop = "1px dashed var(--border)";
        row.style.paddingTop = "8px";
        const del = document.createElement("button");
        del.className = "btn";
        del.textContent = "删除";
        del.style.float = "right";
        del.addEventListener("click", () => {
          (items()).splice(idx, 1);
          rerender();
          markDirty();
        });
        row.appendChild(del);
        row.appendChild(renderItem(it, idx));
        body.appendChild(row);
      });
    };
    rerender();
    card.appendChild(header);
    card.appendChild(body);
    return card;
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

  async function fetchRaw(path, init = {}) {
    const url = `${apiBase}${path}`;
    const {
      method = "GET",
      body,
      headers = {},
      ...rest
    } = init || {};
    const resp = await withTimeout(fetch(url, {
      method,
      credentials: "include",
      headers,
      body,
      ...rest,
    }));
    if (resp.status === 401) {
      onUnauthorized();
      const err401 = new Error("unauthorized");
      err401.status = 401;
      throw err401;
    }
    return resp;
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
      "ddns": "DDNS",
      "qos": "QoS",
      "parental": "家长控制",
      "ipv6": "IPv6",
      "plugins": "插件",
      "adblock": "广告拦截",
      "traffic": "流量监控",
      "mihomo": "Mihomo",
      "vlan": "VLAN",
      "tailscale": "Tailscale",
      "zerotier": "ZeroTier",
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
  const [base, status] = await Promise.all([
    loadConfigCached(false),
    fetchJson("/api/v1/status").catch(() => ({})),
  ]);
  const ifs = Array.isArray(status?.interfaces) ? status.interfaces : [];
  const wanIf = ifs.find(i => i?.role === "wan") || {};
  const draft = deepClone(base.wan || { mode: "dhcp" });
  if (!draft.static) draft.static = {};
  if (!draft.pppoe) draft.pppoe = {};

  const card = document.createElement("div");
  card.className = "card";
  const form = document.createElement("div");
  form.className = "form";
  const selMode = createSelect([["dhcp","DHCP"],["static","静态"],["pppoe","PPPoE"]], draft.mode || "dhcp");
  selMode.addEventListener("change", () => { draft.mode = selMode.value; refreshVisibility(); markDirty(); });
  form.appendChild(createRow("模式", selMode));

  const inIface = createInput("text", { value: draft.interface || "" });
  inIface.addEventListener("input", () => { draft.interface = inIface.value.trim(); markDirty(); });
  form.appendChild(createRow("接口名", inIface));

  // static fields
  const inAddr = createInput("text", { value: draft.static.addressCidr || "", placeholder: "203.0.113.10/24" });
  inAddr.addEventListener("input", () => { draft.static.addressCidr = inAddr.value.trim(); markDirty(); });
  const inGw = createInput("text", { value: draft.static.gateway || "" });
  inGw.addEventListener("input", () => { draft.static.gateway = inGw.value.trim(); markDirty(); });
  const inDns = createInput("text", { value: Array.isArray(draft.static.dns) ? draft.static.dns.join(", ") : "" , placeholder: "1.1.1.1,8.8.8.8" });
  inDns.addEventListener("input", () => {
    draft.static.dns = inDns.value.split(",").map(s => s.trim()).filter(Boolean);
    markDirty();
  });
  const rowStaticAddr = createRow("静态地址/CIDR", inAddr);
  const rowStaticGw = createRow("静态网关", inGw);
  const rowStaticDns = createRow("静态 DNS（逗号分隔）", inDns);

  // pppoe fields
  const inUser = createInput("text", { value: draft.pppoe.username || "" });
  inUser.addEventListener("input", () => { draft.pppoe.username = inUser.value.trim(); markDirty(); });
  const inPwd = createInput("password", { value: "****" });
  inPwd.addEventListener("input", () => { draft.pppoe.password = inPwd.value; markDirty(); });
  const rowPPPoEUser = createRow("PPPoE 用户名", inUser);
  const rowPPPoEPwd = createRow("PPPoE 密码（未更改留空或 ****）", inPwd);

  form.appendChild(rowStaticAddr);
  form.appendChild(rowStaticGw);
  form.appendChild(rowStaticDns);
  form.appendChild(rowPPPoEUser);
  form.appendChild(rowPPPoEPwd);

  // Status summary
  const summary = [];
  if (wanIf.name) summary.push(["当前接口", wanIf.name]);
  if (wanIf.mode) summary.push(["当前模式", wanIf.mode]);
  if (wanIf.up != null) summary.push(["链路", wanIf.up ? "已连接" : "未连接"]);
  card.appendChild(form);
  grid.appendChild(card);
  if (summary.length) grid.appendChild(renderKeyValueCard("当前状态（只读）", summary));

  // UX bar
  mountUxBar(content, {
    onSave: async () => {
      const res = await saveSection("wan", draft, { andGenerate: false });
      if (!res.saved) alert(`保存失败：${res.error?.message || "未知错误"}`);
    },
    onSaveAndGenerate: async () => {
      const res = await saveSection("wan", draft, { andGenerate: true });
      if (!res.saved) {
        alert(`保存失败：${res.error?.message || "未知错误"}`);
      } else {
        alert("已保存并提交生成任务（不应用运行态）");
      }
    },
  });

  function refreshVisibility() {
    const isStatic = (selMode.value === "static");
    const isPPPoE = (selMode.value === "pppoe");
    rowStaticAddr.style.display = isStatic ? "" : "none";
    rowStaticGw.style.display = isStatic ? "" : "none";
    rowStaticDns.style.display = isStatic ? "" : "none";
    rowPPPoEUser.style.display = isPPPoE ? "" : "none";
    rowPPPoEPwd.style.display = isPPPoE ? "" : "none";
  }
  refreshVisibility();
}

async function renderLAN() {
  const content = clearMain();
  const grid = document.createElement("div");
  grid.className = "grid";
  content.appendChild(grid);
  const [base, status] = await Promise.all([
    loadConfigCached(false),
    fetchJson("/api/v1/status").catch(() => ({})),
  ]);
  const lanIf = (Array.isArray(status?.interfaces) ? status.interfaces : []).find(i => i?.role === "lan") || {};
  const draft = deepClone(base.lan || { bridgeName: "br-lan", ipv4Cidr: "", dhcp: { enable: true } });
  if (!draft.dhcp) draft.dhcp = { enable: true };
  if (!Array.isArray(draft.ports)) draft.ports = [];
  if (!Array.isArray(draft.staticLeases)) draft.staticLeases = [];

  const card = document.createElement("div");
  card.className = "card";
  const form = document.createElement("div");
  form.className = "form";

  const inBridge = createInput("text", { value: draft.bridgeName || "" });
  inBridge.addEventListener("input", () => { draft.bridgeName = inBridge.value.trim(); markDirty(); });
  form.appendChild(createRow("桥（Bridge）", inBridge));

  const inCIDR = createInput("text", { value: draft.ipv4Cidr || "", placeholder: "192.168.1.1/24" });
  inCIDR.addEventListener("input", () => { draft.ipv4Cidr = inCIDR.value.trim(); markDirty(); });
  form.appendChild(createRow("IPv4 CIDR", inCIDR));

  // ports[]
  const portsEditor = createArrayEditor({
    title: "端口（ports）",
    items: () => draft.ports,
    renderItem: (it, idx) => {
      const wrap = document.createElement("div");
      const inp = createInput("text", { value: it || "" });
      inp.addEventListener("input", () => { draft.ports[idx] = inp.value.trim(); markDirty(); });
      wrap.appendChild(createRow(`端口 #${idx+1}`, inp));
      return wrap;
    },
    onAdd: () => { draft.ports.push(""); },
  });

  // DHCP
  const chkDHCP = createInput("checkbox", { checked: !!draft.dhcp.enable });
  chkDHCP.addEventListener("change", () => { draft.dhcp.enable = chkDHCP.checked; markDirty(); });
  form.appendChild(createRow("启用 DHCP", chkDHCP));
  const inStart = createInput("text", { value: draft.dhcp.rangeStart || "" });
  inStart.addEventListener("input", () => { draft.dhcp.rangeStart = inStart.value.trim(); markDirty(); });
  const inEnd = createInput("text", { value: draft.dhcp.rangeEnd || "" });
  inEnd.addEventListener("input", () => { draft.dhcp.rangeEnd = inEnd.value.trim(); markDirty(); });
  const inLease = createInput("number", { value: draft.dhcp.leaseMins ?? 1440, min: 1 });
  inLease.addEventListener("input", () => { draft.dhcp.leaseMins = Number(inLease.value || 0); markDirty(); });
  form.appendChild(createRow("DHCP 起始", inStart));
  form.appendChild(createRow("DHCP 结束", inEnd));
  form.appendChild(createRow("租约（分钟）", inLease));

  // staticLeases[]
  const leasesEditor = createArrayEditor({
    title: "静态租约（staticLeases）",
    items: () => draft.staticLeases,
    renderItem: (it, idx) => {
      const w = document.createElement("div");
      const inIP = createInput("text", { value: it?.ip || "" });
      inIP.addEventListener("input", () => { draft.staticLeases[idx].ip = inIP.value.trim(); markDirty(); });
      const inMAC = createInput("text", { value: it?.mac || "" });
      inMAC.addEventListener("input", () => { draft.staticLeases[idx].mac = inMAC.value.trim(); markDirty(); });
      const inHost = createInput("text", { value: it?.hostname || "" });
      inHost.addEventListener("input", () => { draft.staticLeases[idx].hostname = inHost.value.trim(); markDirty(); });
      w.appendChild(createRow("IP", inIP));
      w.appendChild(createRow("MAC", inMAC));
      w.appendChild(createRow("主机名", inHost));
      return w;
    },
    onAdd: () => { draft.staticLeases.push({ ip: "", mac: "", hostname: "" }); },
  });

  card.appendChild(form);
  grid.appendChild(card);
  grid.appendChild(portsEditor);
  grid.appendChild(leasesEditor);

  // 状态只读
  const rows = [];
  if (lanIf.up != null) rows.push(["链路", lanIf.up ? "已连接" : "未连接"]);
  if (rows.length) grid.appendChild(renderKeyValueCard("当前状态（只读）", rows));

  mountUxBar(content, {
    onSave: async () => {
      const res = await saveSection("lan", draft, { andGenerate: false });
      if (!res.saved) alert(`保存失败：${res.error?.message || "未知错误"}`);
    },
    onSaveAndGenerate: async () => {
      const res = await saveSection("lan", draft, { andGenerate: true });
      if (!res.saved) alert(`保存失败：${res.error?.message || "未知错误"}`);
      else alert("已保存并提交生成任务（不应用运行态）");
    },
  });
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
    "plugins": () => renderPlugins(),
    "ddns": () => renderDDNS(),
    "qos": () => renderQoS(),
    "parental": () => renderParental(),
    "ipv6": () => renderIPv6(),
    // Plugin-specific top-level routes
    "adblock": () => renderPluginAdblock(),
    "traffic": () => renderPluginTraffic(),
    "mihomo": () => renderPluginMihomo(),
    "vlan": () => renderPluginVlan(),
    "tailscale": () => renderPluginTailscale(),
    "zerotier": () => renderPluginZerotier(),
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
    // Support generic plugin route: #/plugins/<name>
    if (id.startsWith("plugins/")) {
      const name = id.split("/")[1] || "";
      await renderPluginGeneric(name);
      return;
    }
    const fn = routes[id] || (() => renderPlaceholder(id));
    await fn();
  }

  function initEvents() {
  state.prevHash = location.hash || "#/overview";
  window.addEventListener("beforeunload", (e) => {
    if (state.pageDirty) {
      e.preventDefault();
      e.returnValue = "";
    }
  });
  window.addEventListener("hashchange", (e) => {
    if (state.pageDirty && !state.suppressHashRevert) {
      const ok = confirm("有未保存的更改，确定离开本页吗？");
      if (!ok) {
        state.suppressHashRevert = true;
        location.hash = state.prevHash;
        setTimeout(() => { state.suppressHashRevert = false; }, 0);
        return;
      }
      clearDirty();
    }
    state.prevHash = location.hash || "#/overview";
    renderRoute();
  });
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

  // Plugin specific pages (typed where reasonable; otherwise fallback to generic)
  async function renderPluginAdblock() {
    return renderPluginGeneric("adblock");
  }
  async function renderPluginTraffic() {
    return renderPluginGeneric("traffic");
  }
  async function renderPluginMihomo() {
    return renderPluginGeneric("mihomo");
  }
  async function renderPluginVlan() {
    const cfg = await loadConfigCached(false);
    const plug = deepClone((cfg.plugins || {})["vlan"] || null);
    if (!plug || !plug.config) return renderPluginGeneric("vlan");
    const content = clearMain();
    const grid = document.createElement("div");
    grid.className = "grid";
    content.appendChild(grid);
    const draft = plug;
    if (!Array.isArray(draft.config.vlans)) draft.config.vlans = [];
    const card = document.createElement("div");
    card.className = "card";
    const form = document.createElement("div");
    form.className = "form";
    const chk = createInput("checkbox", { checked: !!draft.enable });
    chk.addEventListener("change", () => { draft.enable = chk.checked; markDirty(); });
    form.appendChild(createRow("启用插件", chk));
    card.appendChild(form);
    grid.appendChild(card);
    const vlEditor = createArrayEditor({
      title: "VLAN 列表",
      items: () => draft.config.vlans,
      renderItem: (v, idx) => {
        const w = document.createElement("div");
        const inId = createInput("number", { value: v?.id || 0, min: 1, max: 4094 });
        inId.addEventListener("input", () => { draft.config.vlans[idx].id = Number(inId.value || 0); markDirty(); });
        const inIface = createInput("text", { value: v?.iface || "" });
        inIface.addEventListener("input", () => { draft.config.vlans[idx].iface = inIface.value.trim(); markDirty(); });
        const inCIDR = createInput("text", { value: v?.cidr || "" });
        inCIDR.addEventListener("input", () => { draft.config.vlans[idx].cidr = inCIDR.value.trim(); markDirty(); });
        w.appendChild(createRow("VLAN ID", inId));
        w.appendChild(createRow("接口", inIface));
        w.appendChild(createRow("子网 CIDR", inCIDR));
        return w;
      },
      onAdd: () => { draft.config.vlans.push({ id: 10, iface: "", cidr: "" }); },
    });
    grid.appendChild(vlEditor);
    mountUxBar(content, {
      onSave: async () => {
        try {
          await putPlugin("vlan", draft);
          alert("已保存");
        } catch (e) {
          if (e?.status === 404) {
            const res = await saveSection("plugins.vlan", draft, { andGenerate: false });
            if (!res.saved) alert(`保存失败：${res.error?.message || "未知错误"}`); else alert("已保存");
          } else {
            alert(`保存失败：${e?.message || "未知错误"}`);
          }
        }
      },
      onSaveAndGenerate: async () => {
        try {
          await putPlugin("vlan", draft);
          await fetchJson("/api/v1/apply", { method: "POST" });
          alert("已保存并提交生成任务（不应用运行态）");
        } catch (e) {
          if (e?.status === 404) {
            const res = await saveSection("plugins.vlan", draft, { andGenerate: true });
            if (!res.saved) alert(`保存失败：${res.error?.message || "未知错误"}`); else alert("已保存并提交生成任务（不应用运行态）");
          } else {
            alert(`保存失败：${e?.message || "未知错误"}`);
          }
        }
      },
    });
  }
  async function renderPluginTailscale() {
    const cfg = await loadConfigCached(false);
    const plug = deepClone((cfg.plugins || {})["tailscale"] || null);
    if (!plug || !plug.config) return renderPluginGeneric("tailscale");
    const content = clearMain();
    const grid = document.createElement("div");
    grid.className = "grid";
    content.appendChild(grid);
    const draft = plug;
    const card = document.createElement("div");
    card.className = "card";
    const form = document.createElement("div");
    form.className = "form";
    const chk = createInput("checkbox", { checked: !!draft.enable });
    chk.addEventListener("change", () => { draft.enable = chk.checked; markDirty(); });
    form.appendChild(createRow("启用插件", chk));
    const selMode = createSelect([["official","官方"],["selfhost","自建"],["headscale","Headscale"]], draft.config.mode || "official");
    selMode.addEventListener("change", () => { draft.config.mode = selMode.value; markDirty(); refreshT(); });
    form.appendChild(createRow("模式", selMode));
    const inLogin = createInput("text", { value: draft.config.loginServer || "" , placeholder: "https://login.tailscale.com"});
    inLogin.addEventListener("input", () => { draft.config.loginServer = inLogin.value.trim(); markDirty(); });
    const rowLogin = createRow("登录服务器", inLogin);
    const inHS = createInput("text", { value: draft.config.headscaleUrl || "" , placeholder: "https://headscale.example.com"});
    inHS.addEventListener("input", () => { draft.config.headscaleUrl = inHS.value.trim(); markDirty(); });
    const rowHS = createRow("Headscale URL", inHS);
    const inKey = createInput("password", { value: "****" });
    inKey.addEventListener("input", () => { draft.config.authKey = inKey.value; markDirty(); });
    const rowKey = createRow("AuthKey（未更改留空或 ****）", inKey);
    form.appendChild(rowLogin);
    form.appendChild(rowHS);
    form.appendChild(rowKey);
    card.appendChild(form);
    grid.appendChild(card);
    function refreshT() {
      const m = selMode.value;
      rowLogin.style.display = (m === "official" || m === "selfhost") ? "" : "none";
      rowHS.style.display = (m === "headscale") ? "" : "none";
    }
    refreshT();
    mountUxBar(content, {
      onSave: async () => {
        try {
          await putPlugin("tailscale", draft);
          alert("已保存");
        } catch (e) {
          if (e?.status === 404) {
            const res = await saveSection("plugins.tailscale", draft, { andGenerate: false });
            if (!res.saved) alert(`保存失败：${res.error?.message || "未知错误"}`); else alert("已保存");
          } else {
            alert(`保存失败：${e?.message || "未知错误"}`);
          }
        }
      },
      onSaveAndGenerate: async () => {
        try {
          await putPlugin("tailscale", draft);
          await fetchJson("/api/v1/apply", { method: "POST" });
          alert("已保存并提交生成任务（不应用运行态）");
        } catch (e) {
          if (e?.status === 404) {
            const res = await saveSection("plugins.tailscale", draft, { andGenerate: true });
            if (!res.saved) alert(`保存失败：${res.error?.message || "未知错误"}`); else alert("已保存并提交生成任务（不应用运行态）");
          } else {
            alert(`保存失败：${e?.message || "未知错误"}`);
          }
        }
      },
    });
  }
  async function renderPluginZerotier() {
    const cfg = await loadConfigCached(false);
    const plug = deepClone((cfg.plugins || {})["zerotier"] || null);
    if (!plug || !plug.config) return renderPluginGeneric("zerotier");
    const content = clearMain();
    const grid = document.createElement("div");
    grid.className = "grid";
    content.appendChild(grid);
    const draft = plug;
    if (!Array.isArray(draft.config.networks)) draft.config.networks = [];
    const card = document.createElement("div");
    card.className = "card";
    const form = document.createElement("div");
    form.className = "form";
    const chk = createInput("checkbox", { checked: !!draft.enable });
    chk.addEventListener("change", () => { draft.enable = chk.checked; markDirty(); });
    form.appendChild(createRow("启用插件", chk));
    const selCP = createSelect([["public","公共"],["private","私有"]], draft.config.controlPlane || "public");
    selCP.addEventListener("change", () => { draft.config.controlPlane = selCP.value; markDirty(); refreshZ(); });
    form.appendChild(createRow("控制平面", selCP));
    const inCtrl = createInput("text", { value: draft.config.controllerUrl || "" });
    inCtrl.addEventListener("input", () => { draft.config.controllerUrl = inCtrl.value.trim(); markDirty(); });
    const rowCtrl = createRow("控制器 URL", inCtrl);
    const inTok = createInput("password", { value: "****" });
    inTok.addEventListener("input", () => { draft.config.apiToken = inTok.value; markDirty(); });
    const rowTok = createRow("API Token（未更改留空或 ****）", inTok);
    form.appendChild(rowCtrl);
    form.appendChild(rowTok);
    card.appendChild(form);
    grid.appendChild(card);
    const nets = createArrayEditor({
      title: "加入网络（networks）",
      items: () => draft.config.networks,
      renderItem: (n, idx) => {
        const w = document.createElement("div");
        const inId = createInput("text", { value: n?.id || "" , placeholder: "16位网络ID"});
        inId.addEventListener("input", () => { draft.config.networks[idx].id = inId.value.trim(); markDirty(); });
        w.appendChild(createRow("网络 ID", inId));
        return w;
      },
      onAdd: () => { draft.config.networks.push({ id: "" }); },
    });
    grid.appendChild(nets);
    function refreshZ() {
      const isPriv = selCP.value === "private";
      rowCtrl.style.display = isPriv ? "" : "none";
      rowTok.style.display = isPriv ? "" : "none";
    }
    refreshZ();
    mountUxBar(content, {
      onSave: async () => {
        try {
          await putPlugin("zerotier", draft);
          alert("已保存");
        } catch (e) {
          if (e?.status === 404) {
            const res = await saveSection("plugins.zerotier", draft, { andGenerate: false });
            if (!res.saved) alert(`保存失败：${res.error?.message || "未知错误"}`); else alert("已保存");
          } else {
            alert(`保存失败：${e?.message || "未知错误"}`);
          }
        }
      },
      onSaveAndGenerate: async () => {
        try {
          await putPlugin("zerotier", draft);
          await fetchJson("/api/v1/apply", { method: "POST" });
          alert("已保存并提交生成任务（不应用运行态）");
        } catch (e) {
          if (e?.status === 404) {
            const res = await saveSection("plugins.zerotier", draft, { andGenerate: true });
            if (!res.saved) alert(`保存失败：${res.error?.message || "未知错误"}`); else alert("已保存并提交生成任务（不应用运行态）");
          } else {
            alert(`保存失败：${e?.message || "未知错误"}`);
          }
        }
      },
    });
  }

  // Start
  document.addEventListener("DOMContentLoaded", boot, { once: true });
})(); 

