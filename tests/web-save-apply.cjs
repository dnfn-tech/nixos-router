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
test("single plugin save persists through API and clears draft before apply", async () => {
  const { page, context } = await pageAt("plugins/mihomo");
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
    assert.equal((saved.config || saved).plugins.mihomo.enable, enabled);
    await waitAction(page, "#apply-run");
    assert.match(
      await page.locator("#apply-msg").innerText(),
      /生成完成.*未应用运行态/,
    );
  } finally {
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
  const { page, context } = await pageAt("plugins/mihomo");
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
      (result.config || result).plugins.mihomo.enable,
      savedValue,
      "discarded draft was persisted by another page",
    );
  } finally {
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
  "plugins/mihomo",
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
  return (await (await context.request.get(`${base}/api/v1/config`)).json())
    .config;
}
async function putConfig(context, config) {
  const r = await context.request.put(`${base}/api/v1/config`, {
    data: config,
  });
  assert.equal(r.status(), 200, await r.text());
}
test("core and plugin fields survive real API saves with their correct schema", async () => {
  const { page, context } = await pageAt("plugins");
  const original = await configOf(context);
  try {
    const cfg = structuredClone(original);
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
      enable: true,
      config: {
        vlans: [{ vid: 10, name: "vlan10", bridge: "br-lan" }],
        extra: { keep: true },
      },
    };
    cfg.plugins.zerotier = {
      enable: true,
      config: {
        controlPlane: "private",
        controllerUrl: "https://old.example.com",
        apiToken: "original-token",
        networks: ["0123456789abcdef"],
      },
    };
    cfg.plugins.tailscale = {
      enable: true,
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
      ["ssh", [["监听端口", "2222"]], [["ssh.port", 2222]]],
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
    cfg.plugins.custom = {
      enable: true,
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
test("client edits persist and password changes remain usable", async () => {
  const { page, context } = await pageAt("plugins");
  const original = await configOf(context);
  let changed = false;
  try {
    const cfg = structuredClone(original);
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
    (cfg.plugins ||= {}).arrayplugin = {
      enable: true,
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
      await page.waitForFunction(() => !window.app.state.loading);
      assert.equal(await page.evaluate(() => window.overviewTimers.size), 1);
    }
    await navigate(page, "wan");
    assert.equal(await page.evaluate(() => window.overviewTimers.size), 0, "leaving overview must cancel its timer");
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
