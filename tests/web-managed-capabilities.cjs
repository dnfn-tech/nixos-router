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
  assert.equal(await page.getByLabel("启用 SSH", {exact:true}).isDisabled(),true);
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
    assert.equal(await page.getByLabel("启用 SSH", { exact: true }).isDisabled(), true);
    unavailable = true;
    await page.locator("#page-refresh").click();
    await page.waitForFunction(() => !window.app.state.loading);
    assert.equal(await page.getByLabel("启用 SSH", { exact: true }).isDisabled(), true);
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
    await page.goto(`${base}/?ownership-test=unknown#/ssh`);
    await page.waitForFunction(() => !window.app.state.loading && window.app.state.section?.path === "ssh");
    assert.equal(await page.getByLabel("启用 SSH", { exact: true }).isDisabled(), true);
    await page.unroute("**/api/v1/health");
    await page.locator("#page-refresh").click();
    await page.waitForFunction(() => !window.app.state.loading);
    assert.equal(await page.getByLabel("启用 SSH", { exact: true }).isDisabled(), false, "confirmed generate-only deployment keeps its editor");
  } finally { await context.close(); }
});
