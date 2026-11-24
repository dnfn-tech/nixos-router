// Run with playwright-core on NODE_PATH, CHROMIUM_PATH and ROUTERD_TEST_BINARY.
// Uses a disposable state directory; never touches an existing deployment.
const { test, before, after } = require("node:test");
const assert = require("node:assert/strict");
const { spawn } = require("node:child_process");
const { mkdtempSync, rmSync } = require("node:fs");
const { tmpdir } = require("node:os");
const { join } = require("node:path");
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
test("single plugin save persists through API and clears draft before apply", async () => {
  const { page, context } = await pageAt("plugins/adblock");
  const original = await configOf(context);
  try {
    const input = page.locator("#page-content input[type=checkbox]").first();
    const pluginWrites = [];
    page.on("request", (r) => {
      if (r.method() === "PUT" && r.url().includes("/api/v1/plugins/"))
        pluginWrites.push(r.url());
    });
    await input.click();
    const enabled = await input.isChecked();
    await waitAction(page, "#apply-save");
    assert.equal(
      pluginWrites.length,
      0,
      "plugin save must use the authoritative config endpoint without 404 fallback",
    );
    assert.equal(await page.locator("#apply-dirty").isVisible(), false);
    const response = await context.request.get(`${base}/api/v1/config`);
    const saved = await response.json();
    assert.equal((saved.config || saved).plugins.adblock.enable, enabled);
    await waitAction(page, "#apply-run");
    assert.match(
      await page.locator("#apply-msg").innerText(),
      /生成完成.*未应用运行态/,
    );
  } finally {
    await putConfig(context, original);
    await context.close();
  }
});
test("HTTP 200 runtime failure is shown as failure with diagnostic", async () => {
  const { page, context } = await pageAt("plugins/mihomo");
  try {
    await page.route("**/api/v1/apply", (route) =>
      route.fulfill({
        json: {
          jobId: "failure-case",
          mode: "generate+reload",
          appliedRuntime: false,
          error: "dnsmasq reload failed",
          notes: ["rollback failed"],
        },
      }),
    );
    await waitAction(page, "#apply-run");
    assert.match(
      await page.locator("#apply-msg").innerText(),
      /应用失败.*dnsmasq reload failed/,
    );
    assert.match(
      await page.locator("#apply-msg").innerText(),
      /rollback failed/,
    );
  } finally {
    await context.close();
  }
});
test("failed atomic plugin save keeps dirty and cancels apply", async () => {
  const { page, context } = await pageAt("plugins");
  try {
    let applies = 0;
    await page.route("**/api/v1/config", (route) =>
      route.request().method() === "PUT"
        ? route.fulfill({ status: 500, json: { error: "save failed" } })
        : route.continue(),
    );
    await page.route("**/api/v1/apply", (route) => {
      applies++;
      return route.fulfill({
        json: { mode: "generate-only", appliedRuntime: false },
      });
    });
    await page.locator("#page-content input[type=checkbox]").first().click();
    await waitAction(page, "#apply-save");
    assert.equal(await page.locator("#apply-dirty").isVisible(), true);
    await waitAction(page, "#apply-run");
    assert.equal(applies, 0);
    assert.match(await page.locator("#apply-msg").innerText(), /保存未完成/);
  } finally {
    await context.close();
  }
});
test("all plugin saves clear dirty and stale cached values", async () => {
  const { page, context } = await pageAt("plugins");
  const original = await configOf(context);
  try {
    const first = page.locator("#page-content input[type=checkbox]").first();
    const label = "adblock";
    await first.click();
    const enabled = await first.isChecked();
    await waitAction(page, "#apply-save");
    assert.equal(await page.locator("#apply-dirty").isVisible(), false);
    await page.goto(`${base}/#/plugins/${label}`);
    await page.waitForSelector("#page-content input[type=checkbox]");
    assert.equal(
      await page
        .locator("#page-content input[type=checkbox]")
        .first()
        .isChecked(),
      enabled,
    );
  } finally {
    await putConfig(context, original);
    await context.close();
  }
});
test("manager conflict exposes the actionable API message", async () => {
  const { page, context } = await pageAt("plugins/mihomo");
  try {
    await page.route("**/api/v1/apply", (route) =>
      route.fulfill({
        status: 409,
        json: {
          error: "network_manager_conflict",
          message:
            "network manager must be stopped before runtime apply: NetworkManager.service (active)",
          stage: "preflight",
          appliedRuntime: false,
        },
      }),
    );
    await waitAction(page, "#apply-run");
    assert.match(
      await page.locator("#apply-msg").innerText(),
      /应用失败.*NetworkManager.service/,
    );
  } finally {
    await context.close();
  }
});
test("runtime success retains reported warnings", async () => {
  const { page, context } = await pageAt("plugins/mihomo");
  try {
    await page.route("**/api/v1/apply", (route) =>
      route.fulfill({
        json: {
          jobId: "warning-case",
          mode: "generate+reload",
          appliedRuntime: true,
          notes: ["plugin unavailable"],
        },
      }),
    );
    await waitAction(page, "#apply-run");
    assert.match(
      await page.locator("#apply-msg").innerText(),
      /应用完成.*plugin unavailable/,
    );
  } finally {
    await context.close();
  }
});
test("discarding edits after a save cannot leak into another page save", async () => {
  const { page, context } = await pageAt("plugins/adblock");
  const original = await configOf(context);
  try {
    const input = page.locator("#page-content input[type=checkbox]").first();
    await input.click();
    const savedValue = await input.isChecked();
    await waitAction(page, "#apply-save");
    await input.click();
    await page.goto(`${base}/#/dns`);
    await page.getByLabel("本地域名", { exact: true }).fill("test.lan");
    await waitAction(page, "#apply-save");
    const result = await (
      await context.request.get(`${base}/api/v1/config`)
    ).json();
    assert.equal(
      (result.config || result).plugins.adblock.enable,
      savedValue,
      "discarded draft was persisted by another page",
    );
  } finally {
    await putConfig(context, original);
    await context.close();
  }
});
const allRoutes = [
  "overview",
  "wan",
  "lan",
  "wifi",
  "clients",
  "dns",
  "firewall",
  "ssh",
  "system",
  "plugins",
  "ddns",
  "qos",
  "parental",
  "ipv6",
  "adblock",
  "traffic",
  "mihomo",
  "vlan",
  "tailscale",
  "zerotier",
  "plugins/adblock",
  "system?tab=account",
  "system?tab=activity",
  "system?tab=diagnostics",
];
test("every route has readable labeled controls and no page overflow", async () => {
  const { page, context } = await pageAt("plugins");
  try {
    for (const width of [1440, 1024, 390, 320]) {
      await page.setViewportSize({ width, height: 900 });
      for (const route of allRoutes) {
        await page.goto(`${base}/#/${route}`);
        await page.waitForFunction(
          (route) =>
            document.querySelector("#page-content").dataset.route ===
              route.split("?")[0].replace("plugins/", "") &&
            document
              .querySelector("#page-content")
              .getAttribute("aria-busy") === "false",
          route,
        );
        const issues = await page.evaluate(() => ({
          overflow: document.documentElement.scrollWidth > innerWidth,
          unnamed: [
            ...document.querySelectorAll(
              "#page-content input, #page-content select, #page-content textarea",
            ),
          ]
            .filter(
              (e) =>
                !e.labels?.length &&
                !e.getAttribute("aria-label") &&
                !e.getAttribute("aria-labelledby"),
            )
            .map((e) => e.id || e.type),
          tiny: [...document.querySelectorAll("#page-content label")].some(
            (e) => parseFloat(getComputedStyle(e).fontSize) < 13,
          ),
        }));
        assert.equal(issues.overflow, false, `${route} overflow at ${width}`);
        assert.deepEqual(issues.unnamed, [], `${route} unnamed controls`);
        assert.equal(issues.tiny, false, `${route} unreadable labels`);
      }
    }
  } finally {
    await context.close();
  }
});
test("QoS controls persist backend units and policy fields", async () => {
  const { page, context } = await pageAt("plugins");
  try {
    await page.goto(`${base}/#/qos`);
    await page
      .getByLabel("上行带宽（Mbps）", { exact: true })
      .fill("80", { timeout: 2000 });
    await page.getByLabel("下行带宽（Mbps）", { exact: true }).fill("400");
    await waitAction(page, "#apply-save");
    const { config } = await (
      await context.request.get(`${base}/api/v1/config`)
    ).json();
    assert.equal(config.qos.upMbps, 80);
    assert.equal(config.qos.downMbps, 400);
  } finally {
    await context.close();
  }
});
test("overview never presents stub clients or WiFi as measured facts", async () => {
  const { page, context } = await pageAt("plugins");
  try {
    await page.goto(`${base}/#/overview`);
    await page.waitForSelector(".dashboard");
    const text = await page.locator("#page-content").innerText();
    assert.match(text, /未采集|不可用/);
    assert.doesNotMatch(text, /最多 2 个 AP|支持频段.*6g/);
    assert.equal(await page.locator("#apply-save").isDisabled(), true);
    assert.match(await page.locator("#deployment-mode").innerText(), /仅生成/);
  } finally {
    await context.close();
  }
});
async function navigate(page, route) {
  await page.goto(`${base}/#/${route}`);
  await page.waitForFunction(
    (id) =>
      document.querySelector("#page-content").dataset.route === id &&
      document.querySelector("#page-content").getAttribute("aria-busy") ===
        "false",
    route.split("?")[0].replace("plugins/", ""),
  );
}
async function configOf(context) {
  // Fixture/verification reads explicitly request real stored secrets; page reads stay redacted.
  return (await (await context.request.get(`${base}/api/v1/config?redact=0`)).json())
    .config;
}
async function putConfig(context, config) {
  const r = await context.request.put(`${base}/api/v1/config`, {
    data: config,
    headers: { "If-Match": JSON.stringify((await configOf(context)).revision) },
  });
  assert.equal(r.status(), 200, await r.text());
}
test("core and plugin fields survive real API saves with their correct schema", async () => {
  const { page, context } = await pageAt("plugins");
  const original = await configOf(context);
  try {
    const cfg = structuredClone(original);
    cfg.plugins ||= {};
    cfg.lan.staticLeases = [
      { mac: "02:00:00:00:00:01", ip: "192.168.1.10", hostname: "desktop" },
    ];
    cfg.firewall.portForwards = [
      {
        protocol: "tcp",
        externalPort: 8080,
        externalPortEnd: 8081,
        destIp: "192.168.1.10",
        destPort: 80,
        description: "web",
      },
    ];
    cfg.qos.devicePolicy = [
      {
        mac: "02:00:00:00:00:01",
        ip: "192.168.1.10",
        priority: "normal",
        limitKbps: 1000,
      },
    ];
    cfg.parental.rules = [
      {
        targetMacs: ["02:00:00:00:00:01"],
        group: "kids",
        schedule: "night",
        action: "block",
      },
    ];
    cfg.plugins.vlan = {
      enable: false,
      config: {
        vlans: [{ vid: 10, name: "vlan10", bridge: "br-lan" }],
        extra: { keep: true },
      },
    };
    cfg.plugins.zerotier = {
      enable: false,
      config: {
        controlPlane: "private",
        controllerUrl: "https://old.example.com",
        apiToken: "original-token",
        networks: ["0123456789abcdef"],
      },
    };
    cfg.plugins.tailscale = {
      enable: false,
      config: {
        controlPlane: "headscale",
        loginServer: "https://old.example.com",
        authKey: "original-key",
      },
    };
    cfg.ddns = {
      enable: true,
      provider: "cloudflare",
      cloudflare: {
        apiToken: "real-test-token",
        zone: "zone-a",
        record: "old.example.com",
      },
    };
    await putConfig(context, cfg);
    const cases = [
      ["wan", [["外网接口", "eth-test"]], [["wan.interface", "eth-test"]]],
      [
        "lan",
        [
          ["租约时间（分钟）", "90"],
          ["设备名称", "desktop-new"],
        ],
        [
          ["lan.dhcp.leaseMins", 90],
          ["lan.staticLeases.0.hostname", "desktop-new"],
        ],
      ],
      [
        "firewall",
        [
          ["外部端口", "9090"],
          ["外部结束端口", "9091"],
          ["目标端口", "8080"],
        ],
        [
          ["firewall.portForwards.0.externalPort", 9090],
          ["firewall.portForwards.0.externalPortEnd", 9091],
          ["firewall.portForwards.0.destPort", 8080],
        ],
      ],
      [
        "qos",
        [["限速（Kbps）", "640"]],
        [["qos.devicePolicy.0.limitKbps", 640]],
      ],
      [
        "parental",
        [
          ["分组名称", "children"],
          ["生效时间段", "Mon-Fri 22:00-07:00"],
        ],
        [
          ["parental.rules.0.group", "children"],
          ["parental.rules.0.schedule", "Mon-Fri 22:00-07:00"],
        ],
      ],
      [
        "ddns",
        [["记录名称", "home.example.com"]],
        [["ddns.cloudflare.record", "home.example.com"]],
      ],
      [
        "vlan",
        [
          ["VLAN ID", "20"],
          ["接口名称", "vlan20"],
        ],
        [
          ["plugins.vlan.config.vlans.0.vid", 20],
          ["plugins.vlan.config.vlans.0.name", "vlan20"],
          ["plugins.vlan.config.extra.keep", true],
        ],
      ],
      [
        "tailscale",
        [["登录服务器", "https://headscale.example.com"]],
        [
          [
            "plugins.tailscale.config.loginServer",
            "https://headscale.example.com",
          ],
          ["plugins.tailscale.config.authKey", "original-key"],
        ],
      ],
      [
        "zerotier",
        [["网络 ID", "fedcba9876543210"]],
        [
          ["plugins.zerotier.config.networks.0", "fedcba9876543210"],
          ["plugins.zerotier.config.apiToken", "original-token"],
        ],
      ],
      [
        "mihomo",
        [["DNS 监听端口", "1054"]],
        [["plugins.mihomo.config.dns.port", 1054]],
      ],
      [
        "traffic",
        [["保留天数", "14"]],
        [["plugins.traffic.config.retentionDays", 14]],
      ],
      [
        "system",
        [["主机名", "router-ui-test"]],
        [["system.hostname", "router-ui-test"]],
      ],
    ];
    for (const [route, fields, expected] of cases) {
      await navigate(page, route);
      for (const [label, value] of fields)
        await page.getByLabel(label, { exact: true }).fill(value);
      await waitAction(page, "#apply-save");
      assert.equal(
        await page.locator("#apply-dirty").isVisible(),
        false,
        `${route}: ${await page.locator("#apply-msg").innerText()}`,
      );
      const saved = await configOf(context);
      for (const [path, value] of expected)
        assert.deepEqual(
          path.split(".").reduce((a, k) => a?.[k], saved),
          value,
          path,
        );
    }
    const backup = await (
      await context.request.get(`${base}/api/v1/backup`)
    ).json();
    assert.equal(backup.config.ddns.cloudflare.apiToken, "real-test-token");
  } finally {
    await putConfig(context, original);
    await context.close();
  }
});
test("WiFi deletes preserve the remaining AP secret and renames require a new password", async () => {
  const { page, context } = await pageAt("plugins");
  const original = await configOf(context);
  try {
    const cfg = structuredClone(original);
    cfg.plugins ||= {};
    cfg.wifi.aps = [
      {
        ssid: "first",
        band: "2g",
        channel: 1,
        psk: "first-password",
        enable: true,
      },
      {
        ssid: "second",
        band: "5g",
        channel: 36,
        psk: "second-password",
        enable: true,
      },
    ];
    await putConfig(context, cfg);
    await navigate(page, "wifi");
    await page
      .getByRole("button", { name: "删除接入点第 1 项", exact: true })
      .click();
    await waitAction(page, "#apply-save");
    let backup = await (
      await context.request.get(`${base}/api/v1/backup`)
    ).json();
    assert.equal(backup.config.wifi.aps.length, 1);
    assert.equal(backup.config.wifi.aps[0].psk, "second-password");
    await page.getByLabel("无线名称（SSID）", { exact: true }).fill("renamed");
    await page.locator("#apply-save").click();
    assert.equal(await page.locator("#apply-dirty").isVisible(), true);
    await page
      .getByLabel("无线密码", { exact: true })
      .fill("replacement-password");
    await waitAction(page, "#apply-save");
    backup = await (await context.request.get(`${base}/api/v1/backup`)).json();
    assert.equal(backup.config.wifi.aps[0].psk, "replacement-password");
    assert.equal(backup.config.wifi.aps[0].ssid, "renamed");
  } finally {
    await putConfig(context, original);
    await context.close();
  }
});
test("system panels support backup, invalid restore feedback, restore refresh and password feedback", async () => {
  const { page, context } = await pageAt("plugins");
  const original = await configOf(context);
  try {
    await navigate(page, "system?tab=account");
    await page.getByLabel("当前密码", { exact: true }).fill("wrong-password");
    await page.getByLabel("新密码", { exact: true }).fill("replacement-only");
    await page
      .getByLabel("再次输入新密码", { exact: true })
      .fill("replacement-only");
    await page.getByRole("button", { name: "修改密码", exact: true }).click();
    await page.getByText("当前密码不正确", { exact: true }).waitFor();
    assert.equal(await page.locator("#main-view").isVisible(), true);
    const downloadPromise = page.waitForEvent("download");
    await page.getByRole("button", { name: "下载配置备份" }).click();
    const download = await downloadPromise;
    assert.match(download.suggestedFilename(), /\.json$/);
    await page.getByLabel("选择配置备份").setInputFiles({
      name: "bad.json",
      mimeType: "application/json",
      buffer: Buffer.from("{notjson"),
    });
    await page.getByRole("button", { name: "恢复所选备份" }).click();
    await page.getByText(/恢复失败：/).waitFor();
    const cfg = structuredClone(original);
    cfg.plugins ||= {};
    cfg.system.hostname = "restored-router";
    await page.getByLabel("选择配置备份").setInputFiles({
      name: "config.json",
      mimeType: "application/json",
      buffer: Buffer.from(JSON.stringify({ config: cfg })),
    });
    await page.getByRole("button", { name: "恢复所选备份" }).click();
    await page
      .getByText("备份已恢复，尚未生成或应用。", { exact: true })
      .waitFor();
    assert.equal(
      await page
        .getByRole("button", { name: "重启设备", exact: true })
        .isDisabled(),
      true,
    );
    await navigate(page, "system");
    assert.equal(
      await page.getByLabel("主机名", { exact: true }).inputValue(),
      "restored-router",
    );
    await navigate(page, "system?tab=activity");
    await page.getByLabel("搜索操作记录").fill("restore");
    assert.match(await page.locator("#page-content").innerText(), /restore/);
    await navigate(page, "system?tab=diagnostics");
    assert.equal(await page.locator("details[open]").count(), 0);
  } finally {
    await putConfig(context, original);
    await context.close();
  }
});
test("login errors are visible and the skip link retains the current route", async () => {
  const context = await browser.newContext(),
    page = await context.newPage();
  try {
    await page.goto(base);
    await page.getByLabel("用户名", { exact: true }).fill("admin");
    await page.getByLabel("密码", { exact: true }).fill("wrong-test-only");
    await page.locator("#login-submit").click();
    await page.locator("#login-error:not(.hidden)").waitFor();
    assert.match(await page.locator("#login-error").innerText(), /不正确/);
    await page.getByLabel("密码", { exact: true }).fill("web-test-only");
    await page.locator("#login-submit").click();
    await page.waitForSelector(".dashboard");
    await page.locator(".skip-link").focus();
    await page.keyboard.press("Enter");
    assert.equal(
      await page
        .locator("#page-title")
        .evaluate((e) => e === document.activeElement),
      true,
    );
    assert.equal(await page.locator(".dashboard").count(), 1);
  } finally {
    await context.close();
  }
});
test("unknown plugin editing preserves typed JSON and diagnostics mask plugin secrets", async () => {
  const { page, context } = await pageAt("plugins");
  const original = await configOf(context);
  try {
    const cfg = structuredClone(original);
    cfg.plugins ||= {};
    cfg.plugins.custom = {
      enable: false,
      config: {
        enabled: true,
        count: 2,
        nested: { apiToken: "sensitive-marker", other: "value" },
      },
    };
    await putConfig(context, cfg);
    await navigate(page, "plugins/custom");
    await page.getByLabel("高级配置（JSON）").fill(
      JSON.stringify({
        enabled: false,
        count: 3,
        nested: { apiToken: "", other: "changed" },
      }),
    );
    await waitAction(page, "#apply-save");
    const saved = await configOf(context);
    assert.equal(saved.plugins.custom.config.count, 3);
    assert.equal(
      saved.plugins.custom.config.nested.apiToken,
      "sensitive-marker",
    );
    await navigate(page, "system?tab=diagnostics");
    await page.getByText("配置诊断（密钥已遮盖）", { exact: true }).click();
    assert.doesNotMatch(
      await page.locator("#page-content").innerText(),
      /sensitive-marker/,
    );
  } finally {
    await putConfig(context, original);
    await context.close();
  }
});
test("late requests cannot replace the active page and long feedback remains scrollable", async () => {
  const { page, context } = await pageAt("plugins");
  try {
    let delay = true;
    await page.route("**/api/v1/status", async (route) => {
      if (delay) {
        delay = false;
        await new Promise((r) => setTimeout(r, 400));
      }
      await route.continue();
    });
    await page.goto(`${base}/#/wan`);
    await navigate(page, "dns");
    await page.waitForTimeout(500);
    assert.equal(await page.locator("#page-title").innerText(), "DNS");
    assert.equal(await page.getByLabel("本地域名").count(), 1);
    await page.setViewportSize({ width: 320, height: 640 });
    await page.route("**/api/v1/apply", (route) =>
      route.fulfill({
        status: 409,
        json: { message: "NetworkManager.service ".repeat(100) },
      }),
    );
    await waitAction(page, "#apply-run");
    const geometry = await page.evaluate(() => {
      const msg = document.querySelector("#apply-msg"),
        footer = document.querySelector(".actionbar");
      return {
        overflow: document.documentElement.scrollWidth > innerWidth,
        scroll: msg.scrollHeight > msg.clientHeight,
        bottom: footer.getBoundingClientRect().bottom,
      };
    });
    assert.equal(geometry.overflow, false);
    assert.equal(geometry.scroll, true);
    assert.equal(geometry.bottom, 640);
  } finally {
    await context.close();
  }
});
test("populated tables, system tabs and editors fit mobile and desktop; expired dialogs release login", async () => {
  const { page, context } = await pageAt("plugins");
  const original = await configOf(context);
  try {
    const cfg = structuredClone(original);
    cfg.plugins ||= {};
    cfg.lan.staticLeases = Array.from({ length: 35 }, (_, i) => ({
      hostname: `家庭设备-${i}-` + "long-name-".repeat(5),
      ip: `192.168.1.${i + 10}`,
      mac: `02:00:00:00:01:${i.toString(16).padStart(2, "0")}`,
    }));
    await putConfig(context, cfg);
    const errors = [];
    page.on("pageerror", (e) => errors.push(e.message));
    const { mkdirSync } = require("node:fs");
    mkdirSync("/tmp/nixos-router-web-audit/after", { recursive: true });
    for (const width of [1440, 390, 320]) {
      await page.setViewportSize({ width, height: 900 });
      for (const route of [
        "overview",
        "wan",
        "lan",
        "clients",
        "system?tab=account",
        "system?tab=activity",
        "system?tab=diagnostics",
      ]) {
        await navigate(page, route);
        assert.equal(
          await page.evaluate(
            () => document.documentElement.scrollWidth > innerWidth,
          ),
          false,
          `${route} populated overflow at ${width}`,
        );
        if (width !== 320)
          await page.screenshot({
            path: `/tmp/nixos-router-web-audit/after/${route.replace("?tab=", "-")}-${width}.png`,
            fullPage: true,
          });
      }
    }
    assert.deepEqual(errors, []);
    await navigate(page, "clients");
    await page.getByLabel("搜索设备").fill("家庭设备-0-");
    assert.equal(await page.locator("tbody tr").count(), 1);
    await page.getByRole("button", { name: "编辑", exact: true }).click();
    await page.route("**/api/v1/clients/*", (route) =>
      route.fulfill({ status: 401, json: { error: "unauthorized" } }),
    );
    await page.getByRole("button", { name: "保存设备" }).click();
    await page.locator("#login-view:not(.hidden)").waitFor();
    assert.equal(await page.locator("dialog[open]").count(), 0);
  } finally {
    await putConfig(context, original);
    await context.close();
  }
});
test("WAN mode changes and all DDNS providers retain conditional fields", async () => {
  const { page, context } = await pageAt("plugins");
  const original = await configOf(context);
  try {
    await navigate(page, "wan");
    await page.getByLabel("上网方式", { exact: true }).selectOption("static");
    await page
      .getByLabel("静态地址 / CIDR", { exact: true })
      .fill("192.0.2.2/24");
    await page.getByLabel("网关", { exact: true }).fill("192.0.2.1");
    await page
      .getByLabel("DNS 服务器", { exact: true })
      .fill("1.1.1.1\n8.8.8.8");
    await waitAction(page, "#apply-save");
    assert.deepEqual((await configOf(context)).wan.static.dns, [
      "1.1.1.1",
      "8.8.8.8",
    ]);
    await page.getByLabel("上网方式", { exact: true }).selectOption("pppoe");
    await page.getByLabel("PPPoE 用户名", { exact: true }).fill("test-user");
    await page
      .getByLabel("PPPoE 密码", { exact: true })
      .fill("ppp-test-password");
    await waitAction(page, "#apply-save");
    assert.equal((await configOf(context)).wan.pppoe.username, "test-user");
    await navigate(page, "ddns");
    await page.getByLabel("启用动态 DNS", { exact: true }).check();
    for (const [provider, fields, expected] of [
      [
        "cloudflare",
        [
          ["API Token", "cloud-token"],
          ["Zone", "zone-1"],
          ["记录名称", "router.example.com"],
        ],
        ["record", "router.example.com"],
      ],
      [
        "duckdns",
        [
          ["Token", "duck-token"],
          ["域名", "router-duck"],
        ],
        ["domain", "router-duck"],
      ],
      [
        "aliyun",
        [
          ["Access Key", "ali-key"],
          ["Secret Key", "ali-secret"],
          ["域名", "example.com"],
          ["记录名称", "router"],
        ],
        ["record", "router"],
      ],
      [
        "custom",
        [
          ["更新 URL", "https://example.com/update"],
          ["Token", "custom-token"],
        ],
        ["url", "https://example.com/update"],
      ],
    ]) {
      await page
        .getByLabel("服务提供商", { exact: true })
        .selectOption(provider);
      for (const [label, value] of fields)
        await page.getByLabel(label, { exact: true }).fill(value);
      await waitAction(page, "#apply-save");
      const cfg = await configOf(context);
      assert.equal(cfg.ddns.provider, provider);
      assert.equal(cfg.ddns[provider][expected[0]], expected[1]);
    }
    await navigate(page, "ipv6");
    await page
      .getByLabel("外网获取方式", { exact: true })
      .selectOption("slaac");
    await page.getByLabel("向内网发送路由通告（RA）", { exact: true }).check();
    await waitAction(page, "#apply-save");
    assert.equal((await configOf(context)).ipv6.wanMode, "slaac");
    assert.equal((await configOf(context)).ipv6.lanRouterAdvertisement, true);
  } finally {
    await putConfig(context, original);
    await context.close();
  }
});
test("blocking a DHCP client does not submit an unchanged hostname or create a static lease", async () => {
  const { page, context } = await pageAt("plugins");
  const original = await configOf(context);
  try {
    const cfg = structuredClone(original);
    cfg.lan.staticLeases = [];
    cfg.firewall.blockedMacs = [];
    await putConfig(context, cfg);
    await page.route("**/api/v1/clients", route => route.fulfill({
      status: 200,
      json: {source: "leases", clients: [{mac: "02:00:00:00:00:03", ip: "192.168.1.12", hostname: "dynamic-client", source: "leases", expires: "", lastSeen: "", blocked: false}]},
    }));
    await navigate(page, "clients");
    await page.getByRole("button", { name: "编辑", exact: true }).click();
    await page.getByLabel("阻止访问", { exact: true }).check();
    const patched = page.waitForResponse(r => r.request().method() === "PATCH" && r.url().includes("/api/v1/clients/"));
    await page.getByRole("button", { name: "保存设备" }).click();
    const response = await patched;
    assert.equal(response.status(), 200, await response.text());
    assert.deepEqual(response.request().postDataJSON(), {blocked: true});
    await page.waitForSelector("dialog", { state: "detached" });
    const saved = await configOf(context);
    assert.deepEqual(saved.lan.staticLeases || [], []);
    assert.ok(saved.firewall.blockedMacs.includes("02:00:00:00:00:03"));
  } finally {
    await putConfig(context, original);
    await context.close();
  }
});
test("client edits persist and password changes remain usable", async () => {
  const { page, context } = await pageAt("plugins");
  const original = await configOf(context);
  let changed = false;
  try {
    const cfg = structuredClone(original);
    cfg.plugins ||= {};
    cfg.lan.staticLeases = [
      { mac: "02:00:00:00:00:02", ip: "192.168.1.11", hostname: "old-name" },
    ];
    await putConfig(context, cfg);
    await navigate(page, "clients");
    await page.getByRole("button", { name: "编辑", exact: true }).click();
    await page.getByLabel("设备名称", { exact: true }).fill("new-name");
    await page.getByLabel("阻止访问", { exact: true }).check();
    await page.getByRole("button", { name: "保存设备" }).click();
    await page.waitForSelector("dialog", { state: "detached" });
    await page.getByText("new-name", { exact: true }).waitFor();
    const saved = await configOf(context);
    assert.equal(saved.lan.staticLeases[0].hostname, "new-name");
    assert.ok(saved.firewall.blockedMacs.includes("02:00:00:00:00:02"));
    await navigate(page, "system?tab=account");
    await page.getByLabel("当前密码", { exact: true }).fill("web-test-only");
    await page.getByLabel("新密码", { exact: true }).fill("new-test-password");
    await page
      .getByLabel("再次输入新密码", { exact: true })
      .fill("new-test-password");
    await page.getByRole("button", { name: "修改密码", exact: true }).click();
    await page
      .getByText("密码已修改，其他登录会话已失效。", { exact: true })
      .waitFor();
    changed = true;
    const login = await context.request.post(`${base}/api/v1/session`, {
      data: { username: "admin", password: "new-test-password" },
    });
    assert.equal(login.status(), 201);
  } finally {
    if (changed) {
      const r = await context.request.post(`${base}/api/v1/session/password`, {
        data: {
          currentPassword: "new-test-password",
          newPassword: "web-test-only",
        },
      });
      assert.equal(r.status(), 200);
    }
    await putConfig(context, original);
    await context.close();
  }
});
test("queued runtime jobs honor their final payload instead of claiming submission is completion", async () => {
  const { page, context } = await pageAt("plugins");
  try {
    await page.route("**/api/v1/apply", (route) =>
      route.fulfill({ json: { jobId: "async-test", status: "queued" } }),
    );
    await page.route("**/api/v1/jobs/async-test", (route) =>
      route.fulfill({
        json: {
          id: "async-test",
          status: "success",
          payload: JSON.stringify({
            mode: "generate+reload",
            appliedRuntime: true,
            notes: ["final warning"],
          }),
        },
      }),
    );
    await waitAction(page, "#apply-run");
    assert.match(
      await page.locator("#apply-msg").innerText(),
      /应用完成.*final warning/,
    );
    assert.equal(
      await page.evaluate(() => sessionStorage.getItem("router-apply-job")),
      null,
    );
  } finally {
    await context.close();
  }
});
test("advanced plugin JSON preserves secrets when nested arrays are reordered", async () => {
  const { page, context } = await pageAt("plugins");
  const original = await configOf(context);
  try {
    const cfg = structuredClone(original);
    cfg.plugins ||= {};
    (cfg.plugins ||= {}).arrayplugin = {
      enable: false,
      config: {
        label: "original",
        peers: [
          { name: "a", token: "secret-a" },
          { name: "b", token: "secret-b" },
        ],
      },
    };
    await putConfig(context, cfg);
    await navigate(page, "plugins/arrayplugin");
    const input = page.getByLabel("高级配置（JSON）");
    const value = JSON.parse(await input.inputValue());
    assert.doesNotMatch(await input.inputValue(), /secret-a|secret-b/);
    value.label = "changed";
    value.peers.reverse();
    await input.fill(JSON.stringify(value));
    await waitAction(page, "#apply-save");
    const result = (await configOf(context)).plugins.arrayplugin.config;
    assert.equal(result.peers[0].name, "b");
    assert.equal(result.peers[0].token, "secret-b");
    assert.equal(result.peers[1].token, "secret-a");
    const next = JSON.parse(await input.inputValue());
    next.label = "saved-twice";
    await input.fill(JSON.stringify(next));
    await waitAction(page, "#apply-save");
    assert.equal((await configOf(context)).plugins.arrayplugin.config.label, "saved-twice");
  } finally {
    await putConfig(context, original);
    await context.close();
  }
});
test("restore cannot run concurrently with apply", async () => {
  const { page, context } = await pageAt("plugins");
  try {
    await navigate(page, "system?tab=account");
    let release;
    const held = new Promise((r) => (release = r));
    await page.route("**/api/v1/apply", async (route) => {
      await held;
      await route.fulfill({
        json: { mode: "generate-only", appliedRuntime: false },
      });
    });
    await page.locator("#apply-run").click();
    try {
      await page.waitForFunction(() => window.app.state.busy);
      assert.equal(
        await page.getByRole("button", { name: "恢复所选备份" }).isDisabled(),
        true,
      );
    } finally {
      release();
    }
    await page.waitForFunction(() => !window.app.state.busy);
  } finally {
    await context.close();
  }
});
test("redrawing WiFi collections retains rename password validation", async () => {
  const { page, context } = await pageAt("plugins");
  const original = await configOf(context);
  try {
    const cfg = structuredClone(original);
    cfg.plugins ||= {};
    cfg.wifi.aps = [
      {
        ssid: "old",
        band: "2g",
        channel: 1,
        psk: "original-password",
        enable: true,
      },
      {
        ssid: "other",
        band: "5g",
        channel: 36,
        psk: "other-password",
        enable: true,
      },
    ];
    await putConfig(context, cfg);
    await navigate(page, "wifi");
    await page
      .getByLabel("无线名称（SSID）", { exact: true })
      .first()
      .fill("new-name");
    await page
      .getByRole("button", { name: "删除接入点第 2 项", exact: true })
      .click();
    assert.equal(
      await page
        .getByLabel("无线密码", { exact: true })
        .evaluate((el) => el.required),
      true,
    );
  } finally {
    await putConfig(context, original);
    await context.close();
  }
});

