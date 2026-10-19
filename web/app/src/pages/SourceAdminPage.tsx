import { useEffect, useRef, useState, type FormEvent } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { apiFetch, readSession, userHasRole } from "../lib/api";
import { type CatalogItem, type PlaybackSourceRecord, mediaTypeLabel, visibleCode } from "../lib/models";

type SourceForm = {
  providerId: string; label: string; protocol: PlaybackSourceRecord["protocol"];
  quality: string; language: string; candidateUrl: string; resolvedUrl: string;
  expiresAt: string; sortOrder: string; isPreview: boolean; enabled: boolean;
};
const emptyForm: SourceForm = { providerId: "", label: "", protocol: "hls", quality: "", language: "", candidateUrl: "", resolvedUrl: "", expiresAt: "", sortOrder: "0", isPreview: false, enabled: true };
const errorText = (error: unknown) => error instanceof Error ? error.message : "暂时无法处理，请重试";
const statusText = { active: "可用", unavailable: "暂不可用", expired: "等待刷新" };
const playableItem = (item: CatalogItem) => item.itemType !== "series" && item.itemType !== "season";

export function SourceAdminPage() {
  const canManageModules = userHasRole(readSession()?.user, "Admin");
  const [params, setParams] = useSearchParams();
  const selectedID = params.get("itemId") || "";
  const selectedRef = useRef(selectedID);
  selectedRef.current = selectedID;
  const [query, setQuery] = useState("");
  const [results, setResults] = useState<CatalogItem[] | null>(null);
  const [searching, setSearching] = useState(false);
  const [searchError, setSearchError] = useState("");
  const searchRequest = useRef<AbortController | null>(null);
  const sourceRequest = useRef<AbortController | null>(null);
  const [manualID, setManualID] = useState("");
  const [item, setItem] = useState<CatalogItem | null>(null);
  const [sources, setSources] = useState<PlaybackSourceRecord[]>([]);
  const [form, setForm] = useState<SourceForm>(emptyForm);
  const [editing, setEditing] = useState<PlaybackSourceRecord | null>(null);
  const [editorOpen, setEditorOpen] = useState(false);
  const [loading, setLoading] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");

  useEffect(() => {
    return () => { searchRequest.current?.abort(); sourceRequest.current?.abort(); };
  }, []);

  async function search(event: FormEvent) {
    event.preventDefault();
    if (!query.trim()) return;
    searchRequest.current?.abort();
    const controller = new AbortController(); searchRequest.current = controller;
    setSearching(true); setSearchError(""); setResults(null);
    try {
      const result = await apiFetch<{ items: CatalogItem[] }>(`/api/v1/items?${new URLSearchParams({ q: query.trim(), limit: "20", sort: "title" })}`, { signal: controller.signal });
      if (!controller.signal.aborted) setResults(result.items);
    } catch (e) { if (!controller.signal.aborted) setSearchError(errorText(e)); }
    finally { if (!controller.signal.aborted) setSearching(false); }
  }

  async function loadSources(value: string, preserveMessage = false) {
    sourceRequest.current?.abort();
    const controller = new AbortController(); sourceRequest.current = controller;
    setLoading(true); setError(""); if (!preserveMessage) setMessage("");
    try {
      const [nextItem, response] = await Promise.all([
        apiFetch<CatalogItem>(`/api/v1/items/${encodeURIComponent(value)}`, { signal: controller.signal }),
        apiFetch<{ items: PlaybackSourceRecord[] }>(`/api/v1/admin/items/${encodeURIComponent(value)}/sources`, { signal: controller.signal }),
      ]);
      if (controller.signal.aborted || selectedRef.current !== value) return;
      setItem(nextItem); setSources(response.items);
    } catch (e) { if (!controller.signal.aborted) setError(errorText(e)); }
    finally { if (!controller.signal.aborted) setLoading(false); }
  }

  useEffect(() => {
    setItem(null); setSources([]); setEditorOpen(false); setEditing(null); setError(""); setMessage("");
    if (selectedID) { setManualID(selectedID); void loadSources(selectedID); }
    else { sourceRequest.current?.abort(); setLoading(false); }
    return () => sourceRequest.current?.abort();
  }, [selectedID]);

  function chooseItem(id: string) {
    setParams({ itemId: id });
    if (selectedID === id) void loadSources(id);
  }

  function updateForm<K extends keyof SourceForm>(key: K, value: SourceForm[K]) {
    setForm((current) => ({ ...current, [key]: value }));
  }
  function startEdit(source: PlaybackSourceRecord) {
    setEditing(source); setEditorOpen(true);
    setForm({ providerId: source.providerId, label: source.label, protocol: source.protocol, quality: source.quality || "", language: source.language || "", candidateUrl: "", resolvedUrl: "", expiresAt: source.expiresAt || "", sortOrder: String(source.sortOrder), isPreview: source.isPreview, enabled: source.enabled });
  }

  async function mutate(path: string, init: RequestInit, success: string | ((result: { healthy?: boolean; summary?: { healthy: number; total: number } }) => string)) {
    const id = item?.id;
    if (!id || busy) return;
    setBusy(true); setError(""); setMessage("");
    try {
      const response = await apiFetch<{ healthy?: boolean; summary?: { healthy: number; total: number } }>(`/api/v1/admin/items/${encodeURIComponent(id)}/sources${path}`, init);
      if (selectedRef.current !== id) return;
      setMessage(typeof success === "string" ? success : success(response));
      setEditorOpen(false); setEditing(null);
      await loadSources(id, true);
    } catch (e) { if (selectedRef.current === id) setError(errorText(e)); }
    finally { setBusy(false); }
  }

  async function saveSource(event: FormEvent) {
    event.preventDefault();
    const payload: Record<string, unknown> = { label: form.label.trim(), quality: form.quality.trim(), language: form.language.trim(), sortOrder: Number(form.sortOrder) || 0, isPreview: form.isPreview, enabled: form.enabled };

    await mutate(editing ? `/${encodeURIComponent(editing.id)}` : "", { method: editing ? "PATCH" : "POST", body: JSON.stringify(payload) }, editing ? "播放源已更新" : "播放源已绑定");
  }

  function probe(source?: PlaybackSourceRecord) {
    void mutate(source ? `/${encodeURIComponent(source.id)}/probe` : "/probe", { method: "POST", body: source ? undefined : "{}" }, (response) => response.summary ? `批量探测完成：${response.summary.healthy}/${response.summary.total} 成功` : response.healthy ? "探测成功" : "探测失败，请检查来源连接");
  }
  const sortedSources = [...sources].sort((a, b) => a.sortOrder - b.sortOrder || a.id.localeCompare(b.id));


  return <section className="content-card management-page">
    <div className="relation-page-heading"><div><p className="eyebrow">视频来源</p><h1>管理作品播放源</h1><p className="muted">输入片名选择作品，再检查视频、调整顺序或停用来源。也可以从作品详情直接进入。</p></div><Link to="/settings" className="ghost-button">返回设置</Link></div>
    <form className="search-row" onSubmit={search}><label className="source-search-field">查找作品<input required maxLength={200} value={query} onChange={(event) => setQuery(event.target.value)} placeholder="输入片名或内容标识" /></label><button className="primary-button" disabled={busy || searching}>{searching ? "查找中…" : "查找作品"}</button></form>
    {searchError && <p className="form-error" role="alert">{searchError}</p>}
    {results && <div className="discovery-results" aria-label="作品查找结果">{results.length === 0 ? <p className="muted">没有找到作品，请换一个片名，或先到“添加内容”入库。</p> : <><p className="muted">最多显示 20 项；结果过多时请补充片名。</p>{results.map((value) => <article className="media-list-row" key={value.id}><div><strong>{value.title}</strong><p className="muted">{mediaTypeLabel(value.itemType)}{value.year ? ` · ${value.year}` : ""}{visibleCode(value) ? ` · ${visibleCode(value)}` : ""}</p></div>{playableItem(value) ? <button className="ghost-button" disabled={busy || loading} onClick={() => chooseItem(value.id)}>管理播放源</button> : <Link className="ghost-button" to={`/items/${encodeURIComponent(value.id)}`}>选择季与单集</Link>}</article>)}</>}</div>}
    <details className="source-advanced-entry"><summary>按作品 ID 打开（高级）</summary><form className="search-row" onSubmit={(event) => { event.preventDefault(); if (manualID.trim()) chooseItem(manualID.trim()); }}><label>作品 ID<input required value={manualID} onChange={(event) => setManualID(event.target.value)} placeholder="作品 ID，例如 item_xxx" /></label><button className="ghost-button" disabled={busy || loading}>加载作品源</button></form></details>
    {error && <div role="alert"><p className="form-error">{error}</p>{selectedID && <button className="ghost-button" disabled={loading || busy} onClick={() => void loadSources(selectedID)}>重新读取</button>}</div>}
    {message && <p className="form-success" role="status">{message}</p>}
    {loading && <p role="status">正在读取作品和视频来源…</p>}
    {!selectedID && <div className="empty-card"><h2>先选择一部作品</h2><p className="muted">上方按片名查找，或者从媒体库打开作品详情后点击“管理播放源”。</p><Link to="/libraries" className="ghost-button">打开媒体库</Link></div>}
    {item && <>
      <div className="relation-page-heading"><div><h2>{item.title}</h2><p className="muted">{mediaTypeLabel(item.itemType)} · {sources.length} 个来源</p></div><Link to={`/items/${encodeURIComponent(item.id)}`} className="ghost-button">返回作品详情</Link></div>
      {!playableItem(item) ? <p className="muted">请先打开详情，选择具体单集后管理播放源。</p> : <>
        <div className="table-actions"><button className="ghost-button" disabled={busy || loading} onClick={() => probe()}>批量探测</button><button className="ghost-button" disabled={busy || loading} onClick={() => void loadSources(item.id)}>刷新列表</button></div>
        {sources.length === 0 && !loading ? <div className="empty-card"><h3>还没有找到视频来源</h3><p className="muted">先在“模块管理”中配置视频播放模块，再打开作品详情查找视频。作品信息和观看记录会继续保留。</p>{canManageModules && <Link to="/admin/modules" className="primary-button">检查来源设置</Link>}</div> : <div className="table-wrap"><table className="management-table source-management-table"><thead><tr><th>视频</th><th>来源</th><th>状态</th><th>最近解析</th><th>探测</th><th>操作</th></tr></thead><tbody>{sortedSources.map((source) => <tr key={source.id}><td><strong>{source.label}</strong><small>{source.isPreview ? "预告片" : "正片"} · {source.quality || "画质未知"}{source.language ? ` · ${source.language}` : ""}</small></td><td>{source.moduleName || source.providerName || "视频来源"}</td><td><span className={source.enabled && source.status === "active" ? "status-active" : "status-disabled"}>{source.enabled ? statusText[source.status] : "已停用"}</span>{source.lastError && <small>{source.lastError}</small>}</td><td>{source.lastResolvedAt ? new Date(source.lastResolvedAt).toLocaleString("zh-CN") : "尚未解析"}<small>{source.expiresAt ? `有效至 ${new Date(source.expiresAt).toLocaleString("zh-CN")}` : "播放时检查"}</small></td><td>{source.probeCount ? `${source.probeSuccessCount}/${source.probeCount} 成功` : "尚未探测"}{source.lastProbeError && <small>{source.lastProbeError}</small>}</td><td><div className="table-actions"><button className="ghost-button compact-button" disabled={busy || loading} onClick={() => probe(source)}>探测</button><button className="ghost-button compact-button" disabled={busy || loading} onClick={() => startEdit(source)}>编辑</button><button className="ghost-button compact-button" disabled={busy || loading} onClick={() => void mutate(`/${encodeURIComponent(source.id)}/${source.enabled ? "disable" : "enable"}`, { method: "POST" }, source.enabled ? "已停用播放源" : "已恢复播放源")}>{source.enabled ? "停用" : "恢复"}</button></div></td></tr>)}</tbody></table></div>}
      </>}
    </>}
    {item && editorOpen && <form className="relation-editor source-editor" onSubmit={saveSource}><h2>{editing ? `编辑：${editing.label}` : "绑定播放源"}</h2><fieldset disabled={busy || loading}>
      <p className="muted">播放地址由相关模块管理。这里可以调整名称、排序和启停；来源连接和规则在模块中设置。</p>
      <label>名称<input required value={form.label} onChange={(event) => updateForm("label", event.target.value)} /></label><label>画质<input value={form.quality} placeholder="例如 1080p" onChange={(event) => updateForm("quality", event.target.value)} /></label><label>语言<input value={form.language} placeholder="例如 中文字幕" onChange={(event) => updateForm("language", event.target.value)} /></label>

      <label>排序（数字越小越靠前）<input type="number" value={form.sortOrder} onChange={(event) => updateForm("sortOrder", event.target.value)} /></label><label className="inline-check"><input type="checkbox" checked={form.isPreview} onChange={(event) => updateForm("isPreview", event.target.checked)} />这是预告片</label><label className="inline-check"><input type="checkbox" checked={form.enabled} onChange={(event) => updateForm("enabled", event.target.checked)} />启用播放源</label>
      <div className="table-actions"><button className="primary-button">{busy ? "保存中…" : editing ? "保存修改" : "绑定播放源"}</button><button type="button" className="ghost-button" onClick={() => setEditorOpen(false)}>取消</button></div>
    </fieldset></form>}
  </section>;
}
