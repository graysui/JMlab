// Native, isolated browser regression. No Docker or existing deployment data.
import { spawn } from "node:child_process";
import { once } from "node:events";
import { mkdtemp, access } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve, dirname } from "node:path";
import { fileURLToPath } from "node:url";
import { randomBytes } from "node:crypto";
import { createServer } from "node:net";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const workspace = await mkdtemp(join(tmpdir(), "jmlab-browser-"));
const executable = join(workspace, process.platform === "win32" ? "jmlab.exe" : "jmlab");
const runner = join(root, "e2e/node_modules/@playwright/test/cli.js");
await access(runner).catch(() => { throw new Error("请先运行 npm --prefix e2e ci 安装浏览器测试依赖。"); });
await access(join(root, "web/app/dist/index.html")).catch(() => { throw new Error("请先运行 npm --prefix web/app run build。"); });
const portProbe = createServer(); portProbe.listen(0, "127.0.0.1"); await once(portProbe, "listening");
const port = portProbe.address().port; await new Promise((resolve) => portProbe.close(resolve));
function command(command, args, options = {}) {
  const child = spawn(command, args, { cwd: root, stdio: "inherit", ...options });
  return new Promise((resolve, reject) => { child.once("error", reject); child.once("exit", (code) => code === 0 ? resolve() : reject(new Error(`${command} exited ${code}`))); });
}
await command("go", ["build", "-o", executable, "./cmd/jmlab"]);
const baseURL = `http://127.0.0.1:${port}`;
const env = { ...process.env,
  JMLAB_ENV: "development", JMLAB_HTTP_ADDR: `127.0.0.1:${port}`, JMLAB_PUBLIC_URL: baseURL,
  JMLAB_DATA_DIR: workspace, JMLAB_DATABASE_URL: `sqlite://${join(workspace,"browser.db").replaceAll("\\","/")}`,
  JMLAB_MODULES_DIR: join(workspace,"modules"), JMLAB_MIGRATIONS_DIR: join(root,"migrations"), JMLAB_WEB_DIR: join(root,"web/app/dist"),
  JMLAB_MASTER_KEY: randomBytes(32).toString("hex"), JMLAB_SESSION_SECRET: randomBytes(32).toString("hex"),
  JMLAB_BOOTSTRAP_ADMIN_USERNAME: "e2e-admin", JMLAB_BOOTSTRAP_ADMIN_PASSWORD: "E2e-Playwright-Password-123",
  BASE_URL: baseURL, E2E_USERNAME: "e2e-admin", E2E_PASSWORD: "E2e-Playwright-Password-123",
};
const server = spawn(executable, [], { cwd: root, env, stdio: ["ignore", "ignore", "inherit"] });
const serverExit = once(server, "exit");
let serverError; server.on("error", (error) => { serverError = error; });
try {
  let ready = false;
  for (let attempt = 0; attempt < 100; attempt++) {
    if (serverError || server.exitCode !== null) throw serverError || new Error("临时服务提前退出");
    try { const response = await fetch(`${baseURL}/health/ready`, { signal: AbortSignal.timeout(1000) }); if (response.ok) { ready = true; break; } } catch { /* Wait for bootstrap. */ }
    await new Promise((resolve) => setTimeout(resolve, 200));
  }
  if (!ready) throw new Error("临时服务未就绪");
  const login = await fetch(`${baseURL}/api/v1/auth/login`, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ username: env.E2E_USERNAME, password: env.E2E_PASSWORD, clientName: "Empty install check", deviceId: "empty-install" }) });
  if (!login.ok) throw new Error("空安装检查登录失败");
  const session = await login.json();
  for (const path of ["/api/v1/admin/modules", "/api/v1/admin/modules/templates", "/api/v1/libraries"]) {
    const response = await fetch(`${baseURL}${path}`, { headers: { Authorization: `Bearer ${session.accessToken}` } });
    if (!response.ok || (await response.json()).items.length !== 0) throw new Error(`全新安装不应预置内容：${path}`);
  }
  console.log(`浏览器测试使用独立数据目录：${workspace}`);
  await command(process.execPath, [runner, "test", ...process.argv.slice(2)], { cwd: join(root,"e2e"), env });
} finally {
  if (server.exitCode === null) {
    server.kill("SIGTERM");
    const force = setTimeout(() => server.kill("SIGKILL"), 5000); force.unref();
    await serverExit; clearTimeout(force);
  }
}
