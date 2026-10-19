import { useState, type FormEvent } from "react";
import { apiFetch } from "../lib/api";

export function ModulePackageImporter<T extends { packageToken: string; manifest: { id: string } }>({ platform, moduleId, onPreview }: { platform: string; moduleId?: string; onPreview: (preview: T) => void }) {
  const [mode, setMode] = useState("file");
  const [file, setFile] = useState<File | null>(null);
  const [url, setURL] = useState("");
  const [sha256, setSHA256] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  async function inspect(event: FormEvent) {
    event.preventDefault(); setError(""); setBusy(true);
    try {
      let body: BodyInit;
      if (mode === "file") {
        if (!file) throw new Error("请先选择 ZIP 模块包。");
        if (file.size > 64 * 1024 * 1024) throw new Error("模块 ZIP 包不能超过 64 MB。");
        const form = new FormData(); form.append("file", file); body = form;
      } else { body = JSON.stringify({ url: url.trim(), sha256: sha256.trim() || undefined }); }
      const preview = await apiFetch<T>("/api/v1/admin/modules/packages/inspect", { method: "POST", body });
      if (moduleId && preview.manifest.id !== moduleId) {
        await apiFetch(`/api/v1/admin/modules/packages/${encodeURIComponent(preview.packageToken)}`, { method: "DELETE" });
        throw new Error("更新包的模块 ID 与当前模块不同，请选择同一模块的 ZIP 包。");
      }
      onPreview(preview);
    } catch (e) { setError(e instanceof Error ? e.message : "读取模块包失败，请重试。"); }
    finally { setBusy(false); }
  }
  return <form className="module-config-form" onSubmit={inspect}>
    <fieldset disabled={busy}>
      <div className="table-actions" role="group" aria-label="模块包来源"><button type="button" className="ghost-button" aria-pressed={mode === "file"} onClick={() => { setMode("file"); setError(""); }}>本地 ZIP 文件</button><button type="button" className="ghost-button" aria-pressed={mode === "url"} onClick={() => { setMode("url"); setError(""); }}>网址导入</button></div>
      <p className="muted">{platform ? `当前服务器：${platform}。` : ""}请选择适用于服务器的模块包，最大 64 MB。</p>
      {mode === "file" ? <label>模块 ZIP 文件<input type="file" accept=".zip,application/zip,application/x-zip-compressed" required onChange={(event) => setFile(event.target.files?.[0] || null)} /></label> : <><label>模块包下载网址<input type="url" required value={url} onChange={(event) => setURL(event.target.value)} placeholder="https://example.com/module.zip" /><small>填写公网 ZIP 文件直链，推荐 HTTPS 地址。</small></label><details><summary>文件校验（可选）</summary><label>提供者公布的 SHA256<input value={sha256} onChange={(event) => setSHA256(event.target.value)} pattern="[a-fA-F0-9]{64}" maxLength={64} /></label></details></>}
      {error && <p className="form-error" role="alert">{error}</p>}
      {busy && <p role="status">正在{mode === "url" ? "下载并" : ""}检查模块包，请稍候…</p>}
      <button className="primary-button">{busy ? "检查中…" : moduleId ? "读取更新包" : "读取模块包"}</button>
    </fieldset>
  </form>;
}