test("overview polling resumes after hidden or busy ticks and stops on navigation", async () => {
  const { page, context } = await pageAt("plugins");
  try {
    await page.evaluate(() => {
      const originalSet = window.setTimeout, originalClear = window.clearTimeout;
      const timers = new Map();
      let serial = -1;
      window.setTimeout = (fn, delay, ...args) => {
        if (delay !== 15000) return originalSet(fn, delay, ...args);
        const id = serial--;
        timers.set(id, () => fn(...args));
        return id;
      };
      window.clearTimeout = (id) => {
        if (!timers.delete(id)) originalClear(id);
      };
      window.overviewTimers = timers;
      window.fireOverviewTick = () => {
        const [id, fn] = timers.entries().next().value;
        timers.delete(id);
        fn();
      };
    });
    await navigate(page, "overview");
    for (const reason of ["hidden", "busy"]) {
      await page.evaluate((reason) => {
        Object.defineProperty(document, "visibilityState", { configurable: true, value: reason === "hidden" ? "hidden" : "visible" });
        window.app.state.busy = reason === "busy";
        window.fireOverviewTick();
      }, reason);
      assert.equal(await page.evaluate(() => window.overviewTimers.size), 1, `${reason} tick must rearm polling`);
      const refreshed = page.waitForResponse((r) => r.url().endsWith("/api/v1/status"));
      await page.evaluate(() => {
        Object.defineProperty(document, "visibilityState", { configurable: true, value: "visible" });
        window.app.state.busy = false;
        window.fireOverviewTick();
      });
      await refreshed;
      await page.waitForFunction(() => window.overviewTimers.size === 1);
    }
    await navigate(page, "wan");
    assert.equal(await page.evaluate(() => window.overviewTimers.size), 0, "leaving overview must cancel its timer");
  } finally {
    await context.close();
  }
});

