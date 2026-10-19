import { useEffect, useRef, useState, type FormEvent } from "react";
import { Link } from "react-router-dom";
import { apiFetch, readSession, userHasRole } from "../lib/api";
import { type CatalogItem, type ItemType, type MediaChild, mediaTypeLabel } from "../lib/models";

type Source = { id: string; name: string; supportedItemTypes: ItemType[] };
type Result = { sourceId: string; title: string; itemType: ItemType; releaseDate?: string; originalTitle?: string; providerIds?: Record<string, string> };
const messageOf = (error: unknown) => error instanceof Error ? error.message : "暂时无法处理，请重试";

export function DiscoverPage() {
  const [sources, setSources] = useState<Source[]>([]);
  const [sourceId, setSourceId] = useState("");
  const [kind, setKind] = useState<ItemType>("movie");
  const [query, setQuery] = useState("");
  const [results, setResults] = useState<Result[] | null>(null);
  const [imported, setImported] = useState<Record<string, CatalogItem>>({});
  const [searchTerm, setSearchTerm] = useState("");
  const [page, setPage] = useState(1);
  const [total, setTotal] = useState(0);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");
  const allowed = userHasRole(readSession()?.user, "Admin", "Editor");
  const source = sources.find((item) => item.id === sourceId);
  const kinds = source?.supportedItemTypes.filter((value) => ["movie", "series", "video"].includes(value)) || [];
  useEffect(() => {
    if (!allowed) return;
    let cancelled = false;
    void apiFetch<{ items: Source[] }>("/api/v1/metadata/modules").then((response) => {
      if (cancelled) return;
      const items = response.items.filter((item) => item.supportedItemTypes.some((type) => ["movie", "series", "video"].includes(type)));
      setSources(items); setSourceId(items[0]?.id || ""); setKind(items[0]?.supportedItemTypes.find((type) => ["movie", "series", "video"].includes(type)) || "movie");
    }).catch((e) => { if (!cancelled) setError(messageOf(e)); }).finally(() => { if (!cancelled) setLoading(false); });
    return () => { cancelled = true; };
  }, [allowed]);
  async function search(nextPage = 1) {
    if (!query.trim() || !sourceId) return;
    setBusy(true); setError(""); setMessage("");
    try {
      const term = nextPage === 1 ? query.trim() : searchTerm;
      const response = await apiFetch<{ items: Result[]; total: number }>(`/api/v1/metadata/${encodeURIComponent(sourceId)}/search?${new URLSearchParams({ q: term, itemType: kind, page: String(nextPage) })}`);
      setResults(response.items || []); setTotal(response.total); setPage(nextPage); setSearchTerm(term);
    } catch (e) { setError(messageOf(e)); } finally { setBusy(false); }
  }
  async function add(result: Result) {
    setBusy(true); setError(""); setMessage("");
    try {
      const response = await apiFetch<{ item: CatalogItem; created: boolean }>(`/api/v1/metadata/${encodeURIComponent(sourceId)}/import`, { method: "POST", body: JSON.stringify({ sourceId: result.sourceId, providerIds: result.providerIds || {} }) });
      setImported((current) => ({ ...current, [`${sourceId}:${result.sourceId}`]: response.item }));
      setMessage(response.created ? `已添加“${response.item.title}”。${response.item.itemType === "series" ? "打开详情后可添加季和单集。" : "打开详情即可查找播放来源。"}` : `“${response.item.title}”已在媒体库中，信息已更新。`);
    } catch (e) { setError(messageOf(e)); } finally { setBusy(false); }
  }
  if (!allowed) return <section className="content-card"><h1>需要内容管理权限</h1><Link to="/">返回首页</Link></section>;
  return <section className="content-card management-page"><div className="relation-page-heading"><div><p className="eyebrow">添加内容</p><h1>搜索并添加</h1><p className="muted">先选择信息来源，再搜索片名。添加后会保留在媒体库，视频来源可随时补充。</p></div><div className="table-actions"><Link to="/settings" className="ghost-button">返回设置</Link>{userHasRole(readSession()?.user, "Admin") && <Link to="/admin/modules" className="ghost-button">模块管理</Link>}</div></div>
    {loading ? <p role="status">正在读取来源…</p> : sources.length === 0 ? <div className="empty-card"><h2>还没有可用的信息来源</h2><p>请先连接并启用内容来源。</p>{userHasRole(readSession()?.user, "Admin") && <Link to="/admin/modules" className="primary-button">模块管理</Link>}</div> : <form className="discovery-search" onSubmit={(e: FormEvent) => { e.preventDefault(); void search(); }}><fieldset disabled={busy}>
      <label>信息来源<select value={sourceId} onChange={(e) => { const next = sources.find((value) => value.id === e.target.value); setSourceId(e.target.value); setKind(next?.supportedItemTypes.find((value) => ["movie", "series", "video"].includes(value)) || "movie"); setResults(null); setMessage(""); }} >{sources.map((item) => <option value={item.id} key={item.id}>{item.name}</option>)}</select></label>
      <label>内容类型<select value={kind} onChange={(e) => { setKind(e.target.value as ItemType); setResults(null); }}>{kinds.map((type) => <option value={type} key={type}>{mediaTypeLabel(type)}</option>)}</select></label>
      <label className="discovery-query">片名或关键词<input required maxLength={200} value={query} onChange={(e) => setQuery(e.target.value)} placeholder="输入片名开始搜索" /></label><button className="primary-button">{busy ? "处理中…" : "搜索来源"}</button>
    </fieldset></form>}
    {error && <p className="form-error" role="alert">{error}</p>}{message && <p className="form-success" role="status">{message}</p>}
    {results && <><h2>搜索结果{total > 0 ? ` · ${total} 项` : ""}</h2>{results.length === 0 && <p>没有找到相关内容，试试其他片名或来源。</p>}<div className="discovery-results">{results.map((result) => { const added = imported[`${sourceId}:${result.sourceId}`]; return <article className="media-list-row" key={result.sourceId}><div><h3>{result.title}</h3><p className="muted">{mediaTypeLabel(result.itemType)}{result.releaseDate ? ` · ${result.releaseDate}` : ""}{result.originalTitle && result.originalTitle !== result.title ? ` · ${result.originalTitle}` : ""}</p></div>{added ? <Link to={`/items/${encodeURIComponent(added.id)}`} className="ghost-button">打开详情</Link> : <button type="button" className="primary-button" disabled={busy} onClick={() => void add(result)}>添加到媒体库</button>}</article>; })}</div><div className="table-actions"><button className="ghost-button" disabled={busy || page <= 1} onClick={() => void search(page - 1)}>上一页</button><span>第 {page} 页</span><button className="ghost-button" disabled={busy || page * 20 >= total} onClick={() => void search(page + 1)}>下一页</button></div></>}
  </section>;
}

