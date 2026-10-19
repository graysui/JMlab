import { test, expect } from "@playwright/test";
import { execFile } from "node:child_process";
import { promisify } from "node:util";
import { mkdtemp, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";

const exec = promisify(execFile);
let firstPackage, updatePackage;
test.describe.configure({ timeout: 90000 });
test.beforeAll(async () => {
  test.skip(!!process.env.E2E_DOCKER_CONTAINER, "本测试生成原生平台程序，容器需要单独生成目标平台模块包");
  const directory = await mkdtemp(join(tmpdir(), "jmlab-package-browser-"));
  const executable = join(directory, process.platform === "win32" ? "provider.exe" : "provider");
  await exec("go", ["build", "-o", executable, "./fixtures/package-provider"], { cwd: resolve(".") });
  const manifest = { id: "browser-package", name: "ZIP 测试模块", version: "1.0.0", type: "metadata", supportedItemTypes: ["movie"], transport: { type: "http" }, runtime: { os: process.platform === "win32" ? "windows" : process.platform, arch: process.arch === "x64" ? "amd64" : process.arch, executable: process.platform === "win32" ? "bin/provider.exe" : "bin/provider" }, configSchema: { apiKey: { type: "password", secret: true, required: true, label: "模块访问凭证" } } };
  for (const [version, filename] of [["1.0.0", "first.zip"], ["2.0.0", "update.zip"]]) {
    const source = join(directory, "manifest.json");
    await writeFile(source, JSON.stringify({ ...manifest, version }));
    await exec("python", ["-c", "import sys,zipfile; z=zipfile.ZipFile(sys.argv[3],'w',zipfile.ZIP_DEFLATED); z.write(sys.argv[1],'manifest.json'); z.write(sys.argv[2],sys.argv[4]); z.close()", source, executable, join(directory, filename), manifest.runtime.executable]);
  }
  firstPackage = join(directory, "first.zip"); updatePackage = join(directory, "update.zip");
});

test("本地 ZIP 安装、凭证保留、启停和更新，网址错误有明确反馈", async ({ page }) => {
  await page.goto("/login");
  await page.getByLabel("用户名").fill(process.env.E2E_USERNAME || "e2e-admin");
  await page.getByLabel("密码", { exact: true }).fill(process.env.E2E_PASSWORD || "E2e-Playwright-Password-123");
  await page.getByRole("button", { name: "登录", exact: true }).click();
  await expect(page.getByRole("button", { name: "退出", exact: true })).toBeVisible();
  await page.goto("/admin/modules?view=available");
  await page.getByRole("button", { name: "网址导入", exact: true }).click();
  await page.getByLabel("模块包下载网址").fill("http://127.0.0.1:1/module.zip");
  await page.getByRole("button", { name: "读取模块包", exact: true }).click();
  await expect(page.getByRole("alert")).toContainText("下载失败");
  await page.getByRole("button", { name: "本地 ZIP 文件", exact: true }).click();
  await page.getByLabel("模块 ZIP 文件").setInputFiles(firstPackage);
  await page.getByRole("button", { name: "读取模块包", exact: true }).click();
  await expect(page.getByRole("heading", { name: "安装并设置：ZIP 测试模块" })).toBeVisible();
  await expect(page.getByLabel("模块服务地址")).toHaveCount(0);
  await page.getByLabel("模块访问凭证", { exact: false }).fill("browser-package-secret");
  await page.getByRole("button", { name: "安装模块", exact: true }).click();
  await expect(page).toHaveURL(/\/admin\/modules\/browser-package$/);
  await expect(page.getByLabel("模块访问凭证", { exact: false })).toHaveValue("");
  await page.getByRole("button", { name: "保存设置", exact: true }).click();
  await expect(page.getByRole("status")).toContainText("模块设置已保存");
  await page.getByRole("button", { name: "停用模块", exact: true }).click();
  await expect(page.getByRole("button", { name: "启用模块", exact: true })).toBeVisible();
  await page.getByRole("button", { name: "启用模块", exact: true }).click();
  await expect(page.getByRole("button", { name: "停用模块", exact: true })).toBeVisible();
  await page.getByRole("link", { name: "状态与维护", exact: true }).click();
  await page.getByText("更新模块包", { exact: true }).click();
  await page.getByLabel("模块 ZIP 文件").setInputFiles(updatePackage);
  await page.getByRole("button", { name: "读取更新包", exact: true }).click();
  await expect(page.getByText("将当前模块更新至 2.0.0，保留已有设置。", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "更新模块", exact: true }).click();
  await expect(page).toHaveURL(/\/admin\/modules\/browser-package$/);
  await expect(page.getByRole("heading", { name: "ZIP 测试模块", exact: true })).toBeVisible();
  await page.getByRole("link", { name: "状态与维护", exact: true }).click();
  await expect(page.locator("dd").filter({ hasText: "2.0.0" })).toBeVisible();
  await page.getByRole("button", { name: "卸载模块", exact: true }).click();
  await page.getByRole("button", { name: "确认卸载", exact: true }).click();
  await expect(page).toHaveURL(/\/admin\/modules$/);
  await expect(page.getByRole("heading", { name: "ZIP 测试模块", exact: true })).toHaveCount(0);
});
