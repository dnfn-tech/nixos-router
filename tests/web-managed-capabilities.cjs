// Run with playwright-core on NODE_PATH, CHROMIUM_PATH and ROUTERD_TEST_BINARY.
// Uses a disposable state directory; never touches an existing deployment.
const { test, before, after } = require("node:test");
const assert = require("node:assert/strict");
const { spawn } = require("node:child_process");
const { mkdtempSync, rmSync } = require("node:fs");
const { tmpdir } = require("node:os");
const { join, resolve } = require("node:path");
const { createServer } = require("node:net");
const { chromium } = require("playwright-core");
let processHandle, browser, stateDir, base;
before(async () => {
  const listener = createServer();
  await new Promise((resolve) => listener.listen(0, "127.0.0.1", resolve));
  const port = listener.address().port;
  await new Promise((resolve) => listener.close(resolve));
  base = `http://127.0.0.1:${port}`;
  stateDir = mkdtempSync(join(tmpdir(), "router-web-test-"));
  processHandle = spawn(
    process.env.ROUTERD_TEST_BINARY,
    [
      "--addr",
      `127.0.0.1:${port}`,
      "--state-dir",
      stateDir,
      "--seed-default-config",
      "--web-dir",
      resolve(__dirname, "../backend/web"),
    ],
    {
      env: { ...process.env, NIXOS_ROUTER_ADMIN_PASSWORD: "web-test-only" },
      stdio: "ignore",
    },
  );
  processHandle.on("error", () => {});
  let ready = false;
  for (let i = 0; i < 100; i++) {
    try {
      if ((await fetch(`${base}/api/v1/health`)).ok) {
        ready = true;
        break;
      }
    } catch {}
    await new Promise((resolve) => setTimeout(resolve, 50));
  }
  assert.ok(ready, "isolated backend must start");
  browser = await chromium.launch({
    executablePath: process.env.CHROMIUM_PATH,
    headless: true,
    args: ["--no-sandbox"],
  });
});
after(async () => {
  if (browser) await browser.close();
  if (processHandle && processHandle.exitCode === null) {
    const exited = new Promise((resolve) =>
      processHandle.once("exit", resolve),
    );
    processHandle.kill();
    await exited;
  }
  if (stateDir) rmSync(stateDir, { recursive: true, force: true });
});
async function pageAt(route) {
  const context = await browser.newContext();
  const page = await context.newPage();
  page.on("dialog", (dialog) => dialog.accept());
  await page.goto(base);
  await page.locator("#login-username").fill("admin");
  await page.locator("#login-password").fill("web-test-only");
  await page.locator("#login-submit").click();
  await page.waitForSelector(".dashboard");
  await page.goto(`${base}/#/${route}`);
  await page.waitForSelector("#page-content input[type=checkbox]");
  return { page, context };
}
async function waitAction(page, id) {
  await page.locator(id).click();
  await page.waitForFunction(() => !window.app.state.busy);
}

