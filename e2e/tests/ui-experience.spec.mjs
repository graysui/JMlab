import { expect, test } from "../fixtures/host.mjs";

async function login(page) {
  await page.goto("/login");
  await page.getByLabel("用户名").fill(process.env.E2E_USERNAME || "e2e-admin");
  await page.getByLabel("密码", { exact: true }).fill(process.env.E2E_PASSWORD || "E2e-Playwright-Password-123");
  await page.getByRole("button", { name: "登录", exact: true }).click();
  await expect(page.getByRole("button", { name: "退出", exact: true })).toBeVisible();
}

async function noOverflow(page) {
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBeTruthy();
}

test("设置查找、模块状态筛选和逐步展开的搜索在桌面与手机可用", async ({ page }, testInfo) => {
  test.setTimeout(90000);
  const errors = [];
  page.on("pageerror", (error) => errors.push(error.message));
  await login(page);
  await page.goto("/manage");
  await page.getByLabel("查找管理入口", { exact: true }).fill("备份");
  await expect(page.getByRole("navigation", { name: "管理导航" }).getByRole("link")).toHaveCount(1);
  await expect(page.getByRole("navigation", { name: "管理导航" })).toContainText("备份与恢复");
  await page.getByLabel("查找管理入口", { exact: true }).fill("没有这个入口");
  await expect(page.getByRole("status")).toHaveText("没有匹配的管理入口。");
  await page.getByRole("button", { name: "清除搜索", exact: true }).click();
  await expect(page.getByRole("navigation", { name: "管理导航" }).getByRole("link")).toHaveCount(12);
  await page.screenshot({ path: testInfo.outputPath("management-desktop.png"), fullPage: true });

  await page.goto("/admin/modules");
  await expect(page.locator(".module-card").first()).toBeVisible();
  const allCount = await page.locator(".module-card").count();
  const target = page.locator(".module-card").filter({ has: page.getByRole("heading", { name: "测试目录模块", exact: true }) });
  const targetName = await target.getByRole("heading").innerText();
  await target.getByRole("button", { name: "停用", exact: true }).click();
  await expect(page.locator(".module-card").filter({ has: page.getByRole("heading", { name: targetName, exact: true }) }).getByText("已停用", { exact: true })).toBeVisible();
  await page.getByRole("combobox", { name: "模块状态", exact: true }).selectOption("disabled");
  expect(await page.locator(".module-card").count()).toBeGreaterThan(0);
  for (const card of await page.locator(".module-card").all()) await expect(card.getByText("已停用", { exact: true })).toBeVisible();
  await page.getByLabel("查找模块", { exact: true }).fill("没有这个模块");
  await expect(page.getByRole("heading", { name: "没有找到模块" })).toBeVisible();
  await page.getByRole("button", { name: "清空筛选" }).click();
  await expect(page.locator(".module-card")).toHaveCount(allCount);
  await page.locator(".module-card").filter({ has: page.getByRole("heading", { name: targetName, exact: true }) }).getByRole("button", { name: "启用", exact: true }).click();
  await expect(page.locator(".module-card").filter({ has: page.getByRole("heading", { name: targetName, exact: true }) }).getByRole("button", { name: "停用", exact: true })).toBeEnabled();
  await page.screenshot({ path: testInfo.outputPath("modules-desktop.png"), fullPage: true });

  await page.goto("/search");
  await expect(page.getByRole("combobox", { name: "收藏状态", exact: true })).toBeVisible();
  await expect(page.locator(".advanced-search select[multiple]").first()).not.toBeVisible();
  await page.locator(".advanced-search summary").click();
  await expect(page.locator(".advanced-search select[multiple]").first()).toBeVisible();
  await page.locator(".advanced-search summary").click();
  await page.getByLabel("最低评分", { exact: true }).fill("8.5");
  const request = page.waitForRequest((request) => request.url().includes("/api/v1/items?") && request.url().includes("minRating=8.5"));
  await page.getByRole("button", { name: "应用筛选" }).click();
  await request;
  await expect(page).toHaveURL(/minRating=8.5/);
  await expect(page.locator(".search-result-summary")).toBeVisible();
  await page.reload();
  await expect(page.getByLabel("最低评分", { exact: true })).toHaveValue("8.5");
  await page.getByRole("button", { name: "重置条件" }).click();
  await page.screenshot({ path: testInfo.outputPath("search-desktop.png"), fullPage: true });

  await page.goto("/rankings?type=top250");
  await page.getByRole("combobox", { name: "范围", exact: true }).selectOption("2025");
  await expect(page).toHaveURL(/type=top250&period=2025/);
  await page.reload();
  await expect(page.getByRole("combobox", { name: "范围", exact: true })).toHaveValue("2025");

  for (const viewport of [{ width: 320, height: 844 }, { width: 375, height: 812 }, { width: 768, height: 1024 }, { width: 1280, height: 900 }]) {
    await page.setViewportSize(viewport);
    await page.goto("/manage");
    await noOverflow(page);
    if (viewport.width <= 900) {
      const menuButton = page.getByRole("button", { name: "管理导航", exact: true });
      await expect(menuButton).toBeVisible();
      await menuButton.click();
      const drawer = page.getByRole("dialog", { name: "管理导航" });
      await expect(drawer).toBeVisible();
      await expect(drawer.getByRole("link", { name: "模块", exact: true })).toBeVisible();
      await page.keyboard.press("Escape");
      await expect(drawer).toHaveCount(0);
      await expect(menuButton).toBeFocused();
      await menuButton.click();
      await page.getByRole("dialog", { name: "管理导航" }).getByRole("link", { name: "模块", exact: true }).click();
      await expect(page).toHaveURL(/\/manage\/modules$/);
      await expect(page.locator(".module-card").first()).toBeVisible();
      await expect(menuButton).toBeFocused();
      await noOverflow(page);
      if (viewport.width === 375) await page.screenshot({ path: testInfo.outputPath("modules-mobile.png"), fullPage: true });
    } else {
      const nav = page.getByRole("navigation", { name: "管理导航" });
      const links = nav.getByRole("link");
      await expect(links).toHaveCount(12);
      for (const link of await links.all()) { const box = await link.boundingBox(); expect(box.height).toBeGreaterThanOrEqual(44); }
      await nav.getByRole("link", { name: "模块", exact: true }).click();
      await expect(page.locator(".module-card").first()).toBeVisible();
      await noOverflow(page);
    }
    await page.goto("/search");
    const nav = page.getByRole("navigation", { name: "浏览导航" });
    const links = nav.getByRole("link");
    await expect(links).toHaveCount(5);
    for (const link of await links.all()) { const box = await link.boundingBox(); expect(box.height).toBeGreaterThanOrEqual(44); }
    await expect(page.getByLabel("搜索关键词", { exact: true })).toBeVisible();
    await noOverflow(page);
    if (viewport.width === 375) await page.screenshot({ path: testInfo.outputPath("search-mobile.png"), fullPage: true });
  }
  expect(errors).toEqual([]);
});

