// Disposable protocol fixtures for host checks; never shipped in the application image.
import { test as base, expect } from "@playwright/test";
import { createServer } from "node:http";
import { execFile } from "node:child_process";
import { promisify } from "node:util";

const exec = promisify(execFile);
export { expect };
export const test = base.extend({
  hostModules: [async ({}, use, workerInfo) => {
    const service = createServer((req, res) => {
      res.setHeader("Content-Type", "application/json");
      const path = new URL(req.url, "http://localhost").pathname;
      if (path === "/api/v1/caps") return res.end(JSON.stringify({ supportedTypes: ["movie"], supportsRankings: true, supportsStreams: true }));
      if (path === "/api/v1/rankings") return res.end(JSON.stringify({ items: [] }));
      if (path === "/api/v1/stream/discover") return res.end(JSON.stringify({ candidates: [] }));
      res.writeHead(404).end();
    });
    await new Promise((resolve) => service.listen(0, process.env.E2E_MODULE_BIND_HOST || "127.0.0.1", resolve));
    const port = service.address().port;
    let relay;
    const baseURL = workerInfo.project.use.baseURL;
    const login = await fetch(`${baseURL}/api/v1/auth/login`, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ username: process.env.E2E_USERNAME || "e2e-admin", password: process.env.E2E_PASSWORD || "E2e-Playwright-Password-123", clientName: "Isolated check", deviceId: "protocol-fixture" }) });
    if (!login.ok) throw new Error(`测试登录失败：${login.status}`);
    const session = await login.json();
    const headers = { Authorization: `Bearer ${session.accessToken}`, "Content-Type": "application/json" };
    try {
      if (process.env.E2E_DOCKER_CONTAINER) {
        relay = `${process.env.E2E_DOCKER_CONTAINER}-fixture-${port}`;
        const target = process.env.E2E_MODULE_RELAY_HOST || "host.docker.internal";
        const code = `const net=require('node:net');net.createServer(c=>{const s=net.connect(${port},${JSON.stringify(target)});c.pipe(s);s.pipe(c);s.on('error',()=>c.destroy());c.on('error',()=>s.destroy());}).listen(${port},'127.0.0.1')`;
        await exec("docker", ["run", "-d", "--name", relay, "--network", `container:${process.env.E2E_DOCKER_CONTAINER}`, "--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges:true", "--entrypoint", "node", process.env.E2E_RELAY_IMAGE, "-e", code]);
      }
      const periods = ["daily", "weekly", "monthly", "all", "2025"].map((value) => ({ value, label: value }));
      const rankingScopes = ["weekly", "monthly"].map((period) => ({ id: `ranking-${period}`, name: period, rankingType: "ranking", rankingPeriod: period }));
      const manifests = [
        { id: "test-catalog", name: "测试目录模块", type: "metadata", rankings: ["ranking", "playback", "top250"].map((id) => ({ id, name: id, periods })), libraryTemplates: [
          { id: "popular-ranking", name: "测试推荐", itemType: "movie", scope: "metadata", filter: {}, rankingScopes },
          { id: "top250-ranking", name: "测试年度精选", itemType: "movie", scope: "metadata", filter: {}, rankingScopes: [{ id: "top250-2025", name: "2025", rankingType: "top250", rankingPeriod: "2025" }] },
        ] },
        { id: "test-stream", name: "测试媒体流模块", type: "stream", libraryTemplates: [{ id: "playable-items", name: "测试可用媒体", itemType: "movie", scope: "stream", filter: {} }] },
      ];
      for (const item of manifests) {
        item.libraryTemplates = item.libraryTemplates.map((template) => ({ ...template, sort: { field: "title", direction: "asc" } }));
        await fetch(`${baseURL}/api/v1/admin/modules/${item.id}`, { method: "DELETE", headers });
        const response = await fetch(`${baseURL}/api/v1/admin/modules`, { method: "POST", headers, body: JSON.stringify({ enabled: true, manifest: { ...item, version: "1", supportedItemTypes: ["movie"], transport: { type: "http", url: `http://127.0.0.1:${port}` } } }) });
        if (!response.ok) throw new Error(`测试清单安装失败：${response.status} ${await response.text()}`);
      }
      await use();
    } finally {
      for (const id of ["test-catalog", "test-stream"]) await fetch(`${baseURL}/api/v1/admin/modules/${id}`, { method: "DELETE", headers }).catch(() => {});
      if (relay) await exec("docker", ["rm", "-f", relay]);
      service.closeAllConnections();
      await new Promise((resolve) => service.close(resolve));
    }
  }, { scope: "worker", auto: true }],
});
