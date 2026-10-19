import { expect, test } from "../fixtures/host.mjs";

async function login(page, username, password) {
  await page.goto("/login");
  await page.getByLabel("用户名").fill(username);
  await page.getByLabel("密码", { exact: true }).fill(password);
  await page.getByRole("button", { name: "登录", exact: true }).click();
  await expect(page.getByRole("button", { name: "退出", exact: true })).toBeVisible();
}

test("设置统一承接通用管理，旧链接和不同角色仍可正确使用", async ({ page, browser }, testInfo) => {
  test.setTimeout(90000);
  const pageErrors = [];
  page.on("pageerror", (error) => pageErrors.push(error.message));
  await login(page, process.env.E2E_USERNAME || "e2e-admin", process.env.E2E_PASSWORD || "E2e-Playwright-Password-123");
  const session = await page.evaluate(() => JSON.parse(sessionStorage.getItem("jmlab.session")));
  const authHeaders = { Authorization: `Bearer ${session.accessToken}` };
  const settingsNav = page.getByRole("navigation", { name: "主导航" }).getByRole("link", { name: "设置", exact: true });
  await settingsNav.click();
  await expect(page).toHaveURL(/\/settings$/);
  await expect(page.getByRole("heading", { name: "设置", exact: true })).toBeVisible();
  await expect(page.getByRole("link", { name: "管理后台", exact: true })).toHaveCount(0);
  await expect(page.getByRole("heading", { name: "模块设置", exact: true })).toBeVisible();
  await page.screenshot({ path: testInfo.outputPath("settings-desktop.png"), fullPage: true });

  const pages = [
    ["profile", "主题与首页"], ["discover", "搜索并添加"], ["libraries", "媒体库配置"],
    ["collections", "合集管理"], ["relations", "编辑、排除与合并"], ["sources", "管理作品播放源"],
    ["dashboard", "运行概况"], ["statistics", "媒体库统计"], ["tasks", "同步任务中心"],
    ["logs", "审计日志"], ["backup", "备份与恢复"], ["users", "用户管理"],
  ];
  for (const [id, title] of pages) {
    await page.locator(`a[href="/settings/${id}"]`).click();
    await expect(page.getByRole("heading", { name: title, exact: true })).toBeVisible();
    await expect(settingsNav).toHaveAttribute("aria-current", "page");
    await page.getByRole("link", { name: "返回设置", exact: true }).click();
    await expect(page).toHaveURL(/\/settings$/);
  }
  await page.getByRole("link", { name: /管理模块/ }).click();
  await expect(page).toHaveURL(/\/admin\/modules$/);
  await expect(page.getByRole("heading", { name: "模块管理", exact: true })).toBeVisible();

  // Bookmarks keep selected media, query parameters and anchors after migration.
  const imported = await page.request.post("/api/v1/import/apply", { headers: authHeaders, data: { mode: "merge", includeUserState: false, document: {
    formatVersion: "jmlab.export.v2", includeUserState: false,
    items: [{ id: "settings-media", source: "tmdb", sourceId: "movie:settings", code: "media:settings", title: "设置页测试作品", itemType: "movie", isAdult: false }],
    libraries: [{ id: "settings-library", name: "设置页测试媒体库", itemType: "movie", filter: {}, sort: { field: "title", direction: "asc" }, isPublic: true, enabled: true }],
    libraryItems: [{ libraryId: "settings-library", itemId: "settings-media", position: 0 }],
  } } });
  expect(imported.ok(), await imported.text()).toBeTruthy();
  await page.goto("/admin/sources?itemId=settings-media#sources");
  await expect(page).toHaveURL(/\/settings\/sources\?itemId=settings-media#sources$/);
  await page.reload();
  await expect(page.getByRole("heading", { name: "管理作品播放源", exact: true })).toBeVisible();
  await expect(page.getByRole("heading", { name: "设置页测试作品", exact: true })).toBeVisible();
  await page.goto("/admin/data");
  await expect(page).toHaveURL(/\/settings\/backup$/);
  for (const [id] of pages) {
    await page.goto(`/admin/${id}/`);
    await expect(page).toHaveURL(new RegExp(`/settings/${id}$`));
  }
  await page.goto("/admin");
  await expect(page).toHaveURL(/\/settings$/);
  for (const width of [390, 320]) {
    await page.setViewportSize({ width, height: 844 });
    await expect(page.getByRole("link", { name: "打开设置", exact: true })).toBeVisible();
    await expect(page.getByRole("link", { name: "打开模块管理", exact: true })).toBeVisible();
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBeTruthy();
    await page.screenshot({ path: testInfo.outputPath(`settings-${width}.png`), fullPage: true });
  }

  for (const role of ["Editor", "Viewer"]) {
    const username = `settings-${role.toLowerCase()}`;
    const password = "Settings-Local-Only-2026!";
    const created = await page.request.post("/api/v1/admin/users", { headers: authHeaders, data: { username, password, displayName: username, role } });
    expect(created.ok()).toBeTruthy();
    const user = await created.json();
    const context = await browser.newContext({ baseURL: testInfo.project.use.baseURL, viewport: { width: 320, height: 844 } });
    try {
      const userPage = await context.newPage();
      await login(userPage, username, password);
      await userPage.getByRole("link", { name: "打开设置", exact: true }).click();
      await expect(userPage.getByRole("heading", { name: "设置", exact: true })).toBeVisible();
      await expect(userPage.getByRole("link", { name: /主题与首页/ })).toBeVisible();
      await expect(userPage.getByRole("heading", { name: "模块设置", exact: true })).toHaveCount(0);
      await expect(userPage.getByRole("link", { name: /用户与权限|备份与恢复/ })).toHaveCount(0);
      await expect(userPage.getByRole("link", { name: "打开模块管理", exact: true })).toHaveCount(0);
      if (role === "Editor") {
        await userPage.getByRole("link", { name: /添加内容/ }).click();
        await expect(userPage.getByRole("heading", { name: "搜索并添加", exact: true })).toBeVisible();
        await userPage.getByRole("link", { name: "返回设置", exact: true }).click();
        await expect(userPage.getByRole("link", { name: /任务中心/ })).toBeVisible();
      } else {
        await expect(userPage.getByRole("heading", { name: "内容与媒体库", exact: true })).toHaveCount(0);
        await expect(userPage.getByRole("heading", { name: "系统与维护", exact: true })).toHaveCount(0);
      }
      let restrictedRequests = 0;
      userPage.on("request", (request) => { if (/\/api\/v1\/(admin\/(users|modules)|backup\/)/.test(request.url())) restrictedRequests++; });
      for (const path of ["/settings/users", "/settings/backup", "/admin/modules/test-catalog", ...(role === "Viewer" ? ["/settings/discover"] : [])]) {
        await userPage.goto(path);
        await expect(userPage.getByRole("heading", { name: "没有管理权限", exact: true })).toBeVisible();
      }
      expect(restrictedRequests).toBe(0);
      const ownSession = await userPage.evaluate(() => JSON.parse(sessionStorage.getItem("jmlab.session")));
      for (const path of ["/api/v1/admin/users", "/api/v1/admin/modules"]) {
        const denied = await userPage.request.get(path, { headers: { Authorization: `Bearer ${ownSession.accessToken}` } });
        expect(denied.status()).toBe(403);
      }
    } finally {
      await context.close();
      const removed = await page.request.delete(`/api/v1/admin/users/${encodeURIComponent(user.id)}`, { headers: authHeaders });
      expect(removed.ok()).toBeTruthy();
    }
  }
  expect(pageErrors).toEqual([]);
});