test("overview polling keeps content through slow and failed requests, ignores stale responses, and preserves focus", async () => {
  const { page, context } = await pageAt("plugins");
  try {
    await page.evaluate(() => {
      const originalSet = window.setTimeout, originalClear = window.clearTimeout;
      const timers = new Map();
      let serial = -1;
      window.setTimeout = (fn, delay, ...args) => {
        if (delay !== 15000) return originalSet(fn, delay, ...args);
        const id = serial--;
        timers.set(id, () => fn(...args));
        return id;
      };
      window.clearTimeout = (id) => {
        if (!timers.delete(id)) originalClear(id);
      };
      window.overviewTimers = timers;
      window.fireOverviewTick = () => {
        const [id, fn] = timers.entries().next().value;
        timers.delete(id);
        fn();
      };
    });
    await navigate(page, "overview");
    const pending = [];
    await page.route("**/api/v1/clients", route => pending.push(route));
    const tick = async () => {
      const count = pending.length;
      await page.evaluate(() => {
        Object.defineProperty(document, "visibilityState", { configurable: true, value: "visible" });
        window.app.state.busy = false;
        window.fireOverviewTick();
      });
      await page.waitForFunction(async count => (await window.pendingClientsCount()) > count, count);
    };
    await page.exposeFunction("pendingClientsCount", () => pending.length);
    const leaseMetric = page.locator(".metric-value").nth(1);
    const oldContent = await leaseMetric.innerText();
    await tick();
    assert.equal(await leaseMetric.innerText(), oldContent);
    assert.equal(await page.locator("#page-content").getAttribute("aria-busy"), "false");
    assert.equal(await page.getByText("页面加载中").count(), 0);
    await pending.shift().fulfill({ status: 503, json: { error: "temporary" } });
    await page.waitForFunction(() => window.overviewTimers.size === 1);
    assert.equal(await leaseMetric.innerText(), oldContent);

    await tick();
    const focus = await page.evaluate(() => {
      const input = document.createElement("input");
      input.id = "overview-focus-probe";
      document.querySelector("#page-content").append(input);
      input.focus();
      return document.activeElement === input;
    });
    assert.equal(focus, true);
    await pending.shift().fulfill({ json: { source: "leases", clients: [{ hostname: "poll-client" }] } });
    await page.waitForFunction(() => window.overviewTimers.size === 1);
    assert.equal(await page.locator("#overview-focus-probe").evaluate(el => document.activeElement === el), true);
    assert.equal(await leaseMetric.innerText(), "1");
    assert.equal(await page.locator("#overview-focus-probe").count(), 1);

    await tick();
    await navigate(page, "wan");
    const wanContent = await page.locator("#page-content").innerText();
    await pending.shift().fulfill({ json: { source: "leases", clients: [] } });
    assert.equal(await page.locator("#page-content").innerText(), wanContent);
    assert.equal(await page.evaluate(() => window.overviewTimers.size), 0);
  } finally {
    await context.close();
  }
});

