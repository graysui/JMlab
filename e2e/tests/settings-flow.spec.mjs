import { expect, test } from "../fixtures/host.mjs";

const adminUsername = process.env.E2E_USERNAME || "e2e-admin";
const adminPassword = process.env.E2E_PASSWORD || "E2e-Playwright-Password-123";

async function login(page, username = adminUsername, password = adminPassword) {
  await page.goto("/login");
  await page.getByLabel("用户名").fill(username);
  await page.getByLabel("密码", { exact: true }).fill(password);
  await page.getByRole("button", { name: "登录", exact: true }).click();
  await expect(page.getByRole("button", { name: "退出", exact: true })).toBeVisible();
}

test("管理导航涵盖管理职责，旧书签保留查询、哈希和模块上下文", async ({ page }, testInfo) => {
  await login(page);
  await expect(page).toHaveURL(/\/manage$/);
  const nav = page.getByRole("navigation", { name: "管理导航" });
  await expect(nav).toBeVisible();

  const pages = [
    ["管理概览", "/manage", "从配置到观看"],
    ["模块", "/manage/modules", "模块管理"],
    ["添加内容", "/manage/discover", "添加内容"],
    ["媒体库", "/manage/libraries", "媒体库配置"],
    ["合集与清单", "/manage/collections", "合集管理"],
    ["人物与分类", "/manage/relations", "编辑、排除与合并"],
    ["作品播放源", "/manage/sources", "管理作品播放源"],
    ["任务中心", "/manage/tasks", "同步任务中心"],
    ["操作日志", "/manage/logs", "审计日志"],
    ["媒体统计", "/manage/statistics", "媒体库统计"],
    ["用户与权限", "/manage/users", "用户管理"],
    ["备份与恢复", "/manage/backup", "备份与恢复"],
  ];
  for (const [label, path, heading] of pages) {
    await nav.getByRole("link", { name: label, exact: true }).click();
    await expect(page).toHaveURL(new RegExp(`${path.replaceAll("/", "\\/")}$`));
    await expect(page.getByRole("heading", { name: heading, exact: true })).toBeVisible();
  }
  await page.screenshot({ path: testInfo.outputPath("management-desktop.png"), fullPage: true });

  const session = await page.evaluate(() => JSON.parse(sessionStorage.getItem("jmlab.session")));
  const imported = await page.request.post("/api/v1/import/apply", {
    headers: { Authorization: `Bearer ${session.accessToken}` },
    data: { mode: "merge", includeUserState: false, document: {
      formatVersion: "jmlab.export.v2", includeUserState: false,
      items: [{ id: "management-route-item", source: "test", sourceId: "movie:management-route", code: "media:management-route", title: "管理路由测试作品", itemType: "movie", isAdult: false }],
      libraries: [], libraryItems: [],
    } },
  });
  expect(imported.ok(), await imported.text()).toBeTruthy();

  await page.goto("/settings");
  await expect(page).toHaveURL(/\/settings\/profile$/);
  await expect(page.getByRole("heading", { name: "主题与首页", exact: true })).toBeVisible();
  await expect(page.getByRole("link", { name: "返回浏览", exact: true })).toHaveAttribute("href", "/browse");
  await page.goto("/settings/sources?itemId=management-route-item#sources");
  await expect(page).toHaveURL(/\/manage\/sources\?itemId=management-route-item#sources$/);
  await expect(page.getByRole("heading", { name: "管理路由测试作品", exact: true })).toBeVisible();
  await page.reload();
  await expect(page.getByRole("heading", { name: "管理路由测试作品", exact: true })).toBeVisible();
  await page.goto("/admin/data");
  await expect(page).toHaveURL(/\/manage\/backup$/);
  await page.goto("/admin/modules/test-catalog?tab=rankings");
  await expect(page).toHaveURL(/\/manage\/modules\/test-catalog\?tab=rankings$/);
  await expect(page.getByRole("heading", { name: "榜单与自动更新", exact: true })).toBeVisible();
});

test("管理抽屉在深浅主题支持手机键盘关闭并在所有目标宽度保持页面不溢出", async ({ page }) => {
  await login(page);
  for (const mode of ["light", "dark"]) {
    await page.goto("/settings/profile");
    await page.getByRole("radio", { name: mode === "light" ? /浅色/ : /深色/ }).check();
    await page.getByRole("button", { name: "保存偏好", exact: true }).click();
    await expect(page.getByRole("status")).toHaveText("偏好已保存");

    for (const width of [320, 375, 768, 1280]) {
      await page.setViewportSize({ width, height: 844 });
      await page.goto("/manage");
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBeTruthy();
      if (width > 900) continue;

      const trigger = page.getByRole("button", { name: "管理导航", exact: true });
      await trigger.click();
      const dialog = page.getByRole("dialog", { name: "管理导航" });
      await expect(dialog).toBeVisible();
      await expect(dialog.getByRole("link", { name: "管理概览", exact: true })).toBeFocused();
      await page.keyboard.press("Escape");
      await expect(dialog).toHaveCount(0);
      await expect(trigger).toBeFocused();
      await trigger.click();
      await dialog.getByRole("link", { name: "添加内容", exact: true }).click();
      await expect(page).toHaveURL(/\/manage\/discover$/);
      await expect(dialog).toHaveCount(0);
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBeTruthy();
    }
  }
});

test("Editor 和普通用户按角色访问管理区且不发起越权请求", async ({ page, browser }, testInfo) => {
  await login(page);
  const session = await page.evaluate(() => JSON.parse(sessionStorage.getItem("jmlab.session")));
  const authHeaders = { Authorization: `Bearer ${session.accessToken}` };

  for (const role of ["Editor", "Viewer"]) {
    const username = `management-${role.toLowerCase()}`;
    const password = "Management-Role-Only-2026!";
    const created = await page.request.post("/api/v1/admin/users", {
      headers: authHeaders,
      data: { username, password, displayName: username, role },
    });
    expect(created.ok(), await created.text()).toBeTruthy();
    const user = await created.json();
    const context = await browser.newContext({ baseURL: testInfo.project.use.baseURL, viewport: { width: 375, height: 812 } });
    try {
      const own = await context.newPage();
      const restrictedRequests = [];
      own.on("request", (request) => {
        if (/\/api\/v1\/(admin\/(users|modules)|backup\/)/.test(request.url())) restrictedRequests.push(request.url());
      });
      await login(own, username, password);
      await expect(own).toHaveURL(role === "Editor" ? /\/manage$/ : /\/browse$/);

      if (role === "Editor") {
        const nav = own.getByRole("navigation", { name: "管理导航" });
        await expect(nav.getByRole("link", { name: "模块", exact: true })).toHaveCount(0);
        await own.goto("/manage/libraries");
        await expect(own.getByRole("heading", { name: "媒体库配置", exact: true })).toBeVisible();
        await expect(own.getByRole("button", { name: "新建媒体库", exact: true })).toHaveCount(0);
        await own.goto("/manage/discover");
        await expect(own.getByRole("heading", { name: "添加内容", exact: true })).toBeVisible();
      } else {
        await own.goto("/settings/profile");
        await expect(own.getByRole("heading", { name: "主题与首页", exact: true })).toBeVisible();
        await expect(own.getByRole("link", { name: "返回浏览", exact: true })).toHaveAttribute("href", "/browse");
        for (const path of ["/manage", "/manage/discover", "/manage/modules", "/manage/users", "/manage/backup"]) {
          await own.goto(path);
          await expect(own.getByRole("heading", { name: "没有管理权限", exact: true })).toBeVisible();
        }
      }
      expect(restrictedRequests).toEqual([]);

      const ownSession = await own.evaluate(() => JSON.parse(sessionStorage.getItem("jmlab.session")));
      for (const path of ["/api/v1/admin/users", "/api/v1/admin/modules"]) {
        const denied = await own.request.get(path, { headers: { Authorization: `Bearer ${ownSession.accessToken}` } });
        expect(denied.status()).toBe(403);
      }
    } finally {
      await context.close();
      const removed = await page.request.delete(`/api/v1/admin/users/${encodeURIComponent(user.id)}`, { headers: authHeaders });
      expect(removed.ok()).toBeTruthy();
    }
  }
});
