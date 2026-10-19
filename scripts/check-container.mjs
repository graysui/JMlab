// Verify the actual release image with disposable storage and test credentials.
import { spawn } from "node:child_process";
import { mkdtemp, writeFile, rm, rmdir } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve, dirname } from "node:path";
import { fileURLToPath } from "node:url";
import { randomBytes } from "node:crypto";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const image = process.argv[2];
if (!image) throw new Error("用法：node scripts/check-container.mjs <镜像标签或摘要>");
const name = `jmlab-check-${randomBytes(6).toString("hex")}`;
const volume = `${name}-data`;
const temporary = await mkdtemp(join(tmpdir(), `${name}-`));
const envFile = join(temporary, "test.env");
const username = "admin";
const password = "E2e123";
const relayImage = "node:24.18.0-bookworm-slim@sha256:6f7b03f7c2c8e2e784dcf9295400527b9b1270fd37b7e9a7285cf83b6951452d";
let createdVolume = false;
let createdContainer = false;

function command(executable, args, options = {}) {
  return new Promise((resolve, reject) => {
    const child = spawn(executable, args, { cwd: root, ...options });
    let output = "", errors = "";
    child.stdout?.on("data", (data) => { output += data; });
    child.stderr?.on("data", (data) => { errors += data; });
    child.once("error", reject);
    child.once("exit", (code) => code === 0 ? resolve(output.trim()) : reject(new Error(`${executable} exited ${code}: ${errors.slice(-2500)}`)));
  });
}
const docker = (...args) => command("docker", args);

async function waitReady(baseURL) {
  for (let attempt = 0; attempt < 90; attempt++) {
    try {
      const response = await fetch(`${baseURL}/health/ready`, { signal: AbortSignal.timeout(1000) });
      if (response.ok) return;
    } catch { /* Container is starting. */ }
    const state = JSON.parse(await docker("inspect", name, "--format", "{{json .State}}"));
    if (!state.Running) throw new Error("测试容器提前退出");
    await new Promise((resolve) => setTimeout(resolve, 1000));
  }
  throw new Error("测试容器未就绪");
}

async function keyFingerprints() {
  return docker("exec", name, "sh", "-ec", [
    "test -d /data/keys && test ! -L /data/keys",
    "test \"$(stat -c '%a' /data/keys)\" = 700",
    ...["master.key", "session.secret"].map((filename) => [
      `test -f /data/keys/${filename} && test ! -L /data/keys/${filename}`,
      `test \"$(stat -c '%a' /data/keys/${filename})\" = 600`,
    ].join(" && ")),
    "sha256sum /data/keys/master.key /data/keys/session.secret",
  ].join(" && "));
}

try {
  const imageConfig = JSON.parse(await docker("image", "inspect", image, "--format", "{{json .Config}}"));
  if (!Array.isArray(imageConfig.Healthcheck?.Test) || imageConfig.Healthcheck.Test.length < 2 || imageConfig.Healthcheck.Test[0] === "NONE") {
    throw new Error("发布镜像必须内置健康检查");
  }
  await docker("pull", relayImage);
  await writeFile(envFile, [
    "JMLAB_PUBLIC_URL=http://localhost:8097", `JMLAB_BOOTSTRAP_ADMIN_PASSWORD=${password}`,
  ].join("\n") + "\n", { mode: 0o600 });
  await docker("volume", "create", "--label", "jmlab.check=disposable", volume); createdVolume = true;
  await docker("run", "-d", "--name", name, "--label", "jmlab.check=disposable",
    "--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges:true",
    "--pids-limit", "256", "--memory", "768m", "--tmpfs", "/tmp:rw,noexec,nosuid,nodev,size=64m,mode=1777",
    "--env-file", envFile, "--volume", `${volume}:/data`, "--publish", "127.0.0.1::8097",
    ...(process.platform === "linux" ? ["--add-host", "host.docker.internal:host-gateway"] : []), image);
  createdContainer = true;
  const ports = JSON.parse(await docker("inspect", name, "--format", "{{json .NetworkSettings.Ports}}"));
  let baseURL = `http://127.0.0.1:${ports["8097/tcp"][0].HostPort}`;
  const networks = JSON.parse(await docker("inspect", name, "--format", "{{json .NetworkSettings.Networks}}"));
  const relayHost = process.platform === "linux" ? Object.values(networks)[0].Gateway : "host.docker.internal";
  await waitReady(baseURL);
  const uid = await docker("exec", name, "id", "-u");
  if (uid === "0") throw new Error("发布镜像不应以 root 运行");
  const initialKeyFingerprints = await keyFingerprints();
  await docker("exec", name, "sh", "-c", "test ! -e /app/modules && test -f /app/migrations/00027_large_playback_values.sql && test -f /app/web/index.html");
  const initialLogin = await fetch(`${baseURL}/api/v1/auth/login`, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ username, password, clientName: "Empty container check", deviceId: name }) });
  if (!initialLogin.ok) throw new Error("全新容器登录失败");
  const initialSession = await initialLogin.json();
  for (const path of ["/api/v1/admin/modules", "/api/v1/admin/modules/templates", "/api/v1/libraries"]) {
    const response = await fetch(`${baseURL}${path}`, { headers: { Authorization: `Bearer ${initialSession.accessToken}` } });
    if (!response.ok || (await response.json()).items.length !== 0) throw new Error(`全新容器不应预置内容：${path}`);
  }
  console.log(`容器检查：${image}，${baseURL}，非 root UID=${uid}，独立数据卷 ${volume}`);
  await command(process.execPath, [join(root, "e2e/node_modules/@playwright/test/cli.js"), "test"], {
    cwd: join(root, "e2e"), stdio: "inherit", env: { ...process.env, BASE_URL: baseURL,
      E2E_USERNAME: username, E2E_PASSWORD: password, E2E_MODULE_BIND_HOST: "0.0.0.0",
      E2E_DOCKER_CONTAINER: name, E2E_RELAY_IMAGE: relayImage, E2E_MODULE_RELAY_HOST: relayHost },
  });
  await docker("restart", name);
  const restartedPorts = JSON.parse(await docker("inspect", name, "--format", "{{json .NetworkSettings.Ports}}"));
  baseURL = `http://127.0.0.1:${restartedPorts["8097/tcp"][0].HostPort}`;
  await waitReady(baseURL);
  if (await keyFingerprints() !== initialKeyFingerprints) throw new Error("容器重启后自动密钥发生变化");
  const login = await fetch(`${baseURL}/api/v1/auth/login`, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ username, password, clientName: "Container check", deviceId: name }) });
  if (!login.ok) throw new Error("容器重启后登录失败");
  const session = await login.json();
  const response = await fetch(`${baseURL}/api/v1/admin/modules`, { headers: { Authorization: `Bearer ${session.accessToken}` } });
  if (!response.ok) throw new Error("容器重启后无法读取模块");
  const modules = await response.json();
  for (const id of ["browser-metadata", "browser-stream"]) {
    if (!modules.items.some((item) => item.manifest.id === id && item.enabled && item.configuredSecrets.includes("readToken"))) throw new Error(`模块 ${id} 配置未持久化`);
  }
  console.log("通过：发布镜像、全部浏览器流程、只读文件系统、自动密钥权限与重启复用、模块配置重启持久化。");
} finally {
  if (createdContainer) await docker("rm", "--force", name);
  if (createdVolume) await docker("volume", "rm", volume);
  await rm(envFile, { force: true });
  await rmdir(temporary).catch(() => {});
}