for (const persistent of [false, true]) {
  test(`apply polling survives ${persistent ? "persistent" : "transient"} disconnect without resubmitting`, async () => {
    const { page, context } = await pageAt("plugins");
    try {
      let posts = 0, reads = 0;
      await page.route("**/api/v1/apply", route => {
        posts++;
        return route.fulfill({ json: { jobId: "disconnect-test", status: "running" } });
      });
      await page.route("**/api/v1/jobs/disconnect-test", route => {
        reads++;
        if (persistent || reads === 1) return route.abort("connectionfailed");
        return route.fulfill({ json: { status: "success", payload: JSON.stringify({ mode: "managed-runtime", appliedRuntime: true }) } });
      });
      // Accelerate only the poll delay, leaving request timeout behavior intact.
      await page.evaluate(() => {
        const timeout = window.setTimeout;
        window.setTimeout = (fn, ms, ...args) => timeout(fn, ms === 1000 ? 10 : ms, ...args);
      });
      await waitAction(page, "#apply-run");
      assert.equal(posts, 1);
      assert.ok(reads > 1 && reads <= 30, `bounded retries: ${reads}`);
      const message = await page.locator("#apply-msg").innerText();
      const job = await page.evaluate(() => sessionStorage.getItem("router-apply-job"));
      if (persistent) {
        assert.match(message, /尚未确认.*disconnect-test/);
        assert.match(message, /重新连接/);
        assert.doesNotMatch(message, /应用失败/);
        assert.equal(job, "disconnect-test");
      } else {
        assert.match(message, /网络配置应用完成/);
        assert.equal(job, null);
      }
    } finally { await context.close(); }
  });
}