const managedHealth = {ok:true,deployment:{mode:"runtime",profile:"ipv4-single-lan"},capabilities:{managedNetwork:true,unsupported:["pppoe","ipv6","qos","ddns","parental","plugins","upnp","guest"],hostManaged:["ssh","system"]}};
test("WiFi page shows measured stations and automatic channel status",async()=>{
 const {page,context}=await pageAt("wifi");
 try {
  await page.route("**/api/v1/wifi/channel",route=>route.fulfill({json:{available:true,mode:"auto",channel:6,reason:"survey and neighbor interference"}}));
  await page.route("**/api/v1/wifi/stations",route=>route.fulfill({json:{available:true,source:"iw",stations:[{mac:"aa:bb:cc:dd:ee:ff",ip:"192.0.2.10",hostname:"device",signalDBm:-55,connectedSeconds:42}]}}));
  await page.locator("#page-refresh").click();
  await page.waitForFunction(()=>!window.app.state.loading);
  const content=await page.locator("#page-content").innerText();
  assert.match(content,/当前信道：6/);
  assert.match(content,/aa:bb:cc:dd:ee:ff/);
  assert.match(content,/192.0.2.10/);
 } finally {await context.close()}
});
test("WiFi page distinguishes zero stations from query failure",async()=>{
 const {page,context}=await pageAt("wifi");
 try {
  await page.route("**/api/v1/wifi/stations",route=>route.fulfill({json:{available:true,source:"iw",stations:[]}}));
  await page.locator("#page-refresh").click();await page.waitForFunction(()=>!window.app.state.loading);
  assert.match(await page.locator("#page-content").innerText(),/当前没有关联客户端/);
  await page.unroute("**/api/v1/wifi/stations");
  await page.route("**/api/v1/wifi/stations",route=>route.fulfill({json:{available:false,reason:"Cannot query associated stations"}}));
  await page.locator("#page-refresh").click();await page.waitForFunction(()=>!window.app.state.loading);
  assert.match(await page.locator("#page-content").innerText(),/Cannot query associated stations/);
 } finally {await context.close()}
});
test("managed UI identifies detected radio and NixOS-owned settings",async()=>{
 const {page,context}=await pageAt("wifi");
 try {
  await page.route("**/api/v1/health",route=>route.fulfill({json:managedHealth}));
  await page.route("**/api/v1/capabilities/wifi",route=>route.fulfill({json:{source:"iw",available:true,interface:"wlan-test",phy:"phy7",bands:["2g"],apSupported:true,maxAP:1,managedMaxAP:1,channels:[{channel:1,disabled:false,noIR:false}]}}));
  await page.locator("#page-refresh").click();
  await page.waitForFunction(()=>!window.app.state.loading);
  assert.match(await page.locator("#page-content").innerText(),/iw.*wlan-test/);
  assert.match(await page.locator("#deployment-description").innerText(),/IPv4/);
  await page.goto(`${base}/#/ssh`);await page.waitForFunction(()=>!window.app.state.loading&&window.app.state.route==="ssh");
  assert.match(await page.locator("#page-content").innerText(),/NixOS/);
  assert.equal(await page.getByLabel("启用 SSH", {exact:true}).count(),0);
  assert.equal(await page.locator("#apply-save").isDisabled(),true);
 }finally{await context.close()}
});
test("failed radio detection remains explicitly unknown",async()=>{
 const {page,context}=await pageAt("wifi");try{
  await page.route("**/api/v1/health",route=>route.fulfill({json:managedHealth}));
  await page.route("**/api/v1/capabilities/wifi",route=>route.fulfill({json:{source:"unavailable",available:false,reason:"probe failed",maxAP:0,bands:[]}}));
  await page.locator("#page-refresh").click();await page.waitForFunction(()=>!window.app.state.loading);
  assert.match(await page.locator("#page-content").innerText(),/probe failed/);
 }finally{await context.close()}
});
test("startup draft warning explains repair without claiming disk replacement",async()=>{
 const {page,context}=await pageAt("wifi");try{
  await page.route("**/api/v1/health",route=>route.fulfill({json:{...managedHealth,startupWarning:"invalid saved draft"}}));
  await page.locator("#page-refresh").click();await page.waitForFunction(()=>!window.app.state.loading);
  const text=await page.locator("#status-banner").innerText();
  assert.match(text,/启动时/);assert.match(text,/修复.*保存.*应用/);assert.match(text,/未覆盖/);assert.match(text,/invalid saved draft/);
  assert.equal(await page.locator("#status-banner").isVisible(),true);
 }finally{await context.close()}
});
test("managed client display uses applied policy while editor retains pending draft",async()=>{
 const {page,context}=await pageAt("wifi");try{
  let known=true;
  await page.route("**/api/v1/clients",route=>route.fulfill({json:{source:"leases",clients:[{hostname:"client",ip:"192.168.1.50",mac:"AA:BB:CC:DD:EE:01",source:"leases",blocked:false,draftBlocked:true,blockedSource:known?"last-good":"config",blockedRuntimeKnown:known}]}}));
  await page.goto(`${base}/#/clients`);await page.waitForSelector("#client-search");
  assert.match(await page.locator("#page-content").innerText(),/未应用阻止/);
  await page.getByRole("button",{name:"编辑",exact:true}).click();
  assert.equal(await page.locator("#client-blocked").isChecked(),true);
  await page.keyboard.press("Escape");known=false;
  await page.locator("#page-refresh").click();await page.waitForFunction(()=>!window.app.state.loading);
  assert.match(await page.locator("#page-content").innerText(),/尚未确认运行阻断状态/);
 }finally{await context.close()}
});

