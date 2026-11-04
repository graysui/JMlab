import { expect, test } from "../fixtures/host.mjs";

const adminUsername = process.env.E2E_USERNAME || "e2e-admin";
const adminPassword = process.env.E2E_PASSWORD || "E2e-Playwright-Password-123";

function dashboard(items = 0) {
  return {
    resources: { items, playableItems: 0, people: 0, studios: 0, series: 0, tags: 0, collections: 0, libraries: 0, providers: 0, playbackSources: 0 },
    tasks: [], recentTasks: [], errors: { taskFailures: 0, auditFailures: 0, total: 0 },
  };
}

async function mockOverview(page, { modules = [], libraries = [], items = 0, moduleFailure = false } = {}) {
  await page.route("**/api/v1/**", async (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    if (request.method() === "GET" && path === "/api/v1/dashboard") {
      return route.fulfill({ json: dashboard(items) });
    }
    if (request.method() === "GET" && path === "/api/v1/libraries") {
      return route.fulfill({ json: { items: libraries } });
    }
    if (request.method() === "GET" && path === "/api/v1/admin/modules") {
      if (moduleFailure) return route.fulfill({ status: 503, json: { error: { message: "模块状态服务暂不可用" } } });
      return route.fulfill({ json: { items: modules } });
    }
    return route.continue();
  });
}

async function login(page, username = adminUsername, password = adminPassword) {
  await page.goto("/login");
  await page.getByLabel("用户名").fill(username);
  await page.getByLabel("密码", { exact: true }).fill(password);
  await page.getByRole("button", { name: "登录", exact: true }).click();
  await expect(page.getByRole("button", { name: "退出", exact: true })).toBeVisible();
}

const metadataModule = {
  manifest: { id: "catalog", name: "内容信息模块", type: "metadata" },
  status: "active", enabled: true,
};

const streamModule = (libraryIds) => ({
  manifest: { id: "video", name: "视频来源模块", type: "stream" },
  status: "active", enabled: true,
  libraryScope: { mode: "selected", libraryIds },
});

test("空安装清楚标出模块、内容和媒体库待办", async ({ page }) => {
  await mockOverview(page);
  await login(page);
  await expect(page).toHaveURL(/\/manage$/);
  await expect(page.getByRole("heading", { name: "配置检查", exact: true })).toBeVisible();
  await expect(page.getByText("没有正在运行的内容信息能力；添加内容前需要安装并启用信息模块。", { exact: true })).toBeVisible();
  await expect(page.getByText("先完成内容信息模块配置，再搜索并导入作品。", { exact: true })).toBeVisible();
  await expect(page.getByText("还没有可用媒体库。媒体库模板只整理已入库内容，不会下载或导入视频。", { exact: true })).toBeVisible();
  await expect(page.getByText("页面提供服务端配置地址和登录说明，不代表任意客户端已连接或真实媒体已解码。", { exact: true })).toBeVisible();
});

test("只有信息模块且已有内容时，仍提示创建媒体库和配置视频范围", async ({ page }) => {
  await mockOverview(page, { modules: [metadataModule], items: 3 });
  await login(page);
  await expect(page.getByText("有 1 个内容信息能力正在运行。", { exact: true })).toBeVisible();
  await expect(page.getByText("当前可见范围内有 3 部作品。入库不代表已匹配视频来源。", { exact: true })).toBeVisible();
  await expect(page.getByText("还没有可用媒体库。媒体库模板只整理已入库内容，不会下载或导入视频。", { exact: true })).toBeVisible();
  await expect(page.getByText("先创建媒体库，再设置视频模块适用范围。", { exact: true })).toBeVisible();
});

test("视频模块范围没有匹配已启用媒体库时提示修复入口", async ({ page }) => {
  await mockOverview(page, {
    modules: [metadataModule, streamModule(["old-library"])],
    libraries: [{ id: "current-library", name: "当前媒体库", itemType: "movie", filter: {}, sort: { field: "title", direction: "asc" }, isPublic: true, isAdult: false, enabled: true }],
    items: 2,
  });
  await login(page);
  await expect(page.getByText("“视频来源模块”没有匹配当前已启用媒体库的范围。", { exact: true })).toBeVisible();
  await expect(page.getByRole("link", { name: "配置适用媒体库", exact: true })).toHaveAttribute("href", "/manage/modules/video");
  await expect(page.getByText(/不等同于客户端格式兼容或实际播放验证/)).toBeVisible();
});

test("概览接口部分失败显示未知状态而不是未配置", async ({ page }) => {
  await mockOverview(page, { modules: [metadataModule], libraries: [], items: 2, moduleFailure: true });
  await login(page);
  await expect(page.getByRole("article").filter({ hasText: "安装并运行模块" }).getByRole("alert")).toHaveText("无法确认当前状态：模块状态服务暂不可用");
  await expect(page.getByText("没有正在运行的内容信息能力；添加内容前需要安装并启用信息模块。", { exact: true })).toHaveCount(0);
});

test("Editor 概览只读取其可访问的内容来源，不请求管理员模块和用户清单", async ({ page, browser }, testInfo) => {
  await login(page);
  const session = await page.evaluate(() => JSON.parse(sessionStorage.getItem("jmlab.session")));
  const username = "setup-editor";
  const password = "Setup-Editor-Only-2026!";
  const created = await page.request.post("/api/v1/admin/users", {
    headers: { Authorization: `Bearer ${session.accessToken}` },
    data: { username, password, displayName: username, role: "Editor" },
  });
  expect(created.ok(), await created.text()).toBeTruthy();
  const user = await created.json();
  const context = await browser.newContext({ baseURL: testInfo.project.use.baseURL });
  try {
    const editor = await context.newPage();
    const restrictedRequests = [];
    editor.on("request", (request) => {
      if (/\/api\/v1\/admin\/(modules|users)/.test(request.url())) restrictedRequests.push(request.url());
    });
    await login(editor, username, password);
    await expect(editor).toHaveURL(/\/manage$/);
    await expect(editor.getByRole("heading", { name: "内容工作区", exact: true })).toBeVisible();
    await expect(editor.getByRole("link", { name: "模块", exact: true })).toHaveCount(0);
    await editor.goto("/manage/libraries");
    await expect(editor.getByRole("heading", { name: "媒体库配置", exact: true })).toBeVisible();
    await expect(editor.getByRole("button", { name: "新建媒体库", exact: true })).toHaveCount(0);
    expect(restrictedRequests).toEqual([]);
  } finally {
    await context.close();
    const removed = await page.request.delete(`/api/v1/admin/users/${encodeURIComponent(user.id)}`, {
      headers: { Authorization: `Bearer ${session.accessToken}` },
    });
    expect(removed.ok()).toBeTruthy();
  }
});