for (const persistent of [false, true]) {
  test(`apply polling retries real AbortController ${persistent ? "persistent" : "transient"} timeouts`, async () => {
    const { page, context } = await pageAt("plugins");
    try {
      let posts = 0, reads = 0;
      await page.route("**/api/v1/apply", route => {
        posts++;
        return route.fulfill({ json: { jobId: "timeout-test", status: "running" } });
      });
      await page.route("**/api/v1/jobs/timeout-test", route => {
        reads++;
        // Leave the real browser fetch pending until api() aborts its signal.
        // Do not synthesize an AbortError or abort the route ourselves.
        if (persistent || reads === 1) return;
        return route.fulfill({ json: { status: "success", payload: JSON.stringify({ mode: "managed-runtime", appliedRuntime: true }) } });
      });
      await page.evaluate(() => {
        const timeout = window.setTimeout;
        window.setTimeout = (fn, ms, ...args) => timeout(fn, ms === 1000 ? 10 : ms === 5000 ? 100 : ms, ...args);
        const nativeFetch = window.fetch;
        window.jobTimeoutAborts = 0;
        window.jobFetchAttempts = 0;
        window.fetch = (url, options) => {
          if (String(url).includes("/api/v1/jobs/timeout-test")) {
            window.jobFetchAttempts++;
            options.signal.addEventListener("abort", () => window.jobTimeoutAborts++, { once: true });
          }
          return nativeFetch(url, options);
        };
      });
      await waitAction(page, "#apply-run");
      assert.equal(posts, 1, "polling must never resubmit Apply");
      const attempts = await page.evaluate(() => window.jobFetchAttempts);
      const aborts = await page.evaluate(() => window.jobTimeoutAborts);
      // Under load a 100 ms abort can fire before Playwright receives the
      // routed request. Count the browser's actual fetch attempts and signals,
      // not only requests that happened to reach the interception callback.
      assert.ok(reads > 0 && reads <= attempts, `routed ${reads}, attempted ${attempts}`);
      if (persistent) {
        assert.equal(attempts, 30);
        assert.equal(aborts, 30);
      } else {
        assert.ok(reads >= 2, `response eventually delivered after ${reads} requests`);
        assert.ok(attempts >= 2 && attempts <= 30);
        assert.equal(aborts, attempts - 1);
      }
      const message = await page.locator("#apply-msg").innerText();
      const job = await page.evaluate(() => sessionStorage.getItem("router-apply-job"));
      if (persistent) {
        assert.match(message, /尚未确认.*timeout-test/);
        assert.match(message, /重新连接/);
        assert.doesNotMatch(message, /应用失败/);
        assert.equal(job, "timeout-test");
      } else {
        assert.match(message, /网络配置应用完成/);
        assert.equal(job, null);
      }
    } finally { await context.close(); }
  });
}