test("health failure retains known managed ownership", async () => {
  const { page, context } = await pageAt("wifi");
  try {
    let unavailable = false;
    await page.route("**/api/v1/health", (route) => unavailable
      ? route.fulfill({ status: 503, json: { error: "temporarily unavailable" } })
      : route.fulfill({ json: managedHealth }));
    await page.goto(`${base}/#/ssh`);
    await page.waitForFunction(() => !window.app.state.loading && window.app.state.route === "ssh");
    assert.equal(await page.getByLabel("启用 SSH", { exact: true }).count(), 0);
    unavailable = true;
    await page.locator("#page-refresh").click();
    await page.waitForFunction(() => !window.app.state.loading);
    assert.equal(await page.getByLabel("启用 SSH", { exact: true }).count(), 0);
    assert.match(await page.locator("#status-banner").innerText(), /健康状态暂时无法获取/);
    await page.goto(`${base}/#/system`);
    await page.waitForFunction(() => !window.app.state.loading && window.app.state.route === "system");
    assert.equal(await page.getByLabel("主机名", { exact: true }).isDisabled(), true);
  } finally { await context.close(); }
});

test("unknown ownership protects host settings until generate-only health is confirmed", async () => {
  const { page, context } = await pageAt("wifi");
  try {
    await page.route("**/api/v1/health", (route) => route.fulfill({ status: 503, json: { error: "temporarily unavailable" } }));
    await page.goto(`${base}/?ownership-test=unknown#/system`);
    await page.waitForFunction(() => !window.app.state.loading && window.app.state.section?.path === "system");
    assert.equal(await page.getByLabel("主机名", { exact: true }).isDisabled(), true);
    await page.unroute("**/api/v1/health");
    await page.locator("#page-refresh").click();
    await page.waitForFunction(() => !window.app.state.loading);
    assert.equal(await page.getByLabel("主机名", { exact: true }).isDisabled(), false, "confirmed generate-only deployment keeps its editor");
  } finally { await context.close(); }
});

test("managed WAN description follows advertised PPPoE capability", async () => {
 const {page,context}=await pageAt("wifi");
 try {
  const health=structuredClone(managedHealth);
  health.capabilities.wanModes=["dhcp","static","pppoe"];
  health.capabilities.unsupported=health.capabilities.unsupported.filter(x=>x!=="pppoe");
  await page.route("**/api/v1/health",route=>route.fulfill({json:health}));
  await page.locator("#page-refresh").click();await page.waitForFunction(()=>!window.app.state.loading);
  const description=await page.locator("#deployment-description").innerText();
  assert.match(description,/WAN 支持 DHCP \/ 静态地址 \/ PPPoE/);
  assert.doesNotMatch(description,/不支持 PPPoE/);
 } finally {await context.close()}
});

test("dual-stack managed profile describes independent WiFi without claiming live connectivity",async()=>{
 const {page,context}=await pageAt("wifi");
 try {
  const health=structuredClone(managedHealth);
  health.deployment.profile="managed-router";
  health.capabilities.wanModes=["dhcp","static","pppoe"];
  health.capabilities.independentWiFiLAN=true;
  health.capabilities.ipv6WanModes=["slaac","dhcpv6"];
  health.capabilities.unsupported=health.capabilities.unsupported.filter(x=>x!=="ipv6"&&x!=="pppoe");
  await page.route("**/api/v1/health",route=>route.fulfill({json:health}));
  await page.locator("#page-refresh").click();await page.waitForFunction(()=>!window.app.state.loading);
  const description=await page.locator("#deployment-description").innerText();
  assert.match(description,/IPv4.*IPv6/);
  assert.match(description,/独立无线网段/);
  assert.doesNotMatch(description,/IPv4 单内网|不支持 IPv6|已联网/);
 }finally{await context.close()}
});

