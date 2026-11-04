import { expect, test } from '../fixtures/host.mjs';

async function login(page, username = process.env.E2E_USERNAME || 'e2e-admin', password = process.env.E2E_PASSWORD || 'E2e-Playwright-Password-123') {
  await page.goto('/login');
  await page.getByLabel('用户名').fill(username);
  await page.getByLabel('密码', { exact: true }).fill(password);
  await page.getByRole('button', { name: '登录', exact: true }).click();
  await expect(page.getByRole('button', { name: '退出', exact: true })).toBeVisible();
}

test('管理员默认进入独立管理区域，旧书签保留查询和锚点', async ({ page }) => {
  await login(page);
  await expect(page).toHaveURL(/\/manage$/);
  await expect(page.getByRole('navigation', { name: '管理导航' })).toBeVisible();
  await page.goto('/settings/sources?itemId=missing#sources');
  await expect(page).toHaveURL(/\/manage\/sources\?itemId=missing#sources$/);
  await page.goto('/admin/modules/test-catalog?tab=rankings');
  await expect(page).toHaveURL(/\/manage\/modules\/test-catalog\?tab=rankings$/);
  await expect(page.getByRole('heading', { name: '榜单与自动更新', exact: true })).toBeVisible();
  await page.goto('/route-that-does-not-exist');
  await expect(page.getByRole('heading', { name: '页面不存在', exact: true })).toBeVisible();
  await page.goto('/clients');
  const serverAddress = page.getByLabel('Jellyfin 服务器地址', { exact: true });
  await expect(serverAddress).toHaveValue(process.env.BASE_URL);
  await page.context().grantPermissions(['clipboard-read', 'clipboard-write']);
  await page.getByRole('button', { name: '复制地址', exact: true }).click();
  await expect(page.getByRole('status')).toHaveText('服务器地址已复制。');
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(process.env.BASE_URL);
  await page.evaluate(() => Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText: async () => { throw new Error('clipboard denied'); } } }));
  await page.getByRole('button', { name: '复制地址', exact: true }).click();
  await expect(page.getByRole('alert')).toHaveText('clipboard denied');
  await page.route('**/api/v1/me/connection', route => route.fulfill({ json: { serverUrl: 'https://media.example.test/jmlab/' } }));
  await page.reload();
  await expect(serverAddress).toHaveValue('https://media.example.test/jmlab/');
});

test('登录保留站内目标并拒绝站外返回地址', async ({ page }) => {
  await page.goto('/manage/modules/test-catalog?tab=rankings');
  await expect(page).toHaveURL(/\/login$/);
  await page.getByLabel('用户名').fill(process.env.E2E_USERNAME || 'e2e-admin');
  await page.getByLabel('密码', { exact: true }).fill(process.env.E2E_PASSWORD || 'E2e-Playwright-Password-123');
  await page.getByRole('button', { name: '登录', exact: true }).click();
  await expect(page).toHaveURL(/\/manage\/modules\/test-catalog\?tab=rankings$/);
  await page.getByRole('button', { name: '退出', exact: true }).click();
  await expect(page.getByRole('link', { name: '登录', exact: true })).toBeVisible();
  await page.goto('/login?returnTo=https%3A%2F%2Fexample.com');
  await page.getByLabel('用户名').fill(process.env.E2E_USERNAME || 'e2e-admin');
  await page.getByLabel('密码', { exact: true }).fill(process.env.E2E_PASSWORD || 'E2e-Playwright-Password-123');
  await page.getByRole('button', { name: '登录', exact: true }).click();
  await expect(page).toHaveURL(/\/manage$/);
});

for (const role of ['Editor', 'Viewer']) {
  test(`${role} 默认入口和管理访问保持角色边界`, async ({ page, browser }, testInfo) => {
    await login(page);
    const session = await page.evaluate(() => JSON.parse(sessionStorage.getItem('jmlab.session')));
    const username = `manage-${role.toLowerCase()}`;
    const password = 'Manage-Only-Local-2026!';
    const result = await page.request.post('/api/v1/admin/users', { headers: { Authorization: `Bearer ${session.accessToken}` }, data: { username, password, displayName: username, role } });
    expect(result.ok()).toBeTruthy();
    const context = await browser.newContext({ baseURL: testInfo.project.use.baseURL });
    try {
      const own = await context.newPage();
      await login(own, username, password);
      await expect(own).toHaveURL(role === 'Editor' ? /\/manage$/ : /\/browse$/);
      let restricted = 0;
      own.on('request', request => { if (/\/api\/v1\/(admin\/(modules|users)|backup\/)/.test(request.url())) restricted++; });
      await own.goto('/manage/modules');
      await expect(own.getByRole('heading', { name: '没有管理权限', exact: true })).toBeVisible();
      await own.goto('/settings/backup');
      await expect(own.getByRole('heading', { name: '没有管理权限', exact: true })).toBeVisible();
      expect(restricted).toBe(0);
      if (role === 'Editor') {
        await own.goto('/manage/libraries');
        await expect(own.getByRole('heading', { name: '媒体库配置', exact: true })).toBeVisible();
        await expect(own.getByRole('button', { name: '新建媒体库', exact: true })).toHaveCount(0);
        expect(restricted).toBe(0);
      }
      await own.goto('/settings/profile');
      await expect(own.getByRole('heading', { name: '主题与首页', exact: true })).toBeVisible();
      await expect(own.getByRole('link', { name: '返回浏览', exact: true })).toHaveAttribute('href', '/browse');
      await own.goto('/clients');
      await expect(own.getByLabel('Jellyfin 服务器地址', { exact: true })).toHaveValue(process.env.BASE_URL);
    } finally { await context.close(); }
  });
}