test("two editors reject stale saves and retain input until explicit reload", async () => {
  const first = await pageAt("plugins");
  const second = await pageAt("plugins");
  const original = await configOf(first.context);
  try {
    await navigate(first.page, "system");
    await navigate(second.page, "system");
    await first.page.getByLabel("主机名", { exact: true }).fill("first-editor");
    await second.page.getByLabel("主机名", { exact: true }).fill("second-editor");
    await waitAction(first.page, "#apply-save");
    await waitAction(second.page, "#apply-save");
    assert.match(await second.page.locator("#apply-msg").innerText(), /其他会话修改/);
    assert.equal(await second.page.getByLabel("主机名", { exact: true }).inputValue(), "second-editor");
    assert.equal(await second.page.locator("#apply-dirty").isVisible(), true);
    assert.equal((await configOf(first.context)).system.hostname, "first-editor");
    await navigate(second.page, "plugins");
    await navigate(second.page, "system");
    await second.page.getByLabel("主机名", { exact: true }).fill("second-editor");
    await waitAction(second.page, "#apply-save");
    assert.equal((await configOf(first.context)).system.hostname, "second-editor");
  } finally {
    await putConfig(first.context, original);
    await first.context.close();
    await second.context.close();
  }
});

test("field validation preserves input and focuses the rejected configuration field", async () => {
  const {page,context}=await pageAt("plugins");
  const original=await configOf(context);
  try {
    await navigate(page,"wan");
    await page.getByLabel("上网方式",{exact:true}).selectOption("static");
    const address=page.getByLabel("静态地址 / CIDR",{exact:true});
    await address.fill("invalid-address");
    await waitAction(page,"#apply-save");
    assert.equal(await address.getAttribute("aria-invalid"),"true");
    assert.equal(await address.inputValue(),"invalid-address");
    assert.equal(await address.evaluate(el=>document.activeElement===el),true);
    assert.equal(await page.locator("#apply-dirty").isVisible(),true);
    const described=await address.getAttribute("aria-describedby");
    assert.ok(described?.includes("-error"));
    await address.fill("10.20.30.2/24");
    assert.equal(await address.getAttribute("aria-invalid"),null);
    await waitAction(page,"#apply-save");
    assert.equal((await configOf(context)).wan.static.addressCidr,"10.20.30.2/24");
  } finally {await putConfig(context,original);await context.close();}
});

test("field validation identifies the second array row without marking the first", async()=>{
 const {page,context}=await pageAt("plugins");const original=await configOf(context);
 try {
  const fixture=JSON.parse(JSON.stringify(original));
  fixture.lan.staticLeases=[{mac:"00:11:22:33:44:55",ip:"192.168.1.10"},{mac:"00:11:22:33:44:66",ip:"192.168.1.11"}];
  await putConfig(context,fixture);await navigate(page,"lan");
  const ips=page.getByLabel("IP 地址",{exact:true});
  await ips.nth(1).fill("not-an-ip");await waitAction(page,"#apply-save");
  assert.equal(await ips.nth(0).getAttribute("aria-invalid"),null);
  assert.equal(await ips.nth(1).getAttribute("aria-invalid"),"true");
  assert.equal(await ips.nth(1).getAttribute("data-config-path"),"lan.staticLeases.1.ip");
  assert.equal(await ips.nth(1).evaluate(el=>document.activeElement===el),true);
 } finally {await putConfig(context,original);await context.close();}
});

test("field validation renders text safely and keeps other errors when editing one field",async()=>{
 const {page,context}=await pageAt("plugins");
 try {
  await navigate(page,"lan");
  const message='<img src=x onerror="window.errorInjected=true">';
  await page.route("**/api/v1/config",async route=>{
   if(route.request().method()!=="PUT")return route.continue();
   await route.fulfill({status:400,contentType:"application/json",body:JSON.stringify({code:"invalid_config",message:"检查输入",fieldErrors:{"lan.dhcp.rangeStart":message,"lan.dhcp.rangeEnd":"结束地址错误","wan.static.gateway":"其他页面字段错误"}})});
  });
  await page.getByLabel("地址池起始地址",{exact:true}).fill("192.168.1.30");await waitAction(page,"#apply-save");
  assert.equal(await page.locator(".field-error img").count(),0);
  assert.equal(await page.evaluate(()=>window.errorInjected),undefined);
  assert.ok((await page.locator(".field-error").allTextContents()).includes(message));
  assert.match(await page.locator("#apply-msg").innerText(),/其他页面字段错误/);
  await page.getByLabel("地址池起始地址",{exact:true}).fill("192.168.1.31");
  assert.equal(await page.getByLabel("地址池起始地址",{exact:true}).getAttribute("aria-invalid"),null);
  assert.equal(await page.getByLabel("地址池结束地址",{exact:true}).getAttribute("aria-invalid"),"true");
 } finally {await context.close();}
});

test("secret clearing is explicit, reversible, and cancelled by a replacement value",async()=>{
 const {page,context}=await pageAt("plugins");const original=await configOf(context);
 try {
  const cfg=structuredClone(original);cfg.plugins ||= {};cfg.plugins.tailscale={enable:false,config:{authKey:"saved-auth-key"}};
  await putConfig(context,cfg);await navigate(page,"plugins/tailscale");
  const clear=page.getByLabel("清空认证密钥",{exact:true});
  assert.equal(await clear.count(),1);
  await clear.check();await clear.uncheck();await waitAction(page,"#apply-save");
  assert.equal((await configOf(context)).plugins.tailscale.config.authKey,"saved-auth-key");
  await clear.check();await page.getByLabel("认证密钥",{exact:true}).fill("replacement-key");
  assert.equal(await clear.isChecked(),false);await waitAction(page,"#apply-save");
  assert.equal((await configOf(context)).plugins.tailscale.config.authKey,"replacement-key");
  await clear.check();await waitAction(page,"#apply-save");
  assert.equal((await configOf(context)).plugins.tailscale.config.authKey,"");
 } finally {await putConfig(context,original);await context.close();}
});

test("secret clearing follows an array item after deleting its preceding row",async()=>{
 const {page,context}=await pageAt("plugins");const original=await configOf(context);
 try {
  const cfg=structuredClone(original);cfg.wifi.aps=[{ssid:"first",psk:"first-password",enable:false},{ssid:"second",psk:"second-password",enable:false}];
  await putConfig(context,cfg);await navigate(page,"wifi");
  const clear=page.getByLabel("清空无线密码",{exact:true});assert.equal(await clear.count(),2);
  await clear.nth(1).check();await page.getByRole("button",{name:"删除接入点第 1 项",exact:true}).click();
  assert.equal(await clear.first().isChecked(),true);await waitAction(page,"#apply-save");
  const saved=(await configOf(context)).wifi.aps;assert.equal(saved.length,1);assert.equal(saved[0].ssid,"second");assert.equal(saved[0].psk ?? "","");
 } finally {await putConfig(context,original);await context.close();}
});

