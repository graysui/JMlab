import { expect, test, waitForRelay } from "../fixtures/host.mjs";
import { createServer } from "node:http";
import { execFile } from "node:child_process";
import { promisify } from "node:util";

let moduleServer;
let moduleURL;
let relayName;
const exec = promisify(execFile);
const details = {
  "movie:42": { sourceId: "movie:42", title: "测试电影", itemType: "movie", productionYear: 2024, overview: "用于本地浏览器验收的电影。", providerIds: { tmdb: "42" }, entities: [{ name: "测试人物", sourceId: "person:9", providerIds: { tmdb: "9" }, role: "actor" }, { name: "测试人物", sourceId: "person:9", providerIds: { tmdb: "9" }, role: "writer" }] },
  "tv:42": { sourceId: "tv:42", title: "测试剧集", itemType: "series", extraAttributes: { children: [{ sourceId: "tv:42:1", title: "第一季", itemType: "season", indexNumber: 1 }] } },
  "tv:42:1": { sourceId: "tv:42:1", title: "第一季", itemType: "season", extraAttributes: { children: [{ sourceId: "tv:42:1:1", title: "第一集", itemType: "episode", indexNumber: 1 }] } },
  "tv:42:1:1": { sourceId: "tv:42:1:1", title: "第一集", itemType: "episode", overview: "用于选择单集的本地数据。" },
};

test.beforeAll(async () => {
  moduleServer = createServer(async (req, res) => {
    try {
      const config = JSON.parse(Buffer.from(req.headers["x-jmlab-module-config"] || "", "base64").toString());
      if (config.readToken !== "test-only-token") { res.writeHead(401).end(); return; }
      const url = new URL(req.url, "http://localhost");
      res.setHeader("Content-Type", "application/json");
      if (url.pathname === "/api/v1/caps") { res.end(JSON.stringify({ supportedTypes: ["movie", "series", "season", "episode"], supportsSearch: true, supportsStreams: true, supportsRankings: true })); return; }
      if (url.pathname === "/api/v1/stream/discover") { res.end(JSON.stringify({ candidates: [{ candidateId: "video", label: "测试视频", protocol: "http", encryptedPayload: "browser-private-reference" }] })); return; }
      if (url.pathname === "/api/v1/stream/resolve") { res.end(JSON.stringify({ streamUrl: `${moduleURL}/video.mp4`, protocol: "http", requiresProxy: true })); return; }
      if (url.pathname === "/api/v1/stream/probe") { res.end(JSON.stringify({ healthy: true, statusCode: 200, durationMs: 1 })); return; }
      if (url.pathname === "/api/v1/rankings") { res.end(JSON.stringify({ rankingType: "community_top", period: "today", items: [details["movie:42"]] })); return; }
      if (url.pathname === "/api/v1/actions/check-account") { let body = ""; for await (const chunk of req) body += chunk; const input = JSON.parse(body).input; res.end(JSON.stringify({ message: input.password === "temporary-password" ? "第三方操作成功" : "参数错误", values: { state: "已连接", hidden: "not-rendered" } })); return; }
      if (url.pathname === "/api/v1/search") { let body = ""; for await (const chunk of req) body += chunk; const input = JSON.parse(body); const entry = details[input.itemType === "series" ? "tv:42" : "movie:42"]; res.end(JSON.stringify({ items: [entry], total: 1 })); return; }
      if (url.pathname === "/api/v1/detail") { let body = ""; for await (const chunk of req) body += chunk; const input = JSON.parse(body); const detail = details[input.sourceId] || (/^movie:page-\d+$/.test(input.sourceId) ? { sourceId: input.sourceId, title: `分页电影 ${input.sourceId.split("-").at(-1)}`, itemType: "movie" } : null); res.end(JSON.stringify(detail)); return; }
      res.writeHead(404).end();
    } catch { res.writeHead(400).end(); }
  });
  await new Promise((resolve) => moduleServer.listen(0, process.env.E2E_MODULE_BIND_HOST || "127.0.0.1", resolve));
  const port = moduleServer.address().port;
  moduleURL = `http://127.0.0.1:${port}`;
  if (process.env.E2E_DOCKER_CONTAINER) {
    // The test relay keeps secret-bearing RPC on the backend's loopback.
    relayName = `${process.env.E2E_DOCKER_CONTAINER}-module-relay`;
    const target = process.env.E2E_MODULE_RELAY_HOST || "host.docker.internal";
    const relay = `const net=require('node:net');net.createServer(c=>{const s=net.connect(${port},${JSON.stringify(target)});c.pipe(s);s.pipe(c);s.on('error',()=>c.destroy());c.on('error',()=>s.destroy());}).listen(${port},'127.0.0.1')`;
    await exec("docker", ["run", "-d", "--name", relayName, "--network", `container:${process.env.E2E_DOCKER_CONTAINER}`, "--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges:true", "--entrypoint", "node", process.env.E2E_RELAY_IMAGE, "-e", relay]);
    await waitForRelay(relayName, port);
  }
});
test.afterAll(async () => {
  if (relayName) await exec("docker", ["rm", "-f", relayName]);
  moduleServer?.closeAllConnections(); await new Promise((resolve) => moduleServer?.close(resolve));
});

