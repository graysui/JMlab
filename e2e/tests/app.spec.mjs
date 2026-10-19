import { expect, test } from "../fixtures/host.mjs";

const username = process.env.E2E_USERNAME || "e2e-admin";
const password = process.env.E2E_PASSWORD || "E2e-Playwright-Password-123";

async function login(page) {
  await page.goto("/login");
  await expect(page.getByRole("heading", { name: "登录媒体库" })).toBeVisible();
  await page.getByLabel("用户名").fill(username);
  await page.getByLabel("密码").fill(password);
  await page.getByRole("button", { name: "登录" }).click();
  await expect(page.getByRole("button", { name: "主题" })).toBeVisible();
  await expect(page.getByRole("navigation", { name: "主导航" }).getByRole("link", { name: "设置", exact: true })).toBeVisible();
  await expect(page.getByRole("link", { name: "管理后台", exact: true })).toHaveCount(0);
}

test("用户端登录后可以切换排行榜周期", async ({ page }) => {
  await login(page);
	const initial = await page.request.get("/api/v1/libraries");
	expect((await initial.json()).items).toEqual([]);
  await page.goto("/rankings?type=playback");
  await expect(page.getByRole("heading", { name: "模块排行榜" })).toBeVisible();
  await page.getByRole("combobox", { name: "内容来源", exact: true }).selectOption("test-catalog");
  await page.getByRole("combobox", { name: "榜单", exact: true }).selectOption("playback");
  await page.getByRole("combobox", { name: "范围", exact: true }).selectOption("weekly");
  await expect(page).toHaveURL(/moduleId=test-catalog&type=playback&period=weekly/);
  await page.reload(); await expect(page.getByRole("combobox", { name: "范围", exact: true })).toHaveValue("weekly");
});

test("管理员从榜单生成永久作品页", async ({ page }) => {
  await login(page);
  await page.goto("/admin/items");
  await expect(page).toHaveURL(/\/admin\/modules$/);
  await page.goto("/admin/modules/test-catalog?tab=rankings");
  await expect(page.getByRole("heading", { name: "榜单与自动更新", exact: true })).toBeVisible();


  let requestPayload;
  await page.route("**/api/v1/admin/modules/test-catalog/rankings", async (route) => {
    requestPayload = route.request().postDataJSON();
    await route.fulfill({
      status: 202,
      contentType: "application/json",
      body: JSON.stringify({ taskId: "task-e2e-ranking", status: "queued" }),
    });
  });

  await page.getByRole("combobox", { name: "榜单", exact: true }).selectOption("top250");
  await page.getByRole("combobox", { name: "范围", exact: true }).selectOption("all");
  await page.getByRole("button", { name: "立即同步所选榜单" }).click();
  await expect(page.getByRole("status")).toContainText("任务已加入队列");
  expect(requestPayload).toMatchObject({ rankingType: "top250", period: "all", revision: expect.any(String) });

});

test("个人偏好可以保存首页显示开关并用键盘关闭主题菜单", async ({ page }) => {
  await login(page);
  await page.goto("/settings/profile");
  await expect(page.getByRole("heading", { name: "主题与首页" })).toBeVisible();

  const hotFlag = page.locator("label.module-setting").filter({ has: page.getByText("模块榜单", { exact: true }) }).getByRole("checkbox");
  await expect(hotFlag).toBeChecked();
  await hotFlag.uncheck();
  await page.getByRole("button", { name: "保存偏好" }).click();
  await expect(page.getByRole("status")).toHaveText("偏好已保存");
  await page.reload();
  await expect(page.locator("label.module-setting").filter({ has: page.getByText("模块榜单", { exact: true }) }).getByRole("checkbox")).not.toBeChecked();

  const themeButton = page.getByRole("button", { name: "主题" });
  await themeButton.click();
  const menu = page.getByRole("menu", { name: "主题" });
  await expect(menu).toBeVisible();
  await page.keyboard.press("ArrowDown");
  await page.keyboard.press("Escape");
  await expect(menu).not.toBeVisible();
  await expect(themeButton).toBeFocused();
});