test("secret clearing in advanced JSON follows its reference when rows are reordered",async()=>{
 const {page,context}=await pageAt("plugins");const original=await configOf(context);
 try {
  const cfg=structuredClone(original);cfg.plugins ||= {};
  cfg.plugins["clear-plugin"]={enable:false,config:{peers:[{name:"a","token.with/slash":"secret-a"},{name:"b","token.with/slash":"secret-b"}]}};
  await putConfig(context,cfg);await navigate(page,"plugins/clear-plugin");
  const choice=page.getByLabel("清空密钥：peers.1.token.with/slash",{exact:true});assert.equal(await choice.count(),1);await choice.check();
  const json=page.getByLabel("高级配置（JSON）");const data=JSON.parse(await json.inputValue());
  const duplicate=structuredClone(data);duplicate.peers.push(structuredClone(data.peers[1]));
  await json.fill(JSON.stringify(duplicate));
  assert.match(await json.evaluate(el=>el.validationMessage),/待清空密钥的标记被复制/);
  assert.equal((await configOf(context)).plugins["clear-plugin"].config.peers[1]["token.with/slash"],"secret-b");
  data.peers.reverse();await json.fill(JSON.stringify(data));
  assert.equal(await json.evaluate(el=>el.validationMessage),"");
  assert.equal(await page.getByLabel("清空密钥：peers.0.token.with/slash",{exact:true}).isChecked(),true);
  await waitAction(page,"#apply-save");const saved=(await configOf(context)).plugins["clear-plugin"].config.peers;
  assert.equal(saved[0].name,"b");assert.equal(saved[0]["token.with/slash"],"");assert.equal(saved[1]["token.with/slash"],"secret-a");
 } finally {await putConfig(context,original);await context.close();}
});

test("secret clearing retains intent on validation failure and succeeds after disabling its use",async()=>{
 const {page,context}=await pageAt("plugins");const original=await configOf(context);
 try {
  const cfg=structuredClone(original);cfg.wan.mode="pppoe";cfg.wan.pppoe={username:"user",password:"required-password"};
  await putConfig(context,cfg);await navigate(page,"wan");
  const clear=page.getByLabel("清空PPPoE 密码",{exact:true});assert.equal(await clear.count(),1);await clear.check();await waitAction(page,"#apply-save");
  assert.equal(await clear.isChecked(),true);assert.equal(await page.locator("#apply-dirty").isVisible(),true);
  assert.equal((await configOf(context)).wan.pppoe.password,"required-password");
  assert.equal(await page.getByLabel("PPPoE 密码",{exact:true}).getAttribute("aria-invalid"),"true");
  await page.getByLabel("上网方式",{exact:true}).selectOption("dhcp");await waitAction(page,"#apply-save");
  assert.equal((await configOf(context)).wan.pppoe.password,"");
 } finally {await putConfig(context,original);await context.close();}
});

test("advanced array blank preserves secrets and ambiguous edits retain input", async () => {
 const {page,context}=await pageAt("plugins");const original=await configOf(context);
 try {
  const cfg=structuredClone(original);cfg.plugins ||= {};
  cfg.plugins.arrayblank={enable:false,config:{peers:[{name:"a",token:"saved-token"},{name:"b",token:""}]}};
  await putConfig(context,cfg);await navigate(page,"plugins/arrayblank");
  let json=page.getByLabel("高级配置（JSON）");let data=JSON.parse(await json.inputValue());
  data.peers[0].token="";data.peers.reverse();await json.fill(JSON.stringify(data));await waitAction(page,"#apply-save");
  let stored=(await configOf(context)).plugins.arrayblank.config.peers;
  assert.equal(stored[0].token,"");assert.equal(stored[1].token,"saved-token");
  json=page.getByLabel("高级配置（JSON）");data=JSON.parse(await json.inputValue());
  data.peers[1].name="changed";data.peers[1].token="****";const edited=JSON.stringify(data);
  await json.fill(edited);await waitAction(page,"#apply-save");
  assert.equal(await json.inputValue(),edited);
  assert.equal((await configOf(context)).plugins.arrayblank.config.peers[1].token,"saved-token");
  assert.equal(await page.locator("#apply-dirty").isVisible(),true);
 } finally {await putConfig(context,original);await context.close();}
});

test("WiFi security selection persists SAE and keeps the existing secret", async () => {
  const { page, context } = await pageAt("plugins");
  const original = await configOf(context);
  try {
    const cfg = structuredClone(original);
    cfg.plugins ||= {};
    cfg.wifi.aps = [{ssid:"security-test",band:"2g",channel:6,psk:"preserved-wifi-password",enable:true}];
    await putConfig(context,cfg);
    await navigate(page,"wifi");
    const security = page.getByLabel("安全模式",{exact:true});
    assert.equal(await security.inputValue({timeout:2000}), "");
    await security.selectOption("wpa3-sae");
    await waitAction(page,"#apply-save");
    let saved=await configOf(context);
    assert.equal(saved.wifi.aps[0].security,"wpa3-sae");
    let backup=await (await context.request.get(`${base}/api/v1/backup`)).json();
    assert.equal(backup.config.wifi.aps[0].psk,"preserved-wifi-password");
    await page.locator("#page-refresh").click();
    await page.waitForFunction(()=>!window.app.state.loading);
    assert.equal(await page.getByLabel("安全模式",{exact:true}).inputValue(),"wpa3-sae");
  } finally {
    await putConfig(context,original);
    await context.close();
  }
});

test("independent WiFi subnet editor preserves drafts and explicit access permissions", async()=>{
 const {page,context}=await pageAt("plugins");const original=await configOf(context);
 try {
  const cfg=structuredClone(original);cfg.wifi.enable=true;cfg.wifi.bridgeToLan=true;
  cfg.wifi.aps=[{enable:true,ssid:"routed-ui",psk:"routed-ui-password",band:"2g",channel:6}];
  await putConfig(context,cfg);await navigate(page,"wifi");
  await page.getByLabel("桥接到内网",{exact:true}).uncheck();
  const cidr=page.getByLabel("无线网关 / CIDR",{exact:true});
  assert.equal(await cidr.isVisible({timeout:2000}),true);
  assert.equal(await page.getByLabel("允许无线管理路由器",{exact:true}).isChecked(),false);
  assert.equal(await page.getByLabel("允许有线与无线互访",{exact:true}).isChecked(),false);
  await cidr.fill("192.168.77.1/24");
  await page.getByLabel("无线地址池起始地址",{exact:true}).fill("192.168.77.100");
  await page.getByLabel("无线地址池结束地址",{exact:true}).fill("192.168.77.110");
  await page.getByLabel("无线租约时间（分钟）",{exact:true}).fill("30");
  await page.getByLabel("允许无线管理路由器",{exact:true}).check();
  await waitAction(page,"#apply-save");
  let saved=await configOf(context);
  assert.equal(saved.wifi.bridgeToLan,false);assert.equal(saved.wifi.network.ipv4Cidr,"192.168.77.1/24");
  assert.equal(saved.wifi.network.allowManagement,true);assert.equal(saved.wifi.network.allowWiredAccess,false);
  await page.getByLabel("无线网关 / CIDR",{exact:true}).fill("inactive-draft");
  await page.getByLabel("桥接到内网",{exact:true}).check();
  await waitAction(page,"#apply-save");
  saved=await configOf(context);assert.equal(saved.wifi.network.ipv4Cidr,"inactive-draft");
  await page.getByLabel("桥接到内网",{exact:true}).uncheck();
  await waitAction(page,"#apply-save");
  assert.equal(await page.getByLabel("无线网关 / CIDR",{exact:true}).getAttribute("aria-invalid"),"true");
 } finally {await putConfig(context,original);await context.close();}
});

test("WiFi radio standard and numeric channel width persist through the editor", async()=>{
 const {page,context}=await pageAt("plugins");const original=await configOf(context);
 try {
  const cfg=structuredClone(original);cfg.wifi.enable=true;cfg.wifi.bridgeToLan=true;
  cfg.wifi.aps=[{enable:true,ssid:"radio-ui",psk:"radio-ui-password",band:"5g",channel:149,security:"wpa3-sae"}];
  await putConfig(context,cfg);await navigate(page,"wifi");
  await page.getByLabel("无线标准",{exact:true}).selectOption("wifi7");
  await page.getByLabel("信道带宽",{exact:true}).selectOption("80");
  await waitAction(page,"#apply-save");
  const saved=await configOf(context);
  assert.equal(saved.wifi.aps[0].standard,"wifi7");
  assert.equal(saved.wifi.aps[0].channelWidthMHz,80);
  await page.locator("#page-refresh").click();await page.waitForFunction(()=>!window.app.state.loading);
  assert.equal(await page.getByLabel("无线标准",{exact:true}).inputValue(),"wifi7");
  assert.equal(await page.getByLabel("信道带宽",{exact:true}).inputValue(),"80");
 }finally{await putConfig(context,original);await context.close()}
});

