import { expect, test } from "../fixtures/host.mjs";
import { createHash } from "node:crypto";
import { gzipSync } from "node:zlib";

// Small, deterministic protocol fixture; no existing data or backup directory.
function backupArchive() {
  const document = Buffer.from(JSON.stringify({
    formatVersion: "jmlab.export.v2", includeUserState: false,
    items: [{ id: "browser-restore", source: "tmdb", sourceId: "movie:9001", code: "media:browser-restore", title: "归档电影", itemType: "movie", isAdult: false }],
  }));
  const manifest = Buffer.from(JSON.stringify({
    formatVersion: "jmlab.backup.v1", createdAt: "2026-10-18T00:00:00Z", applicationVersion: "local-test", includeUserState: false, includeEncryptedConfig: false,
    files: [{ name: "database.json", size: document.length, sha256: createHash("sha256").update(document).digest("hex") }],
  }));
  const blocks = [];
  for (const [name, data] of [["manifest.json", manifest], ["database.json", document]]) {
    const header = Buffer.alloc(512);
    const octal = (offset, width, number) => header.write(number.toString(8).padStart(width - 1, "0") + "\0", offset, width, "ascii");
    header.write(name); octal(100, 8, 0o600); octal(108, 8, 0); octal(116, 8, 0); octal(124, 12, data.length); octal(136, 12, 0);
    header.fill(0x20, 148, 156); header[156] = 0x30; header.write("ustar\0", 257); header.write("00", 263);
    const checksum = header.reduce((sum, value) => sum + value, 0);
    header.write(checksum.toString(8).padStart(6, "0") + "\0 ", 148, 8, "ascii");
    blocks.push(header, data, Buffer.alloc((512 - data.length % 512) % 512));
  }
  return gzipSync(Buffer.concat([...blocks, Buffer.alloc(1024)]));
}

test("管理员可以预览校验并恢复归档，变更选项后需要重新预览", async ({ page }, testInfo) => {
  await page.goto("/login");
  await page.getByLabel("用户名").fill(process.env.E2E_USERNAME || "e2e-admin");
  await page.getByLabel("密码").fill(process.env.E2E_PASSWORD || "E2e-Playwright-Password-123");
  await page.getByRole("button", { name: "登录", exact: true }).click();
  await page.getByRole("navigation", { name: "主导航" }).getByRole("link", { name: "设置", exact: true }).click();
  await page.getByRole("link", { name: /备份与恢复/ }).click();
  await expect(page.getByRole("heading", { name: "备份与恢复", exact: true })).toBeVisible();
  await expect(page.getByLabel("导入 JSON", { exact: true })).not.toBeVisible();
  await expect(page.getByLabel("成功备份任务 ID", { exact: true })).not.toBeVisible();
  const confirm = page.getByRole("button", { name: "确认恢复备份", exact: true });
  const preview = page.getByRole("button", { name: "校验并预览归档", exact: true });
  const file = page.getByLabel("选择 JMlab tar.gz 归档", { exact: true });
  await expect(confirm).toBeDisabled();
  await file.setInputFiles({ name: "invalid.tar.gz", mimeType: "application/gzip", buffer: Buffer.from("invalid") });
  await preview.click();
  await expect(page.getByRole("alert")).toContainText("不是有效的 gzip");
  await expect(confirm).toBeDisabled();
  await file.setInputFiles({ name: "fixture.tar.gz", mimeType: "application/gzip", buffer: backupArchive() });
  await preview.click();
  await expect(page.getByRole("status")).toContainText("备份归档校验通过");
  await expect(confirm).toBeEnabled();
  const configuration = page.getByRole("checkbox", { name: "同时恢复来源、模块存储和备份目标设置", exact: true });
  await configuration.check(); await expect(confirm).toBeDisabled(); await configuration.uncheck();
  await preview.click(); await expect(confirm).toBeEnabled();
  await page.getByRole("combobox", { name: /冲突处理/ }).selectOption("overwrite");
  await expect(confirm).toBeDisabled(); await preview.click(); await expect(confirm).toBeEnabled();
  page.once("dialog", (dialog) => dialog.accept("WRONG"));
  await confirm.click(); await expect(page.getByRole("alert")).toContainText("已取消恢复");
  page.once("dialog", (dialog) => dialog.accept("RESTORE_BACKUP"));
  await confirm.click(); await expect(page.getByRole("status")).toContainText("备份恢复完成");
  await expect(confirm).toBeDisabled();
  const token = await page.evaluate(() => JSON.parse(sessionStorage.getItem("jmlab.session")).accessToken);
  const item = await page.request.get("/api/v1/items/browser-restore", { headers: { Authorization: `Bearer ${token}` } });
  expect(item.ok()).toBeTruthy(); expect((await item.json()).title).toBe("归档电影");
  await page.getByText("高级：JSON 导入与导出", { exact: true }).click();
  const downloadEvent = page.waitForEvent("download");
  await page.getByRole("button", { name: "下载 JSON 导出", exact: true }).click();
  const download = await downloadEvent;
  expect(download.suggestedFilename()).toBe("jmlab-export.json");
  const stream = await download.createReadStream(); const chunks = [];
  for await (const chunk of stream) chunks.push(chunk);
  const exported = JSON.parse(Buffer.concat(chunks).toString());
  expect(exported.items.some((entry) => entry.id === "browser-restore")).toBeTruthy();
  await page.reload(); await expect(confirm).toBeDisabled();
  for (const width of [320, 390]) {
    await page.setViewportSize({ width, height: 844 });
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBeTruthy();
    const overflowing = await page.locator(".backup-admin-page").evaluate((root) =>
      [...root.querySelectorAll("section, fieldset, label, p, input, select")]
        .filter((element) => element.getClientRects().length > 0 && element.scrollWidth > element.clientWidth + 1)
        .map((element) => ({ tag: element.tagName, className: element.className, text: element.textContent.slice(0, 40) })));
    expect(overflowing, `${width}px 下备份说明和控件应完整显示`).toEqual([]);
  }
  await page.screenshot({ path: testInfo.outputPath("backup-mobile.png"), fullPage: true });
});
