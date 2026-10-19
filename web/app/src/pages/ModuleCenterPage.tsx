import { ModulePackageImporter } from "../components/ModulePackageImporter";
import { Icon } from "../components/Icon";
import { useEffect, useRef, useState, type FormEvent } from "react";
import { Link, useLocation, useNavigate, useSearchParams } from "react-router-dom";
import { apiFetch, readSession, userHasRole } from "../lib/api";
import { ModuleActionsPanel, type ModuleAction } from "../components/ModuleActionsPanel";
import { ModuleRankingsPanel, type RankingDefinition } from "../components/ModuleRankingsPanel";
import { ModuleFieldInput, fieldInitial, fieldValue, type ModuleField } from "../components/ModuleFields";
import { PlaybackScopePanel, type LibraryScope } from "../components/PlaybackScopePanel";

type Field = ModuleField;
type Manifest = {
  actions?: ModuleAction[]; rankings?: RankingDefinition[];
  id: string; name: string; description?: string; version: string; type: string; supportedItemTypes?: string[];
  transport: { type: string; url?: string }; configSchema?: Record<string, Field>;
  runtime?: { os: string; arch: string; executable: string; args?: string[]; release?: string };
  mediaAccess?: { allowedOrigins?: string[]; allowPrivate?: boolean };
  libraryTemplates?: Array<{ id: string; name: string; description?: string; rankingScopes?: Array<{ id: string; name: string }> }>;
};
type PackagePreview = { packageToken: string; manifest: Manifest; sha256: string; compressedBytes: number; expiresAt: string; revision: string };
type Installed = { revision: string; libraryScope?: LibraryScope | null; libraryScopeNames?: string[]; manifest: Manifest; status: string; lastError?: string; enabled: boolean; config: Record<string, unknown>; configuredSecrets: string[]; tokenConfigured: boolean; health?: { state: string; consecutiveFailures: number; lastCheckedAt?: string; nextCheckAt?: string }; events?: { queued: number; delivered: number; failed: number; dropped: number } };
const errorText = (error: unknown) => error instanceof Error ? error.message : "操作失败，请稍后重试";
const modulePath = (id: string) => `/admin/modules/${encodeURIComponent(id)}`;
const typeLabel = (type: string) => type === "metadata" ? "内容信息" : type === "stream" ? "视频播放" : "内容与视频";
const needsAttention = (item: Installed) => item.enabled && (item.status !== "active" || item.health?.state === "degraded" || item.health?.state === "open");
const statusLabel = (item: Installed) => !item.enabled ? "已停用" : needsAttention(item) ? "连接异常" : "已启用";