test("plugin capabilities expose AdBlock and keep system-owned services disabled", async () => {
  const {page,context}=await pageAt("plugins");
  try {
    assert.equal(await page.getByLabel("启用 Mihomo",{exact:true}).isDisabled(),true);
    assert.equal(await page.getByLabel("启用 广告拦截",{exact:true}).isDisabled(),false);
    assert.match(await page.locator("#page-content").innerText(),/系统管理/);
    await page.goto(`${base}/#/adblock`);
    await page.waitForFunction(()=>window.app.state.route==="adblock"&&!window.app.state.loading);
    assert.equal(await page.getByText("规则订阅",{exact:true}).count(),0);
    await page.goto(`${base}/#/ssh`);
    await page.waitForFunction(()=>window.app.state.route==="ssh"&&!window.app.state.loading);
    assert.equal(await page.getByLabel("启用 SSH",{exact:true}).count(),0);
    assert.match(await page.locator("#page-content").innerText(),/services.openssh/);
  } finally {await context.close()}
});

test("registered module navigation works without a hardcoded page or existing config", async () => {
 const {page,context}=await pageAt("plugins");
 try {
  await page.route("**/api/v1/plugins",async route=>{
   const response=await route.fetch();const body=await response.json();
   body.plugins.push({id:"new-module",title:"新增模块",available:true,runtimeSupported:false,ownership:"router",enabled:false,status:"not-applied",nav:{path:"/new-module"}});
   await route.fulfill({json:body});
  });
  await page.locator("#page-refresh").click();await page.waitForFunction(()=>!window.app.state.loading);
  await page.goto(`${base}/#/plugins/new-module`);await page.waitForFunction(()=>window.app.state.route==="new-module"&&!window.app.state.loading);
  assert.equal(await page.getByLabel("启用 新增模块",{exact:true}).count(),1);
  assert.equal(await page.getByLabel("高级配置（JSON）",{exact:true}).count(),1);
  assert.equal(await page.locator("#section-tabs").getByRole("link",{name:"新增模块",exact:true}).count(),1);
 } finally {await context.close()}
});

test("network overview samples rates and gaps, pauses, and respects session expiry", async () => {
  const endpoint = "**/api/v1/network/live";
  assert.equal((await fetch(base + "/api/v1/network/live")).status, 401);
  const { page, context } = await pageAt("plugins");
  try {
    const live = await context.request.get(base + "/api/v1/network/live");
    assert.equal(live.status(), 200);
    assert.deepEqual(Object.keys(await live.json()).sort(),
      ["clock", "contractDownMbps", "contractUpMbps", "cpuPercent", "lan", "memory", "ports", "sampleMs", "wan"]);
    const replies = [];
    let hits = 0;
    await page.route(endpoint, route => {
      hits++;
      const reply = replies.shift();
      return route.fulfill(reply || { status: 503, json: {} });
    });
    await page.evaluate(() => {
      Object.defineProperty(document, "visibilityState", { configurable: true, value: "visible" });
      const set = window.setTimeout, clear = window.clearTimeout;
      let action;
      window.setTimeout = (fn, delay, ...args) => {
        if (delay < 250 || delay > 2100) return set(fn, delay, ...args);
        action = () => fn(...args);
        return -100;
      };
      window.clearTimeout = id => {
        if (id === -100) action = null;
        else clear(id);
      };
      window.hasTrafficTick = () => Boolean(action);
      window.takeTrafficTick = () => {
        const tick = action;
        action = null;
        tick();
      };
    });
    const sample = (ms, rxBytes, txBytes, identity = "4/1", independentWiFi = true) => ({ json: {
      clock: "test", sampleMs: ms, contractDownMbps: 300, contractUpMbps: 30,
      cpuPercent: ms === 100 ? null : 0,
      memory: { totalBytes: 1073741824, usedBytes: ms === 100 ? 0 : 536870912 },
      wan: { name: "ppp7", identity, link: "up", sampleMs: ms, rxBytes, txBytes },
      lan: { name: "br0", link: "down" },
      ...(independentWiFi ? { wifi: { name: "wlan-test", identity: "6/1", link: "up", sampleMs: ms, rxBytes: txBytes, txBytes: rxBytes } } : {}),
      ports: [{ name: "eth0", role: "wan", link: "up", speedMbps: 2500, duplex: "full" }],
    } });
    replies.push(sample(100, "100", "200"));
    await navigate(page, "overview");
    await page.waitForFunction(() => window.hasTrafficTick());
    const selector = page.locator("#traffic-period");
    await selector.selectOption("300");
    await selector.focus();
    const wan = page.locator(".traffic .grid > .card").first();
    assert.match(await wan.innerText(), /下载 未采集/);
    const cpu = page.locator(".traffic .grid > .card").filter({ has: page.getByRole("heading", { name: "CPU", exact: true }) });
    const memory = page.locator(".traffic .grid > .card").filter({ has: page.getByRole("heading", { name: "内存", exact: true }) });
    assert.match(await cpu.innerText(), /使用率 未采集/);
    assert.match(await memory.innerText(), /0 B \/ 1 GiB（0%）/);
    assert.match(await page.locator(".traffic").innerText(), /br0[\s\S]*接收 未连接/);
    assert.match(await page.locator(".traffic-ports").innerText(), /2500 Mbps\s+全双工/);
    const pulse = async reply => {
      replies.push(reply);
      await page.evaluate(() => window.takeTrafficTick());
      await page.waitForFunction(() => window.hasTrafficTick());
    };
    await pulse(sample(2100, "1000100", "200"));
    assert.match(await wan.innerText(), /下载 4.00 Mbps\s+上传 0 Mbps/);
    const wifi = page.getByRole("heading", { name: "独立 Wi‑Fi 网段", exact: true }).locator("..");
    assert.match(await wifi.innerText(), /wlan-test[\s\S]*接收 0 Mbps\s+发送 4.00 Mbps/);
    assert.match(await cpu.innerText(), /使用率 0%/);
    assert.match(await memory.innerText(), /512 MiB \/ 1 GiB（50.0%）/);
    assert.match(await memory.innerText(), /5 分钟前/);
    await pulse(sample(2100, "1000100", "200"));
    assert.match(await wan.innerText(), /下载 4.00 Mbps/);
    assert.equal(await selector.evaluate(el => el === document.activeElement && el.value === "300"), true);
    await pulse(sample(4100, "90", "200", "4/1", false));
    assert.equal(await wifi.isVisible(), false);
    assert.match(await wan.innerText(), /下载 未采集\s+上传 未采集/);
    await pulse(sample(12100, "2000090", "200"));
    assert.match(await wan.innerText(), /下载 未采集/);
    await pulse(sample(14100, "3000090", "200"));
    assert.match(await wan.innerText(), /下载 4.00 Mbps/);
    await page.evaluate(() => {
      for (const value of ["hidden", "visible"]) {
        Object.defineProperty(document, "visibilityState", { configurable: true, value });
        document.dispatchEvent(new Event("visibilitychange"));
      }
    });
    await pulse(sample(16100, "4000090", "200"));
    assert.match(await wan.innerText(), /下载 未采集/);
    await pulse({ status: 503, json: {} });
    assert.match(await wan.innerText(), /下载 未采集/);
    assert.match(await cpu.innerText(), /使用率 未采集/);
    assert.match(await memory.innerText(), /使用量 未采集/);
    await page.getByRole("button", { name: "暂停" }).click();
    const before = hits;
    await page.evaluate(() => window.takeTrafficTick());
    assert.equal(hits, before);
    await page.getByRole("button", { name: "继续" }).click();
    await pulse(sample(18100, "5000090", "200"));
    assert.match(await wan.innerText(), /下载 未采集/);
    await navigate(page, "wan");
    assert.equal(await page.evaluate(() => window.hasTrafficTick()), false);
    replies.push(sample(20100, "6000090", "200"));
    await navigate(page, "overview");
    await page.waitForFunction(() => window.hasTrafficTick());
    replies.push({ status: 401, json: {} });
    await page.evaluate(() => window.takeTrafficTick());
    await page.locator("#login-submit").waitFor();
    assert.equal(await page.evaluate(() => window.hasTrafficTick()), false);
  } finally {
    await context.close();
  }
});