export function MediaChildren({ item }: { item: CatalogItem }) {
  const [items, setItems] = useState<CatalogItem[]>([]);
  const [cursor, setCursor] = useState("");
  const [busy, setBusy] = useState(false);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");
  const [added, setAdded] = useState<Record<string, CatalogItem>>({});
  const [bulk, setBulk] = useState(false);
  const stopAdding = useRef(false);
  const canManage = userHasRole(readSession()?.user, "Admin", "Editor");
  const references = Array.isArray(item.extraAttributes?.children) ? item.extraAttributes.children : [];
  const children = references.filter((child) => child && typeof child.sourceId === "string" && typeof child.title === "string" && (child.itemType === "season" || child.itemType === "episode") && Number.isInteger(child.indexNumber));
  async function load(next = "") {
    const result = await apiFetch<{ items: CatalogItem[]; nextCursor: string }>(`/api/v1/items?${new URLSearchParams({ parentId: item.id, limit: "100", cursor: next })}`);
    setItems((current) => next ? [...current, ...result.items] : result.items); setCursor(result.nextCursor || "");
  }
  useEffect(() => { setItems([]); setAdded({}); setError(""); setLoading(true); void load().catch((e) => setError(messageOf(e))).finally(() => setLoading(false)); }, [item.id]);
  async function add(child: MediaChild) {
    setBusy(true); setError("");
    try {
      const response = await apiFetch<{ item: CatalogItem }>(`/api/v1/metadata/${encodeURIComponent(item.metadataModuleId || "")}/import`, { method: "POST", body: JSON.stringify({ sourceId: child.sourceId, parentId: item.id, indexNumber: child.indexNumber }) });
      setAdded((current) => ({ ...current, [child.sourceId]: response.item }));
      setMessage(`已添加“${child.title}”。`); await load();
    } catch (e) { setError(messageOf(e)); } finally { setBusy(false); }
  }
  async function addAll() {
    const pending = children.filter((child) => !added[child.sourceId] && !items.some((value) => value.itemType === child.itemType && value.indexNumber === child.indexNumber));
    stopAdding.current = false; setBusy(true); setBulk(true); setError("");
    let count = 0;
    try {
      for (const child of pending) {
        if (stopAdding.current) break;
        setMessage(`正在添加 ${count + 1}/${pending.length}：${child.title}`);
        const result = await apiFetch<{ item: CatalogItem }>(`/api/v1/metadata/${encodeURIComponent(item.metadataModuleId || "")}/import`, { method: "POST", body: JSON.stringify({ sourceId: child.sourceId, parentId: item.id, indexNumber: child.indexNumber }) });
        setAdded((current) => ({ ...current, [child.sourceId]: result.item })); count++;
      }
      setMessage(`${stopAdding.current ? "已停止，" : "添加完成，"}本次已添加 ${count} 项。`);
    } catch (e) { setError(messageOf(e)); setMessage(`本次已添加 ${count} 项，剩余内容可以再次添加。`); }
    finally { await load().catch((e) => setError(messageOf(e))); setBusy(false); setBulk(false); }
  }
  useEffect(() => () => { stopAdding.current = true; }, []);
  return <section className="content-card" id="episodes"><h2>{item.itemType === "series" ? "选择季" : "选择单集"}</h2>
    {item.parentId && <Link to={`/items/${encodeURIComponent(item.parentId)}`} className="ghost-button">返回上一级</Link>}
    {error && <p className="form-error" role="alert">{error}</p>}{message && <p role="status" className="form-success">{message}</p>}
    {loading ? <p role="status">正在读取内容…</p> : items.length === 0 && <p className="muted">暂未添加{item.itemType === "series" ? "季" : "单集"}。{canManage ? "请从下方选择添加。" : "请联系管理员添加内容。"}</p>}
    <div className="episode-grid">{items.slice().sort((a, b) => (a.indexNumber ?? 0) - (b.indexNumber ?? 0)).map((child) => <Link className="episode-card" to={`/items/${encodeURIComponent(child.id)}`} key={child.id}><small>{mediaTypeLabel(child.itemType)} {child.indexNumber ?? ""}</small><strong>{child.title}</strong></Link>)}</div>
    {cursor && <button className="ghost-button" disabled={busy} onClick={() => { setBusy(true); void load(cursor).catch((e) => setError(messageOf(e))).finally(() => setBusy(false)); }}>查看更多</button>}
    {canManage && children.length > 0 && <details className="child-import"><summary>从来源添加{item.itemType === "series" ? "季" : "单集"}</summary><div className="table-actions"><button className="primary-button" disabled={busy} onClick={() => void addAll()}>添加全部{item.itemType === "series" ? "季" : "单集"}</button>{bulk && <button className="ghost-button" onClick={() => { stopAdding.current = true; }}>停止添加</button>}</div>{children.map((child) => { const existing = added[child.sourceId] || items.find((value) => value.itemType === child.itemType && value.indexNumber === child.indexNumber); return <div className="media-list-row" key={child.sourceId}><span>{child.title}</span>{existing ? <Link className="ghost-button" to={`/items/${encodeURIComponent(existing.id)}`}>打开详情</Link> : <button className="ghost-button" disabled={busy} onClick={() => void add(child)}>添加</button>}</div>; })}</details>}
  </section>;
}