export function ModuleCenterPage() {
  const navigate = useNavigate();
  const location = useLocation();
  const [params] = useSearchParams();
  const rawID = location.pathname.slice("/admin/modules".length).replace(/^\//, "");
  let selectedID = rawID;
  try { selectedID = decodeURIComponent(rawID); } catch { /* Show the missing module state. */ }
  const [items, setItems] = useState<Installed[]>([]);
  const [editing, setEditing] = useState<Manifest | null>(null);
  const [packagePreview, setPackagePreview] = useState<PackagePreview | null>(null);
  const [platform, setPlatform] = useState("");
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");
  const [confirmRemove, setConfirmRemove] = useState(false);
  const [filter, setFilter] = useState("");
  const [statusFilter, setStatusFilter] = useState("all");
  const allowed = userHasRole(readSession()?.user, "Admin");
  const selected = items.find((item) => item.manifest.id === selectedID);
  const adding = params.get("view") === "available";
  const tab = params.get("tab") || "settings";
  async function load() {
    const installed = await apiFetch<{ items: Installed[]; packagePlatform?: string }>("/api/v1/admin/modules");
    setItems(installed.items);
    setPlatform(installed.packagePlatform || "");
    return installed.items;
  }
  useEffect(() => {
    if (!allowed) return;
    let active = true;
    void load().catch((e) => { if (active) setError(errorText(e)); }).finally(() => { if (active) setLoading(false); });
    const timer = window.setInterval(() => { if (document.visibilityState === "visible") void load().catch(() => {}); }, 15000);
    return () => { active = false; window.clearInterval(timer); };
  }, [allowed]);
  useEffect(() => { setError(""); setMessage(""); setConfirmRemove(false); setEditing(null); setPackagePreview(null); }, [location.pathname]);
  useEffect(() => () => { if (packagePreview) void apiFetch(`/api/v1/admin/modules/packages/${encodeURIComponent(packagePreview.packageToken)}`, { method: "DELETE" }).catch(() => {}); }, [packagePreview]);
  function acceptPackage(preview: PackagePreview) {
    const existing = items.find((item) => item.manifest.id === preview.manifest.id);
    setPackagePreview(preview);
    setEditing({ ...preview.manifest, mediaAccess: preview.manifest.mediaAccess || existing?.manifest.mediaAccess });
  }
  const editor = editing && <ModuleForm key={packagePreview?.packageToken || editing.id} manifest={editing} packagePreview={packagePreview || undefined} installed={packagePreview ? items.find((item) => item.manifest.id === editing.id) : undefined} onCancel={() => { setEditing(null); setPackagePreview(null); }} onSaved={async () => { const id = editing.id; await load(); setEditing(null); setPackagePreview(null); navigate(modulePath(id)); setMessage("模块已保存，无需重启。"); }} />;
  async function toggle(item: Installed) {
    setBusy(true); setError(""); setMessage("");
    try {
      await apiFetch(`/api/v1/admin/modules/${encodeURIComponent(item.manifest.id)}`, { method: "PATCH", body: JSON.stringify({ enabled: !item.enabled }) });
      const refreshed = await load();
      setMessage(item.enabled ? "模块已停用。已添加的媒体和播放进度会保留。" : refreshed.find((entry) => entry.manifest.id === item.manifest.id)?.status === "active" ? "模块已启用，现在可以使用。" : "设置已保存，模块暂时无法连接，系统会自动重试。");
    } catch (e) { setError(errorText(e)); } finally { setBusy(false); }
  }
  async function scan() {
    setBusy(true); setError(""); setMessage("");
    try {
      const result = await apiFetch<{ errors: Record<string, string> }>("/api/v1/admin/modules/reload", { method: "POST" });
      await load();
      const failures = Object.values(result.errors);
      if (failures.length) setError(`刷新完成，${failures.length} 个模块需要处理：${failures.join("；")}`);
      else setMessage("模块状态已刷新，更新即时生效。");
    } catch (e) { setError(errorText(e)); } finally { setBusy(false); }
  }
  async function uninstall(item: Installed) {
    setBusy(true); setError("");
    try {
      await apiFetch(`/api/v1/admin/modules/${encodeURIComponent(item.manifest.id)}`, { method: "DELETE" });
      await load(); navigate("/admin/modules"); setMessage("模块已卸载，已添加的媒体和观看记录保留。");
    } catch (e) { setError(errorText(e)); } finally { setBusy(false); setConfirmRemove(false); }
  }
  const feedback = <>{error && <p className="form-error" role="alert">{error}</p>}{message && <p className="form-success" role="status">{message}</p>}</>;
  if (!allowed) return <section className="content-card"><h1>需要管理员权限</h1><Link to="/">返回首页</Link></section>;
  if (loading) return <section className="content-card"><h1>模块管理</h1><p role="status">正在读取模块…</p>{feedback}</section>;
  if (selectedID && !selected) return <section className="content-card"><h1>找不到这个模块</h1><p className="muted">模块可能已卸载，请返回列表查看当前可用模块。</p>{feedback}<Link to="/admin/modules" className="primary-button">返回模块管理</Link></section>;
  if (selected) {
    const external = selected.manifest.transport.type === "http";
    const hasRankings = !!selected.manifest.rankings?.length || selected.manifest.libraryTemplates?.some((t) => t.rankingScopes?.length);
    return <section className="content-card management-page module-center">
      <Link to="/admin/modules" className="module-back-link">← 全部模块</Link>
      <div className="relation-page-heading"><div><p className="eyebrow">{typeLabel(selected.manifest.type)} · {selected.manifest.runtime ? "本地模块" : external ? "外部模块" : "内置模块"}</p><h1>{selected.manifest.name}</h1><p className="muted">{selected.manifest.description || "在这里设置和管理此模块的功能。"}</p></div><div className="table-actions"><span className={!selected.enabled ? "status-neutral" : needsAttention(selected) ? "status-disabled" : "status-active"}>{statusLabel(selected)}</span><button type="button" className="ghost-button" disabled={busy} onClick={() => void toggle(selected)}>{selected.enabled ? "停用模块" : "启用模块"}</button></div></div>
      {editor}{feedback}{selected.lastError && <p className="form-error" role="alert">{selected.lastError}</p>}
      {!selected.enabled && <p className="module-notice">模块已停用，启用后会立即恢复服务。已添加的内容和观看记录会保留。</p>}
      <nav className="module-tabs" aria-label="模块功能"><Link aria-current={tab === "settings" ? "page" : undefined} to={modulePath(selectedID)}>功能设置</Link>{hasRankings && <Link aria-current={tab === "rankings" ? "page" : undefined} to={`${modulePath(selectedID)}?tab=rankings`}>榜单与自动更新</Link>}{!!selected.manifest.libraryTemplates?.length && <Link aria-current={tab === "libraries" ? "page" : undefined} to={`${modulePath(selectedID)}?tab=libraries`}>虚拟库模板</Link>}<Link aria-current={tab === "status" ? "page" : undefined} to={`${modulePath(selectedID)}?tab=status`}>状态与维护</Link></nav>
      {tab === "settings" && selected.manifest.type !== "metadata" && <PlaybackScopePanel key={`scope:${selectedID}`} moduleId={selectedID} value={selected.libraryScope} onSaved={async () => { await load(); }} />}
      {tab === "libraries" ? <section className="dashboard-panel"><div className="home-module-heading"><div><h2>虚拟库模板</h2><p className="muted">由本模块提供；创建时可选择相应范围，只包含已入库内容。</p></div><Link to="/settings/libraries" className="ghost-button">管理虚拟媒体库</Link></div>{selected.manifest.libraryTemplates?.length ? <div className="management-card-grid">{selected.manifest.libraryTemplates.map((template) => <article className="management-card" key={template.id}><h3>{template.name}</h3><p className="muted">{template.description}</p><p className="muted">{template.rankingScopes?.length ? `榜单范围：${template.rankingScopes.slice(0, 3).map((scope) => scope.name).join("、")}${template.rankingScopes.length > 3 ? `等 ${template.rankingScopes.length} 项` : ""}` : "不限榜单"}</p></article>)}</div> : <p className="muted">此模块尚未提供模板。</p>}</section> : tab === "status" ? <section className="dashboard-panel"><h2>运行状态</h2><dl className="module-facts"><div><dt>当前版本</dt><dd>{selected.manifest.version}</dd></div><div><dt>连接状态</dt><dd>{statusLabel(selected)}</dd></div><div><dt>最近检查</dt><dd>{selected.health?.lastCheckedAt ? new Date(selected.health.lastCheckedAt).toLocaleString() : "按实际使用检查"}</dd></div></dl><button className="ghost-button" disabled={busy} onClick={() => void scan()}>刷新模块状态</button>{selected.events && <p className="muted">通知已发送 {selected.events.delivered} 次，失败 {selected.events.failed} 次，等待 {selected.events.queued} 次，丢弃 {selected.events.dropped} 次。</p>}
        {external && <>{selected.manifest.runtime ? <details className="module-advanced"><summary>更新模块包</summary><p className="muted">导入同一模块的新 ZIP 包。检查失败会保留当前版本，成功后自动切换。</p><ModulePackageImporter<PackagePreview> platform={platform} moduleId={selectedID} onPreview={acceptPackage} /></details> : <details className="module-advanced"><summary>更新模块清单</summary><p className="muted">上传模块提供者的新清单。现有凭证会保留，连接检查失败时继续使用原版本。外部服务程序需由服务提供者更新。</p><ManifestEditor installed={selected} onManifest={async (manifest) => { await apiFetch(`${modulePath(selectedID).replace("/admin/", "/api/v1/admin/")}/manifest`, { method: "PUT", body: JSON.stringify({ manifest }) }); await load(); setMessage("模块已更新，无需重启。"); }} /></details>}<div className="module-remove"><h3>卸载模块</h3><p className="muted">{selected.manifest.runtime ? "停止并移除此模块程序，保留模块数据、已添加的媒体与观看记录。" : "移除此模块连接和连接凭证，保留已添加的媒体与观看记录。外部服务仍由原服务提供者管理。"}</p>{confirmRemove ? <div role="group" aria-label="确认卸载模块"><p>确定卸载“{selected.manifest.name}”吗？</p><div className="table-actions"><button className="ghost-button" disabled={busy} onClick={() => setConfirmRemove(false)}>取消</button><button className="primary-button" disabled={busy} onClick={() => void uninstall(selected)}>确认卸载</button></div></div> : <button className="ghost-button" onClick={() => setConfirmRemove(true)}>卸载模块</button>}</div></>}
      </section> : tab === "rankings" && hasRankings ? <ModuleRankingsPanel key={selectedID} moduleId={selectedID} enabled={selected.status === "active"} /> : <><ModuleForm key={selectedID} manifest={selected.manifest} installed={selected} onCancel={() => navigate("/admin/modules")} onSaved={async () => { await load(); setMessage("模块设置已保存，无需重启。"); }} />{!!selected.manifest.actions?.length && <ModuleActionsPanel key={selectedID} moduleId={selectedID} revision={selected.revision} actions={selected.manifest.actions} enabled={selected.status === "active"} onUpdated={async () => { await load(); }} />}</>}
    </section>;
  }
  const visibleItems = items.filter((item) => `${item.manifest.name} ${item.manifest.description || ""} ${typeLabel(item.manifest.type)}`.toLowerCase().includes(filter.trim().toLowerCase()) && (statusFilter === "all" || (statusFilter === "disabled" ? !item.enabled : statusFilter === "attention" ? needsAttention(item) : item.enabled && !needsAttention(item))));
  const attentionCount = items.filter(needsAttention).length;
  return <section className="content-card management-page module-center">
    <div className="relation-page-heading"><div><p className="eyebrow">扩展与设置</p><h1>模块管理</h1><p className="muted">每个模块的功能都在自己的页面中设置。安装、设置和启停后即时生效。</p></div><Link to="/settings/discover" className="ghost-button">搜索并添加内容</Link></div>
    <nav className="module-tabs" aria-label="模块列表"><Link to="/admin/modules" aria-current={!adding ? "page" : undefined}>已安装模块（{items.length}）</Link><Link to="/admin/modules?view=available" aria-current={adding ? "page" : undefined}>添加模块</Link></nav>
    {feedback}
    {editor}
    {adding ? <><h2>安装模块</h2><p className="muted">上传本地 ZIP 模块包，或填写下载网址。确认模块信息并保存设置后，JMlab 会自动启动模块。</p><section className="dashboard-panel"><ModulePackageImporter<PackagePreview> platform={platform} onPreview={acceptPackage} /></section><details className="module-advanced"><summary>连接外部服务（JSON 清单）</summary><p className="muted">已单独运行模块服务时，可导入 manifest.json 并设置服务地址。JMlab 不附带具体模块。</p><ManifestEditor onManifest={(manifest) => { setPackagePreview(null); setEditing(manifest); }} /></details></> : <><div className="module-summary" aria-label="模块概况"><span><strong>{items.length}</strong>已安装</span><span><strong>{items.filter((item) => item.enabled && !needsAttention(item)).length}</strong>可使用</span><span className={attentionCount ? "needs-attention" : ""}><strong>{attentionCount}</strong>需要处理</span></div><div className="module-list-toolbar"><label>查找模块<input type="search" value={filter} onChange={(e) => setFilter(e.target.value)} placeholder="模块名称或功能" /></label><label className="module-status-filter">模块状态<select value={statusFilter} onChange={(event) => setStatusFilter(event.target.value)}><option value="all">全部状态</option><option value="active">可使用</option><option value="attention">需要处理</option><option value="disabled">已停用</option></select></label><button className="ghost-button" disabled={busy} onClick={() => void scan()}>刷新模块状态</button></div><div className="management-card-grid">{visibleItems.map((item) => <article className="management-card module-card" key={item.manifest.id}><div className="management-card-heading"><div className="module-card-title"><span className="section-icon"><Icon name={item.manifest.type === "metadata" ? "library" : "module"} /></span><h2>{item.manifest.name}</h2></div><span className={!item.enabled ? "status-neutral" : needsAttention(item) ? "status-disabled" : "status-active"}>{statusLabel(item)}</span></div><p className="muted">{item.manifest.description || "查看此模块提供的设置和功能。"}</p><p className="muted">{typeLabel(item.manifest.type)} · {item.manifest.runtime ? "本地模块" : item.manifest.transport.type === "http" ? "外部模块" : "内置模块"} · {item.manifest.version}</p>{item.manifest.type !== "metadata" && <p className="muted">适用：{item.libraryScope?.mode === "selected" ? item.libraryScopeNames?.join("、") || "未选择媒体库" : "全部媒体库"}</p>}{item.lastError && <p className="form-error">{item.lastError}</p>}<div className="table-actions"><Link className="primary-button" to={modulePath(item.manifest.id)}><Icon name="settings" />设置</Link><button className="ghost-button" disabled={busy} onClick={() => void toggle(item)}>{item.enabled ? "停用" : "启用"}</button></div></article>)}</div>{visibleItems.length === 0 && <div className="empty-card"><h2>没有找到模块</h2><p className="muted">可以清空条件查看全部模块，或添加新的模块。</p><button type="button" className="ghost-button" onClick={() => { setFilter(""); setStatusFilter("all"); }}>清空筛选</button><Link to="/admin/modules?view=available" className="primary-button">添加模块</Link></div>}</>}
  </section>;
}

function ManifestEditor({ installed, onManifest }: { installed?: Installed; onManifest: (manifest: Manifest) => void | Promise<void> }) {
  const [source, setSource] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  async function accept(event: FormEvent) {
    event.preventDefault(); setError(""); setBusy(true);
    try {
      const value = JSON.parse(source) as Manifest;
      if (!value || typeof value !== "object" || !value.id || !value.name || !value.version || value.transport?.type !== "http") throw new Error("请选择有效的 HTTP 模块清单，需要包含名称、版本和服务地址。");
      if (value.runtime) throw new Error("本地模块请通过 ZIP 模块包安装或更新。");
      if (installed && value.id !== installed.manifest.id) throw new Error("更新时模块 ID 必须保持一致。");
      await onManifest(value); setSource("");
    } catch (e) { setError(e instanceof SyntaxError ? "清单不是有效的 JSON，请重新选择模块提供者的文件。" : errorText(e)); } finally { setBusy(false); }
  }
  return <form className="module-config-form" onSubmit={accept}><fieldset disabled={busy}><label>模块清单文件<input type="file" accept=".json,application/json" onChange={async (event) => { const file = event.target.files?.[0]; setError(""); if (!file) return; if (file.size > 1024 * 1024) { setError("清单文件不能超过 1 MB。"); return; } try { setSource(await file.text()); } catch { setError("无法读取文件，请重新选择。"); } }} /></label><label>清单内容<textarea rows={8} required value={source} onChange={(e) => setSource(e.target.value)} /></label>{error && <p className="form-error" role="alert">{error}</p>}<button className="ghost-button" disabled={!source.trim()}>{busy ? "检查中…" : installed ? "检查并更新模块" : "读取清单并设置"}</button></fieldset></form>;
}

function ModuleForm({ manifest, installed, packagePreview, onCancel, onSaved }: { manifest: Manifest; installed?: Installed; packagePreview?: PackagePreview; onCancel: () => void; onSaved: () => Promise<void> }) {
  const heading = useRef<HTMLHeadingElement>(null);
  useEffect(() => { if (packagePreview || !installed) heading.current?.focus(); }, []);
  const fields = Object.entries(manifest.configSchema || {});
  const external = manifest.transport.type === "http" && !manifest.runtime;
  const [config, setConfig] = useState<Record<string, unknown>>(() => Object.fromEntries(fields.map(([key, field]) => [key, fieldInitial(field, installed?.config[key])])));
  const [endpoint, setEndpoint] = useState(manifest.transport.url || "");
  const [token, setToken] = useState("");
  const [clearSecrets, setClearSecrets] = useState<string[]>([]);
  const [clearToken, setClearToken] = useState(false);
  const [enabled, setEnabled] = useState(true);
  const effectiveEnabled = installed?.enabled ?? enabled;
  const [origins, setOrigins] = useState((manifest.mediaAccess?.allowedOrigins || []).join("\n"));
  const [allowPrivate, setAllowPrivate] = useState(manifest.mediaAccess?.allowPrivate ?? false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  function change(key: string, value: unknown) {
    setConfig((current) => ({ ...current, [key]: value }));
  }
  async function save(event: FormEvent) {
    event.preventDefault(); setError(""); setBusy(true);
    try {
      const values = Object.fromEntries(fields.filter(([key]) => !clearSecrets.includes(key)).map(([key, field]) => [key, fieldValue(field, config[key])]));
      const transport = external ? { type: "http", url: endpoint.trim() } : undefined;
      const mediaAccess = manifest.transport.type === "http" && manifest.type !== "metadata" ? { allowedOrigins: origins.split(/[\n,]+/).map((value) => value.trim()).filter(Boolean), allowPrivate } : undefined;
      const path = packagePreview ? "/api/v1/admin/modules/packages/install" : installed ? `/api/v1/admin/modules/${encodeURIComponent(manifest.id)}` : "/api/v1/admin/modules";
      const body = packagePreview ? { packageToken: packagePreview.packageToken, moduleId: manifest.id, revision: packagePreview.revision, enabled: effectiveEnabled, config: values, clearSecrets, mediaAccess } : installed ? { config: values, token, clearSecrets, clearToken, transport, mediaAccess } : { manifest: { ...manifest, transport: transport || manifest.transport, mediaAccess: mediaAccess || manifest.mediaAccess }, enabled, config: values, token };
      await apiFetch(path, { method: installed && !packagePreview ? "PATCH" : "POST", body: JSON.stringify(body) });
      setToken(""); setClearSecrets([]); setClearToken(false); setConfig((current) => Object.fromEntries(fields.map(([key, field]) => [key, field.secret || field.type === "password" ? "" : current[key]]))); await onSaved();
    } catch (e) { setError(errorText(e)); } finally { setBusy(false); }
  }
  return <form className="module-config-form dashboard-panel" onSubmit={save}><h2 ref={heading} tabIndex={-1}>{installed ? "模块设置" : "安装并设置"}：{manifest.name}</h2><fieldset disabled={busy}>
    {packagePreview && <><p>{packagePreview.revision ? `将当前模块更新至 ${manifest.version}，保留已有设置。` : `准备安装 ${manifest.version}，适用于 ${manifest.runtime?.os}/${manifest.runtime?.arch}。`}</p><p className="module-notice">模块程序将以 JMlab 的系统账号运行，请仅安装可信来源的模块包。</p><details><summary>查看文件校验值</summary><p className="module-package-digest">SHA256：{packagePreview.sha256}</p><p className="muted">本次读取结果保留 15 分钟，过期后请重新导入。</p></details></>}
    {manifest.runtime && !packagePreview && <p className="muted">本地模块由 JMlab 管理启动和停止，无需填写服务地址或连接令牌。</p>}
    {external && <label>模块服务地址<input type="url" required value={endpoint} onChange={(e) => setEndpoint(e.target.value)} /><small>这是已启动的模块服务地址。</small></label>}
    {fields.map(([key, field]) => <label key={key}>{field.label}<ModuleFieldInput field={field} value={config[key]} required={effectiveEnabled && field.required && !installed?.configuredSecrets.includes(key)} disabled={clearSecrets.includes(key)} placeholder={installed?.configuredSecrets.includes(key) ? "已保存，留空保留" : undefined} onChange={(value) => change(key, value)} />{field.description && <small>{field.description}</small>}{installed?.configuredSecrets.includes(key) && <span className="inline-check"><input type="checkbox" checked={clearSecrets.includes(key)} onChange={(e) => setClearSecrets((current) => e.target.checked ? [...current, key] : current.filter((value) => value !== key))} />清除已保存的凭证</span>}</label>)}
    {external && <><label>模块连接令牌<input type="password" autoComplete="new-password" value={token} disabled={clearToken} placeholder={installed?.tokenConfigured ? "已保存，留空保留" : "模块未设置令牌时可留空"} onChange={(e) => setToken(e.target.value)} /></label>{installed?.tokenConfigured && <label className="inline-check"><input type="checkbox" checked={clearToken} onChange={(e) => setClearToken(e.target.checked)} />清除模块连接令牌</label>}</>}
    {manifest.transport.type === "http" && manifest.type !== "metadata" && <details open><summary>视频服务器授权</summary><p className="muted">填写模块将访问的视频服务器地址；如视频会跳转到其他服务器，请同时填写其地址。</p><label>允许的视频服务器（每行一个，包含协议和端口，不带路径）<textarea value={origins} onChange={(e) => setOrigins(e.target.value)} placeholder="https://media.example.com" /></label><label className="inline-check"><input type="checkbox" checked={allowPrivate} onChange={(e) => setAllowPrivate(e.target.checked)} />允许连接上面列出的局域网服务器</label></details>}
    {!installed && <label className="inline-check"><input type="checkbox" checked={enabled} onChange={(e) => setEnabled(e.target.checked)} />安装后启用</label>}
    {error && <p className="form-error" role="alert">{error}</p>}
    <div className="table-actions"><button className="primary-button">{busy ? "检查并保存中…" : packagePreview ? packagePreview.revision ? "更新模块" : "安装模块" : "保存设置"}</button><button type="button" className="ghost-button" onClick={onCancel}>取消</button></div>
  </fieldset></form>;
}