test("海报比例真实生效，深浅主题及大字号低动效布局正常", async ({ page }, testInfo) => {
  test.setTimeout(90000);
  await login(page);
  const session = await page.evaluate(() => JSON.parse(sessionStorage.getItem("jmlab.session")));
  const response = await page.request.post("/api/v1/import/apply", { headers: { Authorization: `Bearer ${session.accessToken}` }, data: { mode: "merge", includeUserState: false, document: {
    formatVersion: "jmlab.export.v2", includeUserState: false,
    items: [{ id: "ui-ratio-item", source: "tmdb", sourceId: "movie:ui-ratio", code: "media:ui-ratio", title: "界面验收测试作品", originalTitle: "Interface preview", itemType: "movie", year: 2026, isAdult: false }],
    libraries: [{ id: "ui-ratio-library", name: "界面验收媒体库", itemType: "movie", filter: {}, sort: { field: "title", direction: "asc" }, isPublic: true, enabled: true }],
    libraryItems: [{ libraryId: "ui-ratio-library", itemId: "ui-ratio-item", position: 0 }],
  } } });
  expect(response.ok(), await response.text()).toBeTruthy();
  for (const [value, ratio] of [["square", 1], ["wide", 16 / 9], ["portrait", 2 / 3]]) {
    await page.goto("/settings/profile");
    await expect(page.getByRole("combobox", { name: "海报比例", exact: true })).toBeVisible();
    await page.getByRole("combobox", { name: "海报比例", exact: true }).selectOption(value);
    await page.getByRole("button", { name: "保存偏好", exact: true }).click();
    await expect(page.getByRole("status")).toHaveText("偏好已保存");
    await page.goto("/libraries?library=ui-ratio-library");
    const poster = page.locator('.item-card[href="/items/ui-ratio-item"] .item-poster-wrap');
    await expect(poster).toBeVisible();
    const box = await poster.boundingBox();
    expect(box.width / box.height).toBeCloseTo(ratio, 1);
  }
  await page.emulateMedia({ reducedMotion: "reduce" });
  for (const mode of ["light", "dark"]) {
    await page.goto("/settings/profile");
    await page.getByRole("radio", { name: mode === "light" ? /浅色/ : /深色/ }).check();
    await page.getByRole("button", { name: "保存偏好", exact: true }).click();
    await expect(page.getByRole("status")).toHaveText("偏好已保存");
    await page.goto("/settings");
    await page.setViewportSize({ width: 375, height: 812 });
    await page.evaluate(() => document.documentElement.style.fontSize = "20px");
    await noOverflow(page);
    await page.screenshot({ path: testInfo.outputPath(`settings-${mode}-large-text.png`), fullPage: true });
    await page.evaluate(() => document.documentElement.style.fontSize = "");
  }
});