async function login(page, username = process.env.E2E_USERNAME || "e2e-admin", password = process.env.E2E_PASSWORD || "E2e-Playwright-Password-123") {
  await page.goto("/login"); await page.getByLabel("用户名").fill(username); await page.getByLabel("密码", { exact: true }).fill(password); await page.getByRole("button", { name: "登录", exact: true }).click(); await expect(page.getByRole("button", { name: "退出", exact: true })).toBeVisible();
}

test("来源设置、内容添加、季与单集及个人清单可以实际保存", async ({ page, browser }, testInfo) => {
  test.setTimeout(90000);
  await login(page);
  await page.getByRole("navigation", { name: "主导航" }).getByRole("link", { name: "模块管理", exact: true }).click();
  await expect(page.getByRole("heading", { name: "模块管理", exact: true })).toBeVisible();
  await page.getByRole("link", { name: "添加模块", exact: true }).click();
  const manifest = { id: "browser-metadata", name: "测试元数据模块", version: "1", type: "metadata", supportedItemTypes: ["movie", "series", "season", "episode"], transport: { type: "http", url: moduleURL }, configSchema: { readToken: { type: "password", label: "读取凭证", secret: true, required: true } } };
  const template = page.locator("article").filter({ has: page.getByRole("heading", { name: manifest.name, exact: true }) });
  await page.getByLabel("模块清单文件").setInputFiles({ name: "manifest.json", mimeType: "application/json", buffer: Buffer.from(JSON.stringify(manifest)) });
  await page.getByRole("button", { name: "读取清单并设置" }).click();
  await page.getByLabel("模块服务地址").fill(moduleURL);
  await page.getByLabel("读取凭证", { exact: false }).fill("test-only-token");
  await page.getByRole("button", { name: "保存设置" }).click();
  await expect(page).toHaveURL(/\/admin\/modules\/browser-metadata$/);
  await page.reload();
  await expect(page.getByLabel("读取凭证", { exact: false })).toHaveValue("");
  await expect(page.getByLabel("读取凭证", { exact: false })).toHaveAttribute("placeholder", "已保存，留空保留");
  await page.getByRole("button", { name: "保存设置" }).click();
  await expect(page.getByRole("status")).toContainText("模块设置已保存");

  await page.goto("/settings/discover");
  await page.getByLabel("信息来源").selectOption("browser-metadata");
  await page.getByLabel("片名或关键词").fill("测试"); await page.getByRole("button", { name: "搜索来源" }).click();
  await page.getByRole("button", { name: "添加到媒体库" }).click();
  await expect(page.getByRole("status")).toContainText("已添加");
  await page.getByRole("link", { name: "打开详情", exact: true }).click();
  await expect(page.getByRole("heading", { name: "测试电影", exact: true })).toBeVisible();
  await expect(page.getByText("暂未找到可用视频", { exact: false })).toBeVisible();
  await expect(page.locator("body")).not.toContainText("media:media_");
  const moviePath = new URL(page.url()).pathname;
  await page.getByRole("link", { name: "测试人物 · 编剧", exact: true }).click();
  await expect(page.getByRole("heading", { name: "测试人物", exact: true })).toBeVisible();
  await page.getByRole("button", { name: "收藏人物", exact: true }).click();
  await expect(page.getByRole("button", { name: "已收藏人物", exact: true })).toBeVisible();
  await page.getByLabel("个人备注").fill("我喜欢的编剧"); await page.getByRole("button", { name: "保存备注", exact: true }).click();
  await expect(page.getByText("已保存", { exact: true })).toBeVisible();
  await page.reload(); await expect(page.getByLabel("个人备注")).toHaveValue("我喜欢的编剧");
  await expect(page.getByRole("button", { name: "已收藏人物", exact: true })).toBeVisible();
  await page.goto("/settings/relations");
  await expect(page.getByRole("row").filter({ hasText: "测试人物" })).toBeVisible();
  await page.goto(moviePath);
  const poster = await page.request.get(`/api/v1${moviePath}/images/poster`);
  expect(await poster.text()).toContain("测试电影");
  await page.reload(); await expect(page.getByRole("heading", { name: "测试电影", exact: true })).toBeVisible();
  await page.getByRole("button", { name: "☆ 收藏", exact: true }).click();
  await expect(page.getByRole("button", { name: "★ 已收藏", exact: true })).toHaveAttribute("aria-pressed", "true");
  await page.getByText("评分与备注", { exact: true }).click(); await page.getByLabel("我的评分（0～10 分）").fill("8"); await page.getByLabel("私人备注").fill("测试备注"); await page.getByRole("button", { name: "保存评分与备注" }).click();
  await expect(page.getByRole("status")).toContainText("已保存到你的账号");
  await page.reload(); await page.getByText("评分与备注", { exact: true }).click(); await expect(page.getByLabel("我的评分（0～10 分）")).toHaveValue("8");
  await page.getByLabel("我的评分（0～10 分）").fill(""); await page.getByRole("button", { name: "保存评分与备注" }).click(); await expect(page.getByRole("status")).toContainText("已保存到你的账号");
  await page.reload(); await page.getByText("评分与备注", { exact: true }).click(); await expect(page.getByLabel("我的评分（0～10 分）")).toHaveValue(""); await expect(page.getByLabel("私人备注")).toHaveValue("测试备注");
  await page.getByLabel("私人备注").fill(""); await page.getByRole("button", { name: "保存评分与备注" }).click();
  await page.getByText("加入我的清单", { exact: true }).click();
  await page.getByLabel("新清单名称").fill("周末想看"); await page.getByRole("button", { name: "加入清单", exact: true }).click();
  await expect(page.getByText("已加入清单。", { exact: true })).toBeVisible();
  await page.getByRole("link", { name: "查看我的清单" }).click(); await page.getByRole("button", { name: /周末想看/ }).click();
  await expect(page.getByRole("link", { name: /测试电影/ })).toBeVisible();
  await page.getByRole("button", { name: "移除", exact: true }).click(); await expect(page.getByRole("link", { name: /测试电影/ })).toHaveCount(0);

  const adminSession = await page.evaluate(() => JSON.parse(sessionStorage.getItem("jmlab.session")));
  const authHeaders = { Authorization: `Bearer ${adminSession.accessToken}` };
	const libraryCreated = await page.request.post("/api/v1/libraries", { headers: authHeaders, data: { name: "测试内容库", itemType: "mixed", filter: { metadataModuleId: "browser-metadata" }, isPublic: true, isAdult: false } });
	expect(libraryCreated.ok()).toBeTruthy();
	const viewerLibraryID = (await libraryCreated.json()).id;
  const installed = await page.request.post("/api/v1/admin/modules", { headers: authHeaders, data: { enabled: true, manifest: { id: "browser-stream", name: "测试文件来源", version: "1", type: "stream", transport: { type: "http", url: moduleURL }, mediaAccess: { allowedOrigins: [moduleURL], allowPrivate: true }, configSchema: { readToken: { type: "password", label: "连接令牌", secret: true } } }, config: { readToken: "test-only-token" } } });
  expect(installed.ok()).toBeTruthy();
  const movieID = decodeURIComponent(moviePath.split("/").at(-1));
  const discovered = await page.request.get(`/api/v1/items/${encodeURIComponent(movieID)}/sources`, { headers: authHeaders });
  expect((await discovered.json()).items).toEqual(expect.arrayContaining([expect.objectContaining({ moduleId: "browser-stream", providerId: "" })]));
  await page.goto(moviePath); await page.getByRole("link", { name: "管理播放源", exact: true }).click();
  await expect(page).toHaveURL(/settings\/sources\?itemId=/);
  await expect(page.getByRole("heading", { name: "测试电影", exact: true })).toBeVisible();
  const sourceRow = page.getByRole("row").filter({ hasText: "测试视频" });
  await expect(sourceRow).toContainText("测试文件来源");
  await sourceRow.getByRole("button", { name: "探测", exact: true }).click(); await expect(sourceRow).toContainText("1/1");
  await sourceRow.getByRole("button", { name: "编辑", exact: true }).click();
  await expect(page.getByLabel("候选地址", { exact: true })).toHaveCount(0);
  await expect(page.getByText(/播放地址由相关模块管理/)).toBeVisible();
  await page.getByLabel("名称", { exact: true }).fill("客厅视频"); await page.getByRole("button", { name: "保存修改", exact: true }).click();
  await expect(page.getByRole("status").filter({ hasText: "播放源已更新" })).toBeVisible();
  await page.reload();
  await expect(page.getByRole("heading", { name: "测试电影", exact: true })).toBeVisible();
  await expect(page.getByRole("row").filter({ hasText: "客厅视频" })).toContainText("1/1");
  await page.goto("/settings/sources");
  await page.getByLabel("查找作品", { exact: true }).fill("测试电影");
  await page.getByRole("button", { name: "查找作品", exact: true }).click();
  const foundMovie = page.getByRole("article").filter({ hasText: "测试电影" });
  await foundMovie.getByRole("button", { name: "管理播放源", exact: true }).click();
  await expect(page.getByRole("row").filter({ hasText: "客厅视频" })).toContainText("1/1");
  await page.setViewportSize({ width: 390, height: 844 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBeTruthy();
  const mobileEdit = page.getByRole("row").filter({ hasText: "客厅视频" }).getByRole("button", { name: "编辑", exact: true });
  await mobileEdit.scrollIntoViewIfNeeded(); await expect(mobileEdit).toBeInViewport();
  expect(await mobileEdit.evaluate((element) => element.getBoundingClientRect().right <= window.innerWidth)).toBeTruthy();
  await mobileEdit.click(); await expect(page.getByLabel("名称", { exact: true })).toHaveValue("客厅视频");
  await page.getByRole("button", { name: "取消", exact: true }).click();
  await page.evaluate(() => window.scrollTo(0, 0));
  await page.screenshot({ path: testInfo.outputPath("sources-mobile.png"), fullPage: true });
  await page.setViewportSize({ width: 1280, height: 800 });

  await page.goto("/settings/discover"); await page.getByLabel("信息来源").selectOption("browser-metadata"); await page.getByLabel("内容类型").selectOption("series"); await page.getByLabel("片名或关键词").fill("测试"); await page.getByRole("button", { name: "搜索来源" }).click(); await page.getByRole("button", { name: "添加到媒体库" }).click(); await page.getByRole("link", { name: "打开详情", exact: true }).click();
  await expect(page.getByRole("link", { name: "选择季与单集" })).toBeVisible();
  await expect(page.getByRole("link", { name: "▶ 播放", exact: true })).toHaveCount(0);
  await page.getByText("从来源添加季", { exact: true }).click(); await page.getByRole("button", { name: "添加", exact: true }).click(); await page.getByRole("link", { name: "打开详情", exact: true }).click();
  await page.getByText("从来源添加单集", { exact: true }).click(); await page.getByRole("button", { name: "添加全部单集", exact: true }).click(); await expect(page.getByRole("status")).toContainText("添加完成"); await page.getByRole("link", { name: "打开详情", exact: true }).click(); await expect(page.getByRole("heading", { name: "第一集", exact: true })).toBeVisible();

  for (let i = 0; i < 25; i++) {
    const added = await page.request.post("/api/v1/metadata/browser-metadata/import", { headers: authHeaders, data: { sourceId: `movie:page-${i}` } });
    expect(added.ok()).toBeTruthy();
  }

  // A viewer uses the same persisted data but receives no management entry.
  const session = await page.evaluate(() => JSON.parse(sessionStorage.getItem("jmlab.session")));
  const created = await page.request.post("/api/v1/admin/users", { headers: { Authorization: `Bearer ${session.accessToken}` }, data: { username: "browser-viewer", displayName: "普通用户", password: "Local-Viewer-Only-2026", role: "Viewer" } }); expect(created.ok()).toBeTruthy();
  const viewerContext = await browser.newContext({ baseURL: process.env.BASE_URL, viewport: { width: 390, height: 844 } }); const viewerPage = await viewerContext.newPage();
  await login(viewerPage, "browser-viewer", "Local-Viewer-Only-2026"); await viewerPage.goto(moviePath);
  await expect(viewerPage.getByRole("heading", { name: "测试电影", exact: true })).toBeVisible(); await expect(viewerPage.getByRole("link", { name: "管理后台", exact: true })).toHaveCount(0);
  await expect(viewerPage.getByRole("button", { name: "☆ 收藏", exact: true })).toBeVisible();
  await expect(viewerPage.getByRole("link", { name: "管理播放源", exact: true })).toHaveCount(0);
  await expect(viewerPage.getByRole("link", { name: "模块管理", exact: true })).toHaveCount(0);
  await expect(viewerPage.getByRole("navigation", { name: "主导航" })).toBeVisible();
  expect(await viewerPage.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBeTruthy();
  await viewerPage.screenshot({ path: testInfo.outputPath("viewer-mobile.png"), fullPage: true });
  await viewerPage.getByRole("link", { name: "媒体库", exact: true }).click();
  await expect(viewerPage.getByText("已显示 24 部内容", { exact: true })).toBeVisible();
  await viewerPage.getByRole("button", { name: "加载更多", exact: true }).click();
  await expect(viewerPage.getByText("已显示 27 部内容", { exact: true })).toBeVisible();
  await expect(viewerPage.getByRole("link", { name: /第一集|第一季/ })).toHaveCount(0);
  await viewerPage.getByRole("combobox", { name: "内容类型", exact: true }).selectOption("series");
  await expect(viewerPage.getByRole("link", { name: /测试剧集/ })).toBeVisible();
  await expect(viewerPage.getByRole("link", { name: /分页电影/ })).toHaveCount(0);
  await viewerPage.getByRole("combobox", { name: "媒体库范围", exact: true }).selectOption(viewerLibraryID);
  await viewerPage.getByRole("combobox", { name: "排序方式", exact: true }).selectOption("title");
  await viewerPage.reload(); await expect(viewerPage.getByRole("combobox", { name: "内容类型", exact: true })).toHaveValue("series");
  await expect(viewerPage.getByRole("link", { name: /测试剧集/ })).toBeVisible();
  await viewerPage.getByRole("combobox", { name: "内容类型", exact: true }).selectOption("video");
  await expect(viewerPage.getByRole("heading", { name: "这里还没有可见内容" })).toBeVisible();
  expect(await viewerPage.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBeTruthy();
  await viewerPage.screenshot({ path: testInfo.outputPath("library-mobile.png"), fullPage: true });
  await viewerPage.getByRole("link", { name: "打开设置", exact: true }).click();
  await viewerPage.getByRole("link", { name: /主题与首页/ }).click();
  const adultOption = viewerPage.getByRole("checkbox", { name: /我已成年/ });
  await expect(adultOption).not.toBeChecked();
  await adultOption.check(); await viewerPage.getByRole("button", { name: "保存偏好", exact: true }).click(); await expect(viewerPage.getByRole("status")).toContainText("偏好已保存");
  await viewerPage.reload(); await expect(adultOption).toBeChecked();
  await adultOption.uncheck(); await viewerPage.getByRole("button", { name: "保存偏好", exact: true }).click(); await expect(viewerPage.getByRole("status")).toContainText("偏好已保存");
  await viewerPage.reload(); await expect(adultOption).not.toBeChecked();
  await viewerPage.goto("/admin/modules"); await expect(viewerPage.getByRole("heading", { name: "没有管理权限" })).toBeVisible(); await viewerContext.close();
  await page.goto("/admin/modules"); await template.getByRole("button", { name: "停用", exact: true }).click(); await expect(template.getByText("已停用", { exact: true })).toBeVisible(); await template.getByRole("button", { name: "启用", exact: true }).click(); await expect(template.getByText("已启用", { exact: true })).toBeVisible();
  await template.getByRole("link", { name: "设置", exact: true }).click();
  await page.getByRole("link", { name: "状态与维护", exact: true }).click();
  await expect(page.getByRole("heading", { name: "运行状态", exact: true })).toBeVisible();
  await page.goto("/settings/backup");
  const restoreConnections = page.getByRole("checkbox", { name: "同时恢复来源、模块存储和备份目标设置", exact: true });
  await expect(restoreConnections).not.toBeChecked();
  await restoreConnections.check(); await expect(restoreConnections).toBeChecked();
  await expect(page.getByRole("button", { name: "确认恢复备份", exact: true })).toBeDisabled();
});


test("模块页面集中功能设置并支持导入更新和卸载", async ({ page }, testInfo) => {
  await login(page);
  await page.getByRole("navigation", { name: "主导航" }).getByRole("link", { name: "模块管理", exact: true }).click();
  await page.goto("/admin/providers");
  await expect(page).toHaveURL(/\/admin\/modules$/);
  await page.goto("/admin/items");
  await expect(page).toHaveURL(/\/admin\/modules$/);

  const manifest = { rankings: [{ id: "community_top", name: "社区推荐", periods: [{ value: "today", label: "今日" }] }], actions: [{ id: "check-account", name: "检查账户", fields: { password: { type: "password", label: "临时密码", secret: true, required: true } }, columns: { state: "账户状态" } }], id: "ui-lifecycle", name: "可管理的测试模块", version: "1.0.0", type: "metadata", transport: { type: "http", url: moduleURL }, configSchema: { readToken: { type: "password", label: "读取凭证", secret: true, required: true }, mode: { type: "string", label: "测试模式", required: true, default: "safe", options: [{ value: "safe", label: "默认模式" }, { value: "custom", label: "自定义模式" }] }, rules: { type: "json", label: "测试规则", required: true, default: "initial text" } } };
  await page.goto("/admin/modules?view=available");
  await page.getByLabel("模块清单文件").setInputFiles({ name: "manifest.json", mimeType: "application/json", buffer: Buffer.from(JSON.stringify(manifest)) });
  await page.getByRole("button", { name: "读取清单并设置" }).click();
  await page.getByLabel("读取凭证", { exact: true }).fill("test-only-token");
  await expect(page.getByRole("textbox", { name: "测试规则", exact: true })).toHaveValue('"initial text"');
  await page.getByRole("textbox", { name: "测试规则", exact: true }).fill('"saved JSON string"');
  await page.getByRole("combobox", { name: "测试模式", exact: true }).selectOption("custom");
  await page.getByRole("button", { name: "保存设置", exact: true }).click();
  await expect(page).toHaveURL(/\/admin\/modules\/ui-lifecycle$/);
  await page.getByLabel("临时密码", { exact: true }).fill("temporary-password");
  await page.getByRole("button", { name: "检查账户", exact: true }).last().click();
  await expect(page.getByRole("status")).toContainText("第三方操作成功");
  await expect(page.getByLabel("临时密码", { exact: true })).toHaveValue("");
  await expect(page.getByRole("cell", { name: "已连接", exact: true })).toBeVisible();
  await expect(page.getByText("not-rendered", { exact: true })).toHaveCount(0);
  await page.setViewportSize({ width: 320, height: 844 });
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBeTruthy();
  await page.evaluate(() => window.scrollTo(0, 0));
  await page.screenshot({ path: testInfo.outputPath("module-actions-mobile.png"), fullPage: true });
  await page.getByRole("link", { name: "榜单与自动更新", exact: true }).click();
  await expect(page.getByRole("button", { name: "保存自动更新", exact: true })).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBeTruthy();
  await page.evaluate(() => window.scrollTo(0, 0));
  await page.screenshot({ path: testInfo.outputPath("module-rankings-mobile.png"), fullPage: true });
  await page.setViewportSize({ width: 1280, height: 900 });
  await expect(page.getByRole("combobox", { name: "榜单", exact: true })).toHaveValue("community_top");
  await expect(page.getByRole("combobox", { name: "范围", exact: true })).toHaveValue("today");
  await page.getByRole("button", { name: "立即同步所选榜单" }).click();
  await expect(page.getByRole("status")).toContainText("任务已加入队列");
  await page.goto("/admin/modules/ui-lifecycle");
  await page.reload();
  await expect(page.getByRole("textbox", { name: "测试规则", exact: true })).toHaveValue('"saved JSON string"');
  await expect(page.getByRole("combobox", { name: "测试模式", exact: true })).toHaveValue("custom");
  await expect(page.getByLabel("读取凭证", { exact: false })).toHaveAttribute("placeholder", "已保存，留空保留");
  await page.getByRole("button", { name: "停用模块", exact: true }).click();
  await expect(page.getByRole("button", { name: "启用模块", exact: true })).toBeVisible();
  await page.getByRole("button", { name: "保存设置", exact: true }).click();
  await expect(page.getByRole("status")).toContainText("模块设置已保存");
  await expect(page.getByRole("button", { name: "启用模块", exact: true })).toBeVisible();
  await page.getByRole("button", { name: "启用模块", exact: true }).click();
  await page.getByRole("link", { name: "状态与维护", exact: true }).click();
  await page.getByText("更新模块清单", { exact: true }).click();
  await page.getByLabel("清单内容").fill(JSON.stringify({ ...manifest, version: "2.0.0" }));
  await page.getByRole("button", { name: "检查并更新模块" }).click();
  await expect(page.getByRole("status")).toContainText("模块已更新");
  await expect(page.getByText("2.0.0", { exact: true })).toBeVisible();
  await page.getByLabel("清单内容").fill(JSON.stringify({ ...manifest, version: "3.0.0", transport: { type: "http", url: "http://127.0.0.1:1" } }));
  await page.getByRole("button", { name: "检查并更新模块" }).click();
  await expect(page.getByRole("alert")).toBeVisible();
  await expect(page.getByText("2.0.0", { exact: true })).toBeVisible();
  for (const width of [390, 320]) {
    await page.setViewportSize({ width, height: 844 });
    await expect(page.getByRole("link", { name: "打开模块管理", exact: true })).toBeVisible();
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBeTruthy();
  }
  await page.evaluate(() => window.scrollTo(0, 0));
  await page.screenshot({ path: testInfo.outputPath("module-management-mobile.png"), fullPage: true });
  const session = await page.evaluate(() => JSON.parse(sessionStorage.getItem("jmlab.session")));
  const headers = { Authorization: `Bearer ${session.accessToken}` };
  const before = await page.request.get("/api/v1/items?limit=100", { headers });
  const beforeIDs = (await before.json()).items.map((item) => item.id);
  await page.getByRole("button", { name: "卸载模块", exact: true }).click();
  await page.getByRole("button", { name: "确认卸载", exact: true }).click();
  await expect(page).toHaveURL(/\/admin\/modules$/);
  await page.reload();
  await expect(page.getByRole("heading", { name: manifest.name, exact: true })).toHaveCount(0);
  const after = await page.request.get("/api/v1/items?limit=100", { headers });
  expect((await after.json()).items.map((item) => item.id).sort()).toEqual(beforeIDs.sort());
});


test("虚拟库先选模块再读取模板范围，热更新及旧规则保持兼容", async ({ page }, testInfo) => {
  test.setTimeout(90000);
  const errors = []; page.on("pageerror", (error) => errors.push(error.message));
  await login(page);
  const session = await page.evaluate(() => JSON.parse(sessionStorage.getItem("jmlab.session")));
  const headers = { Authorization: `Bearer ${session.accessToken}` };
  const manifest = { id: "library-flow", name: "虚拟库流程模块", version: "1", type: "metadata", supportedItemTypes: ["movie"], transport: { type: "http", url: moduleURL }, configSchema: { readToken: { type: "password", label: "读取凭证", secret: true } }, libraryTemplates: [{ id: "local-items", name: "模块本地作品", description: "仅此模块的已入库作品", itemType: "movie", scope: "metadata", isAdult: false, filter: {}, sort: { field: "title", direction: "asc" } }] };
  const install = await page.request.post("/api/v1/admin/modules", { headers, data: { manifest, enabled: true, config: { readToken: "test-only-token" } } });
  expect(install.ok(), await install.text()).toBeTruthy();
  const imported = await page.request.post("/api/v1/metadata/library-flow/import", { headers, data: { sourceId: "movie:42" } }); expect(imported.ok(), await imported.text()).toBeTruthy();
  try {
    await page.goto("/settings/libraries");
    await page.getByRole("button", { name: "新建媒体库", exact: true }).click();
    const moduleSelect = page.getByRole("combobox", { name: "来源模块", exact: true });
    const templateSelect = page.getByRole("combobox", { name: "2. 预设模板", exact: true });
    const scopeSelect = page.getByRole("combobox", { name: "3. 榜单范围", exact: true });
    const jsonEditor = page.getByRole("textbox", { name: "过滤器 JSON（高级自定义规则）", exact: true });
    const nameInput = page.getByRole("textbox", { name: "名称", exact: true });
    await expect(nameInput).toBeDisabled(); await expect(templateSelect).toHaveCount(0);
    await moduleSelect.selectOption("test-catalog");
    await templateSelect.selectOption("popular-ranking"); await scopeSelect.selectOption("ranking-weekly");
    await page.getByRole("button", { name: "高级 JSON 模式", exact: true }).click();
    expect(JSON.parse(await jsonEditor.inputValue())).toMatchObject({ metadataModuleId: "test-catalog", rankingType: "ranking", rankingPeriod: "weekly" });
    await nameInput.fill("我的自定义名称"); await scopeSelect.selectOption("ranking-monthly"); await expect(nameInput).toHaveValue("我的自定义名称");
    await templateSelect.selectOption("top250-ranking"); await scopeSelect.selectOption("top250-2025");
    await page.getByRole("button", { name: "高级 JSON 模式", exact: true }).click();
    expect(JSON.parse(await jsonEditor.inputValue())).toMatchObject({ rankingType: "top250", rankingPeriod: "2025" });
    await moduleSelect.selectOption("test-stream"); await expect(nameInput).toBeDisabled(); await expect(scopeSelect).toHaveCount(0);
    await templateSelect.selectOption("playable-items");
    await page.getByRole("button", { name: "高级 JSON 模式", exact: true }).click();
    expect(JSON.parse(await jsonEditor.inputValue())).toEqual({ streamModuleId: "test-stream" });

    let failed = false;
    await page.route("**/api/v1/admin/modules/library-flow/library-templates", async (route) => {
      if (!failed) { failed = true; await route.fulfill({ status: 503, contentType: "application/json", body: JSON.stringify({ error: { message: "模板暂时无法读取" } }) }); }
      else await route.continue();
    });
    await moduleSelect.selectOption("library-flow"); await expect(page.getByRole("alert")).toContainText("模板暂时无法读取");
    await expect(nameInput).toBeDisabled(); await page.getByRole("button", { name: "重新读取", exact: true }).click();
    await templateSelect.selectOption("local-items"); await nameInput.fill("模块库流程");
    await page.getByRole("checkbox", { name: "仅包含我的收藏", exact: true }).check();
    await page.getByRole("button", { name: "高级 JSON 模式", exact: true }).click();
    expect(JSON.parse(await jsonEditor.inputValue())).toEqual({ metadataModuleId: "library-flow", isFavorite: true });
    await page.getByRole("checkbox", { name: "仅包含我的收藏", exact: true }).uncheck();
    await page.getByRole("textbox", { name: "发行年份", exact: true }).fill("2024,2025");
    await nameInput.click(); expect(JSON.parse(await jsonEditor.inputValue()).years).toEqual([2024,2025]);
    await page.getByRole("textbox", { name: "发行年份", exact: true }).fill(""); await nameInput.click();
    for (const width of [1280, 375, 320]) {
      await page.setViewportSize({ width, height: 900 });
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBeTruthy();
      await page.screenshot({ path: testInfo.outputPath(`library-template-${width}.png`), fullPage: true });
    }
    await page.getByRole("button", { name: "创建媒体库", exact: true }).click(); await expect(page.getByRole("status")).toContainText("媒体库已创建");
    const card = page.locator("article").filter({ has: page.getByRole("heading", { name: "模块库流程", exact: true }) });
    await card.getByRole("button", { name: "预览数量", exact: true }).click(); await expect(card).toContainText("1 部匹配作品");
    await card.getByRole("button", { name: "编辑", exact: true }).click();
    const original = { all: [{ metadataModuleId: "library-flow" }, { years: [2024] }] };
    await jsonEditor.fill(JSON.stringify(original));
    await expect(page.getByRole("checkbox", { name: "仅包含我的收藏", exact: true })).toBeDisabled();
    await page.getByRole("button", { name: "保存修改", exact: true }).click();
    await card.getByRole("button", { name: "编辑", exact: true }).click();
    expect(JSON.parse(await jsonEditor.inputValue())).toEqual(original);
    await page.getByRole("button", { name: "关闭", exact: true }).click();
    await page.getByRole("button", { name: "新建媒体库", exact: true }).click(); await moduleSelect.selectOption("library-flow"); await templateSelect.selectOption("local-items");
    const updated = { ...manifest, version: "2", libraryTemplates: [{ ...manifest.libraryTemplates[0], name: "更新后的模块模板" }] };
    const update = await page.request.put("/api/v1/admin/modules/library-flow/manifest", { headers, data: { manifest: updated } }); expect(update.ok(), await update.text()).toBeTruthy();
    await page.getByRole("button", { name: "创建媒体库", exact: true }).click(); await expect(page.getByRole("alert")).toContainText("请重新选择模块和模板");
    await moduleSelect.selectOption(""); await moduleSelect.selectOption("library-flow"); await expect(templateSelect).toContainText("更新后的模块模板"); await templateSelect.selectOption("local-items");
    await page.getByRole("button", { name: "创建媒体库", exact: true }).click(); await expect(page.getByRole("status")).toContainText("媒体库已创建");
    await page.getByRole("button", { name: "新建媒体库", exact: true }).click();
    await expect(moduleSelect.locator('option[value="library-flow"]')).toHaveJSProperty("disabled", false);
    const removeTemplates = await page.request.put("/api/v1/admin/modules/library-flow/manifest", { headers, data: { manifest: { ...updated, version: "3", libraryTemplates: [] } } }); expect(removeTemplates.ok()).toBeTruthy();
    await moduleSelect.selectOption("library-flow"); await expect(page.getByText("此模块尚未提供虚拟库模板，可在模块管理中更新清单。", { exact: true })).toBeVisible(); await expect(nameInput).toBeDisabled();
    await page.getByRole("button", { name: "刷新模块列表", exact: true }).click(); await expect(moduleSelect.locator('option[value="library-flow"]')).toHaveJSProperty("disabled", true); await expect(moduleSelect.locator('option[value="library-flow"]')).toContainText("暂无模板");
    const restoreTemplates = await page.request.put("/api/v1/admin/modules/library-flow/manifest", { headers, data: { manifest: { ...updated, version: "4" } } }); expect(restoreTemplates.ok()).toBeTruthy();
    await page.getByRole("button", { name: "关闭", exact: true }).click();
    const disable = await page.request.patch("/api/v1/admin/modules/library-flow", { headers, data: { enabled: false } }); expect(disable.ok()).toBeTruthy();
    await page.getByRole("button", { name: "新建媒体库", exact: true }).click();
    await expect(moduleSelect.locator('option[value="library-flow"]')).toHaveJSProperty("disabled", true);
    await expect(nameInput).toBeDisabled();
    await page.getByRole("button", { name: "关闭", exact: true }).click();
    await card.getByRole("button", { name: "预览数量", exact: true }).click(); await expect(card).toContainText("1 部匹配作品");
    await page.goto("/admin/modules/library-flow?tab=libraries"); await expect(page.getByRole("heading", { name: "更新后的模块模板", exact: true })).toBeVisible();
    expect(errors).toEqual([]);
  } finally {
    await page.request.delete("/api/v1/admin/modules/library-flow", { headers });
  }
});

test("第三方播放模块可以选择适用媒体库并即时限制已有候选", async ({ page }, testInfo) => {
  await login(page);
  const session = await page.evaluate(() => JSON.parse(sessionStorage.getItem("jmlab.session")));
  const headers = { Authorization: `Bearer ${session.accessToken}` };
  const libraryIDs = [];
  const ids = ["scoped-composite", "scoped-backup"];
  const install = async (id, type, libraryScope) => {
    const response = await page.request.post("/api/v1/admin/modules", { headers, data: { enabled: true, libraryScope, manifest: { id, name: id === ids[0] ? "按库提供视频" : "备用视频来源", version: "1", type, transport: { type: "http", url: moduleURL }, supportedItemTypes: ["movie"], configSchema: { readToken: { type: "password", label: "连接凭证", secret: true } }, mediaAccess: { allowedOrigins: [moduleURL], allowPrivate: true } }, config: { readToken: "test-only-token" } } });
    expect(response.ok(), await response.text()).toBeTruthy();
  };
  try {
    await install(ids[0], "composite", { mode: "selected", libraryIds: [] });
    await install(ids[1], "stream", { mode: "all", libraryIds: [] });
    for (const [name, filter] of [["范围测试内容库", { metadataModuleId: ids[0] }], ["范围测试收藏库", { isFavorite: true }], ["范围测试播放库", { streamModuleId: ids[0] }]]) {
      const response = await page.request.post("/api/v1/libraries", { headers, data: { name, filter, itemType: "movie", isPublic: true, isAdult: false } });
      expect(response.ok(), await response.text()).toBeTruthy(); libraryIDs.push((await response.json()).id);
    }
    const imported = await page.request.post(`/api/v1/metadata/${ids[0]}/import`, { headers, data: { sourceId: "movie:42" } });
    expect(imported.ok(), await imported.text()).toBeTruthy(); const itemID = (await imported.json()).item.id;
    const sources = async () => (await (await page.request.get(`/api/v1/items/${itemID}/sources`, { headers })).json()).items;
    expect((await sources()).some((source) => source.moduleId === ids[0])).toBeFalsy();
    await page.goto(`/admin/modules/${ids[0]}`);
    const panel = page.getByRole("form", { name: "播放源适用媒体库", exact: true });
    await expect(panel.getByRole("combobox", { name: "适用范围", exact: true })).toHaveValue("selected");
    await expect(panel.getByRole("checkbox", { name: /^范围测试收藏库/ })).toBeDisabled();
    await expect(panel.getByRole("checkbox", { name: /^范围测试播放库/ })).toBeDisabled();
    await panel.getByRole("checkbox", { name: "范围测试内容库", exact: true }).check();
    await panel.getByRole("button", { name: "保存适用媒体库", exact: true }).click();
    await expect(panel.getByRole("status")).toContainText("即时生效"); await expect(panel).toHaveCount(1);
    await page.reload(); await expect(panel.getByRole("checkbox", { name: "范围测试内容库", exact: true })).toBeChecked();
    const found = await sources();
    expect(found.some((source) => source.moduleId === ids[0])).toBeTruthy();
    expect(found.some((source) => source.moduleId === ids[1])).toBeTruthy();
    const original = found.find((source) => source.moduleId === ids[0]);
    for (const width of [1280, 320]) {
      await page.setViewportSize({ width, height: 900 }); await page.evaluate(() => { document.activeElement?.blur(); window.scrollTo(0, 0); });
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBeTruthy();
      await page.screenshot({ path: testInfo.outputPath(`playback-scope-${width}.png`), fullPage: true });
    }
    await page.setViewportSize({ width: 1280, height: 900 });
    const disabled = await page.request.patch(`/api/v1/libraries/${libraryIDs[0]}`, { headers, data: { enabled: false } }); expect(disabled.ok()).toBeTruthy();
    expect((await sources()).some((source) => source.moduleId === ids[0])).toBeFalsy();
    expect((await sources()).some((source) => source.moduleId === ids[1])).toBeTruthy();
    const resolve = await page.request.post(`/api/v1/items/${itemID}/sources/${original.id}/resolve`, { headers, data: {} }); expect(resolve.status()).toBe(404); expect((await resolve.json()).error.code).toBe("PLAYBACK_SOURCE_NOT_FOUND");
    const deleted = await page.request.delete(`/api/v1/libraries/${libraryIDs[0]}`, { headers }); expect(deleted.ok()).toBeTruthy();
    await page.reload(); await expect(panel.getByRole("checkbox", { name: /已删除的媒体库/ })).toBeChecked();
    await panel.getByRole("checkbox", { name: /已删除的媒体库/ }).click(); await expect(panel.getByRole("checkbox", { name: /已删除的媒体库/ })).toHaveCount(0);
    await panel.getByRole("button", { name: "保存适用媒体库", exact: true }).click(); await expect(panel.getByRole("status")).toContainText("即时生效");
    await panel.getByRole("combobox", { name: "适用范围", exact: true }).selectOption("all"); await panel.getByRole("button", { name: "保存适用媒体库", exact: true }).click(); await expect(panel.getByRole("status")).toContainText("即时生效");
    expect((await sources()).some((source) => source.moduleId === ids[0])).toBeTruthy();
  } finally {
    for (const id of ids) await page.request.delete(`/api/v1/admin/modules/${id}`, { headers });
    for (const id of libraryIDs) await page.request.delete(`/api/v1/libraries/${id}`, { headers });
  }
});