test("managed candidate survives page reload and requires explicit confirmation", async () => {
 const {page,context}=await pageAt("wifi");
 try {
  let confirmations=0;
  let candidate={id:"a".repeat(48),state:"pending",deadline:new Date(Date.now()+60000).toISOString(),revision:"revision-test"};
  const health={...managedHealth,deployment:{mode:"runtime",profile:"managed-router"}};
  await page.route("**/api/v1/health",route=>route.fulfill({json:health}));
  await page.route("**/api/v1/apply/candidate",route=>route.fulfill({json:{confirmation:candidate}}));
  await page.route("**/api/v1/apply/confirm",async route=>{
   confirmations++;
   assert.equal(route.request().postDataJSON().id,candidate.id);
   candidate={...candidate,state:"confirmed"};
   await route.fulfill({json:{confirmation:candidate}});
  });
  await page.reload();
  await page.getByRole("button",{name:"确认保留当前网络配置",exact:true}).waitFor();
  assert.equal(confirmations,0,"reload and polling must never auto-confirm");
  assert.match(await page.locator("#candidate-banner").innerText(),/自动恢复/);
  await page.getByRole("button",{name:"确认保留当前网络配置",exact:true}).click();
  await page.waitForFunction(()=>document.querySelector("#candidate-banner").textContent.includes("已确认保留"));
  assert.equal(confirmations,1);
 }finally{await context.close()}
});

test("expired managed candidate cannot be confirmed in the browser", async () => {
 const {page,context}=await pageAt("wifi");
 try {
  await page.route("**/api/v1/health",route=>route.fulfill({json:{...managedHealth,deployment:{mode:"runtime",profile:"managed-router"}}}));
  await page.route("**/api/v1/apply/candidate",route=>route.fulfill({json:{confirmation:{id:"b".repeat(48),state:"pending",deadline:new Date(Date.now()-1000).toISOString()}}}));
  await page.locator("#page-refresh").click();
  await page.getByRole("button",{name:"确认保留当前网络配置",exact:true}).waitFor();
  assert.equal(await page.getByRole("button",{name:"确认保留当前网络配置",exact:true}).isDisabled(),true);
 }finally{await context.close()}
});

test("host DDNS saves its switch independently of the router draft", async () => {
  const { page, context } = await pageAt("ddns");
  try {
    let enabled = true;
    const writes = [];
    await page.route("**/api/v1/health", route => route.fulfill({
      json: { ...managedHealth, capabilities: { ...managedHealth.capabilities, managedDDNS: true } },
    }));
    await page.route("**/api/v1/ddns", async route => {
      const request = route.request();
      if (request.method() === "PUT") {
        writes.push(request.postDataJSON());
        enabled = writes.at(-1).enabled;
      }
      await route.fulfill({ json: { enabled, record: "s01.example.com" } });
    });
    await page.locator("#page-refresh").click();
    await page.waitForFunction(() => !window.app.state.loading);
    const content = page.locator("#page-content");
    const toggle = content.getByRole("checkbox", { name: "启用动态 DNS" });
    const save = content.getByRole("button", { name: "保存 DDNS" });
    assert.equal(await toggle.isChecked(), true);
    assert.equal(await save.isDisabled(), true);
    assert.equal(await content.locator('input[type="password"]').count(), 0);
    assert.equal(await page.locator("#apply-run").isDisabled(), true);
    assert.match(await content.innerText(), /s01\.example\.com/);
    assert.doesNotMatch(await content.innerText(), /启用将被拒绝/);

    await toggle.uncheck();
    await save.click();
    const result = content.getByRole("status");
    await result.getByText("已停用后续定时检查。").waitFor();
    assert.deepEqual(writes, [{ enabled: false }]);
    await page.locator("#page-refresh").click();
    await page.waitForFunction(() => !window.app.state.loading);
    assert.equal(await toggle.isChecked(), false);
    await toggle.check();
    await save.click();
    await result.getByText("已启用，等待下一次定时检查。").waitFor();
    assert.deepEqual(writes, [{ enabled: false }, { enabled: true }]);
  } finally {
    await context.close();
  }
});
