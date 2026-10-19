import { type FormEvent, useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { apiFetch } from "../lib/api";

type BackupTargetSummary = { id: string; targetType: "local" | "webdav" | "git"; displayName: string; enabled: boolean; retentionCount: number; config: Record<string, unknown>; credentialConfigured: boolean; lastRunAt?: string; lastStatus?: string; lastError?: string; createdAt: string; updatedAt: string };
type BackupTargetForm = { targetType: BackupTargetSummary["targetType"]; displayName: string; enabled: boolean; retentionCount: string; path: string; url: string; remote: string; branch: string; authType: string; username: string; password: string; token: string; sshPrivateKey: string; knownHosts: string };
const emptyBackupTargetForm: BackupTargetForm = { targetType: "local", displayName: "", enabled: true, retentionCount: "7", path: "", url: "", remote: "", branch: "main", authType: "none", username: "", password: "", token: "", sshPrivateKey: "", knownHosts: "" };
type ImportPreviewData = { valid: boolean; formatVersion: string; mode: "merge" | "overwrite"; includeUserState: boolean; summary: { items: number; people: number; studios: number; series: number; tags: number; libraries: number; collections: number; playlists: number; playlistItems: number; userStates: number; created: number; updated: number; skipped: number }; conflicts: Array<{ entity: string; key: string; resolution: string; fields: Array<{ field: string; existing?: unknown; incoming?: unknown; resolution: string }> }>; errors: string[] };
type BackupRestorePreviewData = { valid: boolean; formatVersion: string; archiveFormatVersion: string; migrationVersion?: string; createdAt: string; archiveSha256: string; includeUserState: boolean; includeEncryptedConfig: boolean; encryptedConfigAvailable: boolean; configuration: { targets: number; modules: number; storageKeys: number }; mode: "merge" | "overwrite"; files: Array<{ name: string; size: number; sha256: string }>; summary: ImportPreviewData["summary"]; conflicts: ImportPreviewData["conflicts"]; errors: string[] };
type DatabaseResetSummary = { backupTaskId: string; virtualItems: number; people: number; studios: number; series: number; tags: number; images: number; libraries: number; collections: number; playlists: number; playbackSources: number; userItemStates: number; rankingLocks: number };

function arrayBufferToBase64(buffer: ArrayBuffer): string {
  const bytes = new Uint8Array(buffer);
  let binary = "";
  const chunkSize = 0x8000;
  for (let offset = 0; offset < bytes.length; offset += chunkSize) {
    binary += String.fromCharCode(...bytes.subarray(offset, Math.min(offset + chunkSize, bytes.length)));
  }
  return window.btoa(binary);
}

export function BackupAdminPage() {
  const [targets, setTargets] = useState<BackupTargetSummary[]>([]);
  const [targetForm, setTargetForm] = useState<BackupTargetForm>(emptyBackupTargetForm);
  const [editingTargetId, setEditingTargetId] = useState("");
  const [targetEditorOpen, setTargetEditorOpen] = useState(false);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");
  const [importText, setImportText] = useState("");
  const [mode, setMode] = useState<"merge" | "overwrite">("merge");
  const [includeUserState, setIncludeUserState] = useState(false);
  const [includeEncryptedConfig, setIncludeEncryptedConfig] = useState(false);
  const [preview, setPreview] = useState<ImportPreviewData | null>(null);
  const [restoreFile, setRestoreFile] = useState<File | null>(null);
  const [restoreConfig, setRestoreConfig] = useState(false);
  const [restorePreview, setRestorePreview] = useState<BackupRestorePreviewData | null>(null);
  const [resetBackupTaskId, setResetBackupTaskId] = useState("");

  useEffect(() => {
    let cancelled = false;
    apiFetch<{ items: BackupTargetSummary[] }>("/api/v1/backup/targets")
      .then((result) => { if (!cancelled) setTargets(result.items); })
      .catch((requestError) => { if (!cancelled) setError(requestError instanceof Error ? requestError.message : "读取备份目标失败"); })
      .finally(() => { if (!cancelled) setLoading(false); });
    return () => { cancelled = true; };
  }, []);

  async function loadTargets() {
    const result = await apiFetch<{ items: BackupTargetSummary[] }>("/api/v1/backup/targets");
    setTargets(result.items);
  }

  function updateTargetForm<K extends keyof BackupTargetForm>(key: K, value: BackupTargetForm[K]) {
    setTargetForm((current) => ({ ...current, [key]: value }));
  }

  function startCreateTarget() {
    setEditingTargetId("");
    setTargetForm(emptyBackupTargetForm);
    setTargetEditorOpen(true);
  }

  function startEditTarget(target: BackupTargetSummary) {
    const config = target.config;
    setEditingTargetId(target.id);
    setTargetForm({
      ...emptyBackupTargetForm,
      targetType: target.targetType,
      displayName: target.displayName,
      enabled: target.enabled,
      retentionCount: String(target.retentionCount),
      path: String(config.path ?? ""),
      url: String(config.url ?? ""),
      remote: String(config.remote ?? ""),
      branch: String(config.branch ?? "main"),
      authType: String(config.authType ?? "none"),
      username: String(config.username ?? ""),
    });
    setTargetEditorOpen(true);
    window.scrollTo({ top: 0, behavior: "smooth" });
  }

  function buildTargetConfig() {
    const config: Record<string, string> = {};
    if (targetForm.targetType === "local") {
      config.path = targetForm.path.trim();
    } else if (targetForm.targetType === "webdav") {
      config.url = targetForm.url.trim();
      config.authType = targetForm.authType;
      config.username = targetForm.username.trim();
      if (targetForm.password) config.password = targetForm.password;
      if (targetForm.token) config.token = targetForm.token;
    } else {
      config.remote = targetForm.remote.trim();
      config.branch = targetForm.branch.trim();
      config.authType = targetForm.authType;
      config.username = targetForm.username.trim();
      if (targetForm.token) config.token = targetForm.token;
      if (targetForm.sshPrivateKey) config.sshPrivateKey = targetForm.sshPrivateKey;
      if (targetForm.knownHosts) config.knownHosts = targetForm.knownHosts;
    }
    return config;
  }

  async function saveTarget(event: FormEvent) {
    event.preventDefault(); setBusy(true); setError(""); setMessage("");
    const payload = { targetType: targetForm.targetType, displayName: targetForm.displayName.trim(), enabled: targetForm.enabled, retentionCount: Number(targetForm.retentionCount), config: buildTargetConfig() };
    try {
      await apiFetch<BackupTargetSummary>(editingTargetId ? `/api/v1/backup/targets/${editingTargetId}` : "/api/v1/backup/targets", { method: editingTargetId ? "PATCH" : "POST", body: JSON.stringify(payload) });
      setMessage(editingTargetId ? "备份目标已更新" : "备份目标已创建"); setTargetEditorOpen(false); setEditingTargetId(""); setTargetForm(emptyBackupTargetForm); await loadTargets();
    } catch (requestError) { setError(requestError instanceof Error ? requestError.message : "保存备份目标失败"); }
    finally { setBusy(false); }
  }

  async function toggleTarget(target: BackupTargetSummary) {
    setBusy(true); setError("");
    try { await apiFetch(`/api/v1/backup/targets/${target.id}`, { method: "PATCH", body: JSON.stringify({ enabled: !target.enabled }) }); await loadTargets(); }
    catch (requestError) { setError(requestError instanceof Error ? requestError.message : "更新备份目标状态失败"); }
    finally { setBusy(false); }
  }

  async function testTarget(target: BackupTargetSummary) {
    setBusy(true); setError(""); setMessage("");
    try {
      const result = await apiFetch<{ result: { healthy: boolean; statusCode?: number; durationMs: number; error?: string } }>(`/api/v1/backup/targets/${target.id}/test`, { method: "POST", body: "{}" });
      setMessage(`${target.displayName}：${result.result.healthy ? "连接成功" : "连接失败"}${result.result.statusCode ? ` · HTTP ${result.result.statusCode}` : ""} · ${result.result.durationMs} ms${result.result.error ? ` · ${result.result.error}` : ""}`); await loadTargets();
    } catch (requestError) { setError(requestError instanceof Error ? requestError.message : "连接测试失败"); }
    finally { setBusy(false); }
  }

  async function deleteTarget(target: BackupTargetSummary) {
    if (!window.confirm(`确定删除备份目标「${target.displayName}」吗？`)) return;
    setBusy(true); setError("");
    try { await apiFetch(`/api/v1/backup/targets/${target.id}`, { method: "DELETE" }); setMessage("备份目标已删除"); await loadTargets(); }
    catch (requestError) { setError(requestError instanceof Error ? requestError.message : "删除备份目标失败"); }
    finally { setBusy(false); }
  }

  async function exportJSON() {
    setBusy(true); setError(""); setMessage("");
    try {
      const document = await apiFetch<Record<string, unknown>>(`/api/v1/backup/export?includeUserState=${includeUserState ? "1" : "0"}`);
      const blob = new Blob([JSON.stringify(document, null, 2)], { type: "application/json;charset=utf-8" });
      const url = URL.createObjectURL(blob);
      const anchor = window.document.createElement("a"); anchor.href = url; anchor.download = "jmlab-export.json"; anchor.click(); URL.revokeObjectURL(url);
      setMessage("JSON 导出已下载");
    } catch (requestError) { setError(requestError instanceof Error ? requestError.message : "导出 JSON 失败"); }
    finally { setBusy(false); }
  }

  async function runBackup() {
    if (!targets.some((target) => target.enabled)) { setError("请先配置至少一个启用的备份目标"); return; }
    if (!window.confirm("将按当前启用的备份目标创建归档，并在后台执行。继续吗？")) return;
    setBusy(true); setError(""); setMessage("");
    try {
      const result = await apiFetch<{ taskId: string; runId: string }>("/api/v1/backup/run", { method: "POST", headers: { "Idempotency-Key": `web-${Date.now()}` }, body: JSON.stringify({ includeUserState, includeEncryptedConfig }) });
      setMessage(`备份已开始，可在任务中心查看进度。任务编号：${result.taskId}`);
    } catch (requestError) { setError(requestError instanceof Error ? requestError.message : "创建备份任务失败"); }
    finally { setBusy(false); }
  }

  async function loadImportFile(file?: File) {
    if (!file) return;
    setError(""); setMessage(""); setPreview(null);
    try { setImportText(await file.text()); }
    catch (requestError) { setError(requestError instanceof Error ? requestError.message : "读取 JSON 文件失败"); }
  }

  function importPayload() {
    const document = JSON.parse(importText) as Record<string, unknown>;
    return { document, mode, includeUserState };
  }

  async function previewImport() {
    setBusy(true); setError(""); setMessage(""); setPreview(null);
    try { setPreview(await apiFetch<ImportPreviewData>("/api/v1/import/preview", { method: "POST", body: JSON.stringify(importPayload()) })); }
    catch (requestError) { setError(requestError instanceof Error ? requestError.message : "预览导入失败"); }
    finally { setBusy(false); }
  }

  async function applyImport() {
    if (!preview?.valid) { setError("请先完成有效的导入预览"); return; }
    const strategy = mode === "overwrite" ? "覆盖冲突字段" : "保留已有数据并合并缺失内容";
    if (!window.confirm(`即将按“${strategy}”导入 ${preview.summary.items} 个作品及其关系。导入在单次事务中执行，失败会整体回滚。继续吗？`)) return;
    setBusy(true); setError(""); setMessage("");
    try {
      const result = await apiFetch<{ summary: ImportPreviewData["summary"]; auditId: string }>("/api/v1/import/apply", { method: "POST", body: JSON.stringify(importPayload()) });
      setMessage(`导入完成：新增 ${result.summary.created} 项，更新 ${result.summary.updated} 项，审计记录 ${result.auditId}`);
      setPreview(null);
    } catch (requestError) { setError(requestError instanceof Error ? requestError.message : "执行导入失败，现有数据未修改"); }
    finally { setBusy(false); }
  }

  async function buildRestorePayload() {
    if (!restoreFile) throw new Error("请先选择 tar.gz 备份归档");
    if (restoreFile.size > 64 * 1024 * 1024) throw new Error("备份归档不能超过 64 MB");
    return { archiveBase64: arrayBufferToBase64(await restoreFile.arrayBuffer()), mode, includeUserState, includeEncryptedConfig: restoreConfig };
  }

  async function previewRestore() {
    setBusy(true); setError(""); setMessage(""); setRestorePreview(null);
    try {
      const result = await apiFetch<BackupRestorePreviewData>("/api/v1/backup/restore/preview", { method: "POST", body: JSON.stringify(await buildRestorePayload()) });
      setRestorePreview(result);
      setMessage(result.valid ? "备份归档校验通过，可以恢复" : "备份归档校验未通过");
    } catch (requestError) { setError(requestError instanceof Error ? requestError.message : "校验备份归档失败"); }
    finally { setBusy(false); }
  }

  async function restoreArchive() {
    if (!restorePreview?.valid) { setError("请先完成有效的备份归档预览"); return; }
    const confirmation = window.prompt("请输入确认词 RESTORE_BACKUP 以恢复归档");
    if (confirmation !== "RESTORE_BACKUP") { setError("确认词不正确，已取消恢复"); return; }
    setBusy(true); setError(""); setMessage("");
    try {
      const result = await apiFetch<{ summary: ImportPreviewData["summary"]; auditId: string; moduleReloadPending: boolean }>("/api/v1/backup/restore", { method: "POST", body: JSON.stringify({ ...(await buildRestorePayload()), confirmation }) });
      setMessage(`备份恢复完成：新增 ${result.summary.created} 项，更新 ${result.summary.updated} 项。${result.moduleReloadPending ? "部分来源暂时无法连接，设置已保存，系统会重试；请到来源管理查看。" : ""}`);
      await loadTargets().catch(() => setError("数据已恢复，备份目标列表暂时无法刷新，请刷新页面。"));
      setRestorePreview(null); setRestoreFile(null);
    } catch (requestError) { setError(requestError instanceof Error ? requestError.message : "恢复失败，现有数据未修改"); }
    finally { setBusy(false); }
  }

  async function resetDatabase() {
    if (!resetBackupTaskId.trim()) { setError("请输入已成功完成的备份任务 ID"); return; }
    const confirmation = window.prompt("这是高危操作。请输入确认词 RESET_DATABASE 以清空目录数据");
    if (confirmation !== "RESET_DATABASE") { setError("确认词不正确，已取消数据库重置"); return; }
    setBusy(true); setError(""); setMessage("");
    try {
      const result = await apiFetch<{ summary: DatabaseResetSummary; auditId: string }>("/api/v1/admin/database/reset", { method: "POST", body: JSON.stringify({ confirmation, backupTaskId: resetBackupTaskId.trim() }) });
      setMessage(`数据库重置完成：清理 ${result.summary.virtualItems} 部作品，审计记录 ${result.auditId}`);
      setResetBackupTaskId("");
    } catch (requestError) { setError(requestError instanceof Error ? requestError.message : "数据库重置失败，现有数据未修改"); }
    finally { setBusy(false); }
  }

  const targetLabels: Record<BackupTargetSummary["targetType"], string> = { local: "本地目录", webdav: "WebDAV", git: "Git" };
  const authTypeLabels: Record<string, string> = { none: "无需认证", basic: "用户名和密码", bearer: "Token", token: "Token", ssh: "SSH 密钥" };
  return <section className="content-card management-page backup-admin-page">
    <div className="relation-page-heading"><div><p className="eyebrow">数据管理</p><h1>备份与恢复</h1><p className="muted">先选择备份位置保存数据；恢复时上传备份文件，核对预览后再确认。</p></div><Link to="/settings" className="ghost-button">返回设置</Link></div>
    {error && <p className="form-error" role="alert">{error}</p>}{message && <p className="form-success" role="status">{message}</p>}
    <section className="dashboard-panel"><div className="home-module-heading"><div><p className="eyebrow">保存与恢复</p><h2>备份数据</h2></div><Link to="/settings/tasks" className="ghost-button compact-button">查看备份任务</Link></div>
      <fieldset disabled={busy} className="backup-data-options"><label className="inline-check"><input type="checkbox" checked={includeUserState} onChange={(event) => { setIncludeUserState(event.target.checked); setPreview(null); setRestorePreview(null); }} />包含用户状态（收藏、观看和播放进度）</label><label className="inline-check"><input type="checkbox" checked={includeEncryptedConfig} onChange={(event) => setIncludeEncryptedConfig(event.target.checked)} />包含来源、模块存储及备份目标的加密配置</label><label>冲突处理<select value={mode} onChange={(event) => { setMode(event.target.value as "merge" | "overwrite"); setPreview(null); setRestorePreview(null); }}><option value="merge">合并：保留已有字段</option><option value="overwrite">覆盖：使用导入字段</option></select></label></fieldset>
      <div className="backup-action-grid"><button type="button" className="primary-button" disabled={busy || !targets.some((target) => target.enabled)} onClick={() => void runBackup()}>运行备份</button></div>
      {!loading && !targets.some((target) => target.enabled) && <p className="muted">请先在下方添加并启用一个备份位置，再运行备份。</p>}
      <section className="import-preview-panel restore-panel"><div className="home-module-heading"><div><p className="eyebrow">恢复数据</p><h3>恢复 tar.gz 备份</h3><p className="muted">先选择文件并预览。默认只恢复媒体；需要迁移来源设置时，勾选下方选项，并确保使用备份时的主密钥。</p></div><span className={restorePreview?.valid ? "status-active" : "status-disabled"}>{restorePreview?.valid ? "可恢复" : "需要校验"}</span></div>
        <label className="import-file-field">选择 JMlab tar.gz 归档<input type="file" disabled={busy} accept=".tar.gz,.tgz,application/gzip" onChange={(event) => { setRestoreFile(event.target.files?.[0] ?? null); setRestorePreview(null); setError(""); }} /></label>
        <label className="inline-check"><input type="checkbox" disabled={busy} checked={restoreConfig} onChange={(event) => { setRestoreConfig(event.target.checked); setRestorePreview(null); }} />同时恢复来源、模块存储和备份目标设置</label><div className="backup-action-grid"><button type="button" className="ghost-button" disabled={busy || !restoreFile} onClick={() => void previewRestore()}>校验并预览归档</button><button type="button" className="primary-button" disabled={busy || !restorePreview?.valid} onClick={() => void restoreArchive()}>确认恢复备份</button></div>
        {restorePreview && <><div className="management-card-facts"><span>格式 {restorePreview.archiveFormatVersion}</span><span>作品 {restorePreview.summary.items}</span><span>关系 {restorePreview.summary.people + restorePreview.summary.studios + restorePreview.summary.series + restorePreview.summary.tags}</span><span>{restorePreview.includeUserState ? "包含用户状态" : "不含用户状态"}</span><span>{restorePreview.includeEncryptedConfig ? `恢复 ${restorePreview.configuration.modules} 个来源、${restorePreview.configuration.storageKeys} 项模块数据、${restorePreview.configuration.targets} 个备份目标` : restorePreview.encryptedConfigAvailable ? "归档含加密配置，本次保留当前设置" : "归档不含加密配置"}</span><span>SHA256 {restorePreview.archiveSha256.slice(0, 16)}…</span></div>{restorePreview.errors.map((item) => <p className="form-error" key={item}>{item}</p>)}{restorePreview.conflicts.length > 0 && <p className="muted">发现 {restorePreview.conflicts.length} 个冲突，恢复将按“{mode === "overwrite" ? "覆盖" : "合并"}”策略处理。</p>}</>}
      </section>
      <details className="import-preview-panel"><summary>高级：JSON 导入与导出</summary><p className="muted">用于跨实例迁移或手动整理数据。日常恢复请使用上方的备份归档。</p>
      <div className="backup-action-grid"><button type="button" className="primary-button" disabled={busy} onClick={() => void exportJSON()}>{busy ? "处理中…" : "下载 JSON 导出"}</button><button type="button" className="ghost-button" disabled={busy || !importText.trim()} onClick={() => void previewImport()}>预览并校验</button></div>
      <label className="import-file-field">选择 JMlab JSON 文件<input type="file" disabled={busy} accept="application/json,.json" onChange={(event) => void loadImportFile(event.target.files?.[0])} /></label>
      <label className="import-json-field">导入 JSON<textarea disabled={busy} spellCheck={false} value={importText} onChange={(event) => { setImportText(event.target.value); setPreview(null); }} placeholder="选择 JSON 文件，或粘贴导出内容" /></label>
      {preview && <section className="import-preview-panel" aria-live="polite"><div className="home-module-heading"><div><p className="eyebrow">导入预览</p><h3>{preview.valid ? `格式 ${preview.formatVersion}` : "校验未通过"}</h3></div><span className={preview.valid ? "status-active" : "status-disabled"}>{preview.valid ? "可以导入" : "需要修正"}</span></div>
        {preview.errors.map((item) => <p className="form-error" key={item}>{item}</p>)}
        {preview.valid && <><div className="management-card-facts"><span>作品 {preview.summary.items}</span><span>演员 {preview.summary.people}</span><span>片商 {preview.summary.studios}</span><span>系列 {preview.summary.series}</span><span>标签 {preview.summary.tags}</span><span>媒体库 {preview.summary.libraries}</span><span>合集 {preview.summary.collections}</span><span>播放列表 {preview.summary.playlists}</span><span>列表项目 {preview.summary.playlistItems}</span><span>状态 {preview.summary.userStates}</span></div>
          {preview.conflicts.length > 0 ? <div className="import-conflict-list"><h4>冲突字段（{preview.conflicts.length}）</h4>{preview.conflicts.map((conflict) => <article key={`${conflict.entity}:${conflict.key}`}><strong>{conflict.entity} · {conflict.key}</strong>{conflict.fields.map((field) => <small key={field.field}>{field.field}：现有「{String(field.existing ?? "空")}」→ 导入「{String(field.incoming ?? "空")}」；{field.resolution === "overwrite" ? "覆盖" : "保留现有值"}</small>)}</article>)}</div> : <p className="muted">没有发现字段冲突。执行时仍会进行数据库约束校验。</p>}
          <button type="button" className="primary-button" disabled={busy} onClick={() => void applyImport()}>确认并执行导入</button>
        </>}
      </section>}
      </details>

    </section>
    <section className="dashboard-panel"><div className="home-module-heading"><div><p className="eyebrow">备份目标</p><h2>已配置目标</h2></div><div className="admin-toolbar"><span className="muted">{targets.length} 个目标</span><button type="button" className="primary-button compact-button" onClick={startCreateTarget}>添加备份目标</button></div></div>
      {targetEditorOpen && <form className="relation-editor backup-target-editor" onSubmit={saveTarget}>
        <div className="relation-editor-heading"><div><p className="eyebrow">{editingTargetId ? "编辑备份目标" : "新建备份目标"}</p><h3>{editingTargetId ? "更新连接配置" : "选择备份位置"}</h3></div><button type="button" className="ghost-button" onClick={() => { setTargetEditorOpen(false); setEditingTargetId(""); }}>关闭</button></div>
        <label>目标类型<select disabled={Boolean(editingTargetId)} value={targetForm.targetType} onChange={(event) => updateTargetForm("targetType", event.target.value as BackupTargetForm["targetType"])}><option value="local">本地目录</option><option value="webdav">WebDAV</option><option value="git">Git</option></select></label>
        <label>显示名称<input required maxLength={200} value={targetForm.displayName} onChange={(event) => updateTargetForm("displayName", event.target.value)} /></label>
        <label>保留份数<input required type="number" min="1" max="3650" value={targetForm.retentionCount} onChange={(event) => updateTargetForm("retentionCount", event.target.value)} /></label>
        {targetForm.targetType === "local" && <label className="wide-field">备份子目录<input required placeholder="daily" value={targetForm.path} onChange={(event) => updateTargetForm("path", event.target.value)} /><small className="muted">填写 daily 等子目录名；文件保存在服务器的备份目录内。</small></label>}
        {targetForm.targetType === "webdav" && <>
          <label className="wide-field">WebDAV HTTPS 地址<input required type="url" placeholder="https://backup.example.com/dav/jmlab" value={targetForm.url} onChange={(event) => updateTargetForm("url", event.target.value)} /></label>
          <label>认证方式<select value={targetForm.authType} onChange={(event) => updateTargetForm("authType", event.target.value)}><option value="none">无需认证</option><option value="basic">用户名和密码</option><option value="bearer">Token</option></select></label>
          {targetForm.authType === "basic" && <><label>用户名<input value={targetForm.username} onChange={(event) => updateTargetForm("username", event.target.value)} /></label><label>密码<input type="password" autoComplete="new-password" value={targetForm.password} onChange={(event) => updateTargetForm("password", event.target.value)} placeholder={editingTargetId ? "留空保留现有密码" : "输入密码"} /></label></>}
          {targetForm.authType === "bearer" && <label className="wide-field">访问 Token<input type="password" autoComplete="new-password" value={targetForm.token} onChange={(event) => updateTargetForm("token", event.target.value)} placeholder={editingTargetId ? "留空保留现有 Token" : "输入 Token"} /></label>}
        </>}
        {targetForm.targetType === "git" && <>
          <label className="wide-field">Git 远端地址<input required placeholder="https://git.example.com/user/jmlab-backup.git" value={targetForm.remote} onChange={(event) => updateTargetForm("remote", event.target.value)} /><small className="muted">地址中不能嵌入用户名或 Token。</small></label>
          <label>分支<input value={targetForm.branch} onChange={(event) => updateTargetForm("branch", event.target.value)} /></label>
          <label>认证方式<select value={targetForm.authType} onChange={(event) => updateTargetForm("authType", event.target.value)}><option value="none">无需认证</option><option value="token">HTTPS Token</option><option value="ssh">SSH 私钥</option></select></label>
          {targetForm.authType === "token" && <><label>用户名<input value={targetForm.username} onChange={(event) => updateTargetForm("username", event.target.value)} /></label><label>访问 Token<input type="password" autoComplete="new-password" value={targetForm.token} onChange={(event) => updateTargetForm("token", event.target.value)} placeholder={editingTargetId ? "留空保留现有 Token" : "输入 Token"} /></label></>}
          {targetForm.authType === "ssh" && <><label className="wide-field">SSH 私钥<textarea rows={5} spellCheck={false} value={targetForm.sshPrivateKey} onChange={(event) => updateTargetForm("sshPrivateKey", event.target.value)} placeholder={editingTargetId ? "留空保留现有私钥" : "粘贴私钥内容"} /></label><label className="wide-field">known_hosts<textarea rows={3} spellCheck={false} value={targetForm.knownHosts} onChange={(event) => updateTargetForm("knownHosts", event.target.value)} placeholder={editingTargetId ? "留空保留现有主机指纹" : "粘贴可信主机指纹"} /></label></>}
        </>}
        <label className="inline-check"><input type="checkbox" checked={targetForm.enabled} onChange={(event) => updateTargetForm("enabled", event.target.checked)} />启用此目标</label>
        {editingTargetId && <p className="muted wide-field">密码、Token 和私钥不会回显；密码框留空时保留已保存的凭证。</p>}
        <button className="primary-button" disabled={busy}>{busy ? "保存中…" : editingTargetId ? "保存目标" : "创建目标"}</button>
      </form>}
      {loading ? <p className="muted">正在读取备份目标…</p> : targets.length === 0 ? <div className="empty-card compact-empty"><h3>尚未配置备份目标</h3><p className="muted">添加本地 `/backup` 目录、WebDAV 或 Git 目标，并先测试连接。</p></div> : <div className="backup-target-list">{targets.map((target) => <article className="backup-target-card" key={target.id}><div className="management-card-heading"><div><p className="eyebrow">{targetLabels[target.targetType] || target.targetType}</p><h3>{target.displayName}</h3></div><span className={target.enabled ? "status-active" : "status-disabled"}>{target.enabled ? "启用" : "停用"}</span></div><div className="management-card-facts"><span>保留 {target.retentionCount} 份</span>{target.targetType === "local" && <span>{String(target.config.path ?? "")}</span>}{target.targetType === "webdav" && <><span>{String(target.config.url ?? "")}</span><span>{authTypeLabels[String(target.config.authType ?? "none")]}</span></>}{target.targetType === "git" && <><span>{String(target.config.remote ?? "")}</span><span>分支 {String(target.config.branch ?? "main")}</span><span>{authTypeLabels[String(target.config.authType ?? "none")]}</span></>}{target.credentialConfigured && <span>凭证已保存</span>}{target.lastStatus && <span>上次测试：{target.lastStatus}</span>}{target.lastRunAt && <span>{new Date(target.lastRunAt).toLocaleString()}</span>}</div>{target.lastError && <p className="form-error">{target.lastError}</p>}<small className="muted">目标 ID：{target.id}</small><div className="table-actions"><button type="button" className="ghost-button compact-button" disabled={busy} onClick={() => void testTarget(target)}>测试连接</button><button type="button" className="ghost-button compact-button" onClick={() => startEditTarget(target)}>编辑</button><button type="button" className="ghost-button compact-button" disabled={busy} onClick={() => void toggleTarget(target)}>{target.enabled ? "停用" : "启用"}</button><button type="button" className="ghost-button compact-button" disabled={busy} onClick={() => void deleteTarget(target)}>删除</button></div></article>)}</div>}
    </section>
    <details className="dashboard-panel danger-panel"><summary>高级：重置媒体库数据</summary><div><div className="home-module-heading"><div><p className="eyebrow">高危操作</p><h2>数据库重置保护</h2><p className="muted">必须先完成成功备份，并提供备份任务 ID；重置会清空目录、关系、媒体库、合集、播放源和用户状态，保留账号、配置、任务历史与审计日志。</p></div><span className="status-disabled">需要二次确认</span></div><label className="wide-field">成功备份任务 ID<input value={resetBackupTaskId} onChange={(event) => setResetBackupTaskId(event.target.value)} placeholder="task_…" /></label><button type="button" className="ghost-button" disabled={busy || !resetBackupTaskId.trim()} onClick={() => void resetDatabase()}>验证备份并重置数据库</button></div></details>
    <p className="muted">备份包含媒体信息和选定的用户记录；来源设置只有勾选后才加密保存。恢复加密设置需要原主密钥，备份不包含视频文件、播放直链、模块程序或主密钥。</p>
  </section>;
}