test("深浅主题及强调色满足正文、按钮和焦点对比度目标", async ({ page }) => {
  await login(page);
  await page.goto("/manage");
  for (const mode of ["dark", "light"]) {
    for (const accent of ["blue", "mint", "amber"]) {
      const ratios = await page.evaluate(({ mode, accent }) => {
        document.documentElement.dataset.theme = mode;
        document.documentElement.dataset.accent = accent;
        const parseColor = (value) => {
          const text = value.trim();
          const hex = text.match(/^#([\da-f]{3,4}|[\da-f]{6}|[\da-f]{8})$/i);
          if (hex) {
            const digits = hex[1].length <= 4 ? [...hex[1]].map((digit) => digit + digit).join("") : hex[1];
            const channels = [0, 2, 4].map((start) => Number.parseInt(digits.slice(start, start + 2), 16));
            return [...channels, digits.length === 8 ? Number.parseInt(digits.slice(6, 8), 16) / 255 : 1];
          }
          const parts = text.match(/[\d.]+/g)?.map(Number);
          if (!parts || parts.length < 3) throw new Error(`无法解析 CSS 颜色：${text}`);
          return [parts[0], parts[1], parts[2], parts[3] ?? 1];
        };
        const root = getComputedStyle(document.documentElement);
        const base = parseColor(root.getPropertyValue("--bg"));
        const panelToken = parseColor(root.getPropertyValue("--panel"));
        const panel = panelToken.slice(0, 3).map((channel, index) => channel * panelToken[3] + base[index] * (1 - panelToken[3]));
        const luminance = (color) => color.slice(0, 3).map((channel) => {
          const value = channel / 255;
          return value <= 0.04045 ? value / 12.92 : ((value + 0.055) / 1.055) ** 2.4;
        }).reduce((sum, value, index) => sum + value * [0.2126, 0.7152, 0.0722][index], 0);
        const ratio = (first, second) => {
          const a = luminance(first);
          const b = luminance(second);
          return (Math.max(a, b) + 0.05) / (Math.min(a, b) + 0.05);
        };
        return {
          bodyText: ratio(parseColor(root.getPropertyValue("--text")), panel),
          mutedText: ratio(parseColor(root.getPropertyValue("--muted")), panel),
          buttonText: ratio(parseColor(root.getPropertyValue("--on-accent")), parseColor(root.getPropertyValue("--accent-strong"))),
          focusRing: ratio(parseColor(root.getPropertyValue("--accent")), panel),
        };
      }, { mode, accent });
      expect(ratios.bodyText, `${mode}/${accent} body text`).toBeGreaterThanOrEqual(4.5);
      expect(ratios.mutedText, `${mode}/${accent} muted text`).toBeGreaterThanOrEqual(4.5);
      expect(ratios.buttonText, `${mode}/${accent} button text`).toBeGreaterThanOrEqual(4.5);
      expect(ratios.focusRing, `${mode}/${accent} focus ring`).toBeGreaterThanOrEqual(3);
    }
  }
});
