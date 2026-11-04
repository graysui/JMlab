import { useEffect, useRef, useState, type FormEvent } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { apiFetch, readSession, userHasRole } from "../lib/api";
import { mediaTypeLabel, type CatalogItem, type ItemType } from "../lib/models";

type Source = { id: string; name: string; supportedItemTypes: ItemType[] };
type Result = { sourceId: string; title: string; originalTitle?: string; releaseDate?: string; itemType: ItemType; providerIds?: Record<string, string> };

const searchableTypes: ItemType[] = ["movie", "series", "video"];
const messageOf = (error: unknown) => error instanceof Error ? error.message : "操作失败，请重试";
const pageFrom = (value: string | null) => Math.max(1, Number.isInteger(Number(value)) && Number(value) > 0 ? Number(value) : 1);

export function DiscoverPage() {
  const [searchParams, setSearchParams] = useSearchParams();
  const moduleParam = searchParams.get("moduleId") ?? "";
  const typeParam = searchParams.get("type") ?? "";
  const queryParam = searchParams.get("q") ?? "";
  const pageParam = pageFrom(searchParams.get("page"));
  const [sources, setSources] = useState<Source[]>([]);
  const [sourceId, setSourceId] = useState("");
  const [kind, setKind] = useState<ItemType>("movie");
  const [query, setQuery] = useState(queryParam);
  const [results, setResults] = useState<Result[] | null>(null);
  const [searchTerm, setSearchTerm] = useState(queryParam);
  const [page, setPage] = useState(pageParam);
  const [total, setTotal] = useState(0);
  const [imported, setImported] = useState<Record<string, CatalogItem>>({});
  const [lastImported, setLastImported] = useState<CatalogItem | null>(null);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");
  const [selectionWarning, setSelectionWarning] = useState("");
  const user = readSession()?.user;
  const canManage = userHasRole(user, "Admin", "Editor");
  const isAdmin = userHasRole(user, "Admin");
  const source = sources.find((item) => item.id === sourceId);
  const kinds = source?.supportedItemTypes.filter((value) => searchableTypes.includes(value)) ?? [];

  useEffect(() => {
    if (!canManage) return;
    let active = true;
    void (async () => {
      try {
        const response = await apiFetch<{ items: Source[] }>("/api/v1/metadata/modules");
        if (!active) return;
        const items = response.items.filter((item) => item.supportedItemTypes.some((type) => searchableTypes.includes(type)));
        setSources(items);
        const requestedSource = moduleParam ? items.find((item) => item.id === moduleParam) : undefined;
        const initialSource = requestedSource ?? (!moduleParam ? items[0] : undefined);
        if (moduleParam && !requestedSource) {
          setSelectionWarning("原选择的信息模块当前不可用。请选择一个可用来源后重新搜索；不会自动导入内容。");
          setSourceId("");
          return;
        }
        if (!initialSource) return;
        const supportedKinds = initialSource.supportedItemTypes.filter((value) => searchableTypes.includes(value));
        const requestedType = searchableTypes.includes(typeParam as ItemType) && supportedKinds.includes(typeParam as ItemType) ? typeParam as ItemType : undefined;
        const initialKind = requestedType ?? supportedKinds[0] ?? "movie";
        setSourceId(initialSource.id);
        setKind(initialKind);
        setQuery(queryParam);
        setSearchTerm(queryParam);
        setPage(pageParam);
        if (queryParam.trim()) {
          const params = new URLSearchParams({ q: queryParam.trim(), itemType: initialKind, page: String(pageParam) });
          const result = await apiFetch<{ items: Result[]; total: number }>(`/api/v1/metadata/${encodeURIComponent(initialSource.id)}/search?${params}`);
          if (active) { setResults(result.items || []); setTotal(result.total); }
        }
        if (typeParam && !requestedType) setSelectionWarning("原内容类型不适用于当前模块，已选择该模块的首个可用类型。");
      } catch (requestError) {
        if (active) setError(messageOf(requestError));
      } finally {
        if (active) setLoading(false);
      }
    })();
    return () => { active = false; };
  }, [canManage]);

  function writeAddress(next: { moduleId: string; type: ItemType; query: string; page: number }) {
    const params = new URLSearchParams();
    if (next.moduleId) params.set("moduleId", next.moduleId);
    if (next.type) params.set("type", next.type);
    if (next.query.trim()) params.set("q", next.query.trim());
    if (next.page > 1) params.set("page", String(next.page));
    setSearchParams(params, { replace: false });
  }

  async function search(nextPage = 1, nextSource = sourceId, nextKind = kind, nextQuery = query) {
    if (!nextQuery.trim() || !nextSource) return;
    setBusy(true);
    setError("");
    setMessage("");
    setLastImported(null);
    setResults(null);
    const term = nextQuery.trim();
    setSearchTerm(term);
    setPage(nextPage);
    writeAddress({ moduleId: nextSource, type: nextKind, query: term, page: nextPage });
    try {
      const params = new URLSearchParams({ q: term, itemType: nextKind, page: String(nextPage) });
      const response = await apiFetch<{ items: Result[]; total: number }>(`/api/v1/metadata/${encodeURIComponent(nextSource)}/search?${params}`);
      setResults(response.items || []);
      setTotal(response.total);
      setPage(nextPage);
    } catch (requestError) {
      setError(messageOf(requestError));
    } finally {
      setBusy(false);
    }
  }

  async function add(result: Result) {
    setBusy(true);
    setError("");
    setMessage("");
    try {
      const response = await apiFetch<{ item: CatalogItem; created: boolean }>(`/api/v1/metadata/${encodeURIComponent(sourceId)}/import`, { method: "POST", body: JSON.stringify({ sourceId: result.sourceId, providerIds: result.providerIds || {} }) });
      setImported((current) => ({ ...current, [`${sourceId}:${result.sourceId}`]: response.item }));
      setLastImported(response.item);
      setMessage(response.created ? `已添加“${response.item.title}”。` : `“${response.item.title}”已在媒体库中，信息已更新。`);
    } catch (requestError) {
      setError(messageOf(requestError));
    } finally {
      setBusy(false);
    }
  }

  if (!canManage) return <section className="content-card"><h1>需要内容管理权限</h1><Link to="/browse">返回浏览</Link></section>;

  return <section className="management-page discover-page">
    <div className="page-heading"><div><p className="eyebrow">内容管理</p><h1>添加内容</h1><p className="muted">搜索信息模块并把内容信息加入目录。导入不会下载视频，也不会自动创建媒体库。</p></div><div className="table-actions"><Link to="/manage/libraries" className="ghost-button">媒体库配置</Link>{isAdmin && <Link to="/manage/modules" className="ghost-button">模块管理</Link>}</div></div>
    {loading ? <p role="status">正在读取信息来源…</p> : sources.length === 0 ? <div className="empty-card"><h2>还没有可用的信息来源</h2><p className="muted">请管理员安装并启用支持搜索的内容信息模块。</p>{isAdmin && <Link to="/manage/modules" className="primary-button">管理模块</Link>}</div> : <form className="discovery-search" onSubmit={(event: FormEvent) => { event.preventDefault(); void search(1); }}>
      <fieldset disabled={busy}>
        <label>信息来源<select value={sourceId} onChange={(event) => { const nextId = event.target.value; const nextSource = sources.find((item) => item.id === nextId); const nextKind = nextSource?.supportedItemTypes.find((value) => searchableTypes.includes(value)) ?? "movie"; setSourceId(nextId); setKind(nextKind); setResults(null); setSelectionWarning(""); setMessage(""); writeAddress({ moduleId: nextId, type: nextKind, query, page: 1 }); }}><option value="">请选择来源模块</option>{sources.map((item) => <option value={item.id} key={item.id}>{item.name}</option>)}</select></label>
        <label>内容类型<select value={kind} disabled={!sourceId} onChange={(event) => { const nextKind = event.target.value as ItemType; setKind(nextKind); setResults(null); setMessage(""); writeAddress({ moduleId: sourceId, type: nextKind, query, page: 1 }); }}>{kinds.map((type) => <option value={type} key={type}>{mediaTypeLabel(type)}</option>)}</select></label>
        <label className="discovery-query">片名或关键词<input maxLength={200} value={query} onChange={(event) => setQuery(event.target.value)} placeholder="输入片名开始搜索" /></label>
        <button className="primary-button" disabled={!sourceId || !query.trim()}>{busy ? "处理中…" : "搜索来源"}</button>
      </fieldset>
    </form>}
    {selectionWarning && <p className="module-notice" role="status">{selectionWarning}</p>}
    {error && <p className="form-error" role="alert">{error}</p>}
    {message && <p className="form-success" role="status">{message}</p>}
    {lastImported && <div className="discovery-next-steps"><strong>下一步</strong><Link className="ghost-button" to={`/items/${encodeURIComponent(lastImported.id)}`}>查看作品详情</Link><Link className="ghost-button" to="/manage/libraries">检查媒体库筛选</Link><span className="muted">内容入库不代表已匹配视频来源或用户授权。</span></div>}
    {results && <>
      <div className="section-heading"><div><p className="eyebrow">{source?.name || "来源"}</p><h2>搜索结果{total > 0 ? ` · ${total} 项` : ""}</h2></div></div>
      {results.length === 0 ? <p className="muted">没有找到相关内容，试试其他片名或来源。</p> : <div className="discovery-results">{results.map((result) => { const added = imported[`${sourceId}:${result.sourceId}`]; return <article className="media-list-row" key={result.sourceId}><div><h3>{result.title}</h3><p className="muted">{mediaTypeLabel(result.itemType)}{result.releaseDate ? ` · ${result.releaseDate}` : ""}{result.originalTitle && result.originalTitle !== result.title ? ` · ${result.originalTitle}` : ""}</p></div>{added ? <Link to={`/items/${encodeURIComponent(added.id)}`} className="ghost-button">打开详情</Link> : <button type="button" className="primary-button" disabled={busy} onClick={() => void add(result)}>添加到目录</button>}</article>; })}</div>}
      <div className="table-actions"><button type="button" className="ghost-button" disabled={busy || page <= 1} onClick={() => void search(page - 1, sourceId, kind, searchTerm)}>上一页</button><span>第 {page} 页</span><button type="button" className="ghost-button" disabled={busy || page * 20 >= total} onClick={() => void search(page + 1, sourceId, kind, searchTerm)}>下一页</button></div>
    </>}
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
    setItems((current) => next ? [...current, ...result.items] : result.items);
    setCursor(result.nextCursor || "");
  }

  useEffect(() => { setItems([]); setAdded({}); setError(""); setLoading(true); void load().catch((loadError) => setError(messageOf(loadError))).finally(() => setLoading(false)); }, [item.id]);

  async function add(child: { sourceId: string; title: string; itemType: string; indexNumber: number }) {
    setBusy(true);
    setError("");
    try {
      const response = await apiFetch<{ item: CatalogItem }>(`/api/v1/metadata/${encodeURIComponent(item.metadataModuleId || "")}/import`, { method: "POST", body: JSON.stringify({ sourceId: child.sourceId, parentId: item.id, indexNumber: child.indexNumber }) });
      setAdded((current) => ({ ...current, [child.sourceId]: response.item }));
      setMessage(`已添加“${child.title}”。`);
      await load();
    } catch (requestError) { setError(messageOf(requestError)); }
    finally { setBusy(false); }
  }

  async function addAll() {
    const pending = children.filter((child) => !added[child.sourceId] && !items.some((value) => value.itemType === child.itemType && value.indexNumber === child.indexNumber));
    stopAdding.current = false;
    setBusy(true);
    setBulk(true);
    setError("");
    let count = 0;
    try {
      for (const child of pending) {
        if (stopAdding.current) break;
        setMessage(`正在添加 ${count + 1}/${pending.length}：${child.title}`);
        const result = await apiFetch<{ item: CatalogItem }>(`/api/v1/metadata/${encodeURIComponent(item.metadataModuleId || "")}/import`, { method: "POST", body: JSON.stringify({ sourceId: child.sourceId, parentId: item.id, indexNumber: child.indexNumber }) });
        setAdded((current) => ({ ...current, [child.sourceId]: result.item }));
        count++;
      }
      setMessage(`${stopAdding.current ? "已停止，" : "添加完成，"}本次已添加 ${count} 项。`);
    } catch (requestError) { setError(messageOf(requestError)); setMessage(`本次已添加 ${count} 项，剩余内容可以再次添加。`); }
    finally { await load().catch((loadError) => setError(messageOf(loadError))); setBusy(false); setBulk(false); }
  }

  return <section className="content-card" id="episodes"><h2>{item.itemType === "series" ? "选择季" : "选择单集"}</h2>
    {item.parentId && <Link to={`/items/${encodeURIComponent(item.parentId)}`} className="ghost-button">返回上一级</Link>}
    {error && <p className="form-error" role="alert">{error}</p>}{message && <p role="status" className="form-success">{message}</p>}
    {loading ? <p role="status">正在读取内容…</p> : items.length === 0 && <p className="muted">暂未添加{item.itemType === "series" ? "季" : "单集"}。{canManage ? "请从下方选择添加。" : "请联系管理员添加内容。"}</p>}
    <div className="episode-grid">{items.slice().sort((a, b) => (a.indexNumber ?? 0) - (b.indexNumber ?? 0)).map((child) => <Link className="episode-card" to={`/items/${encodeURIComponent(child.id)}`} key={child.id}><small>{mediaTypeLabel(child.itemType)} {child.indexNumber ?? ""}</small><strong>{child.title}</strong></Link>)}</div>
    {cursor && <button className="ghost-button" disabled={busy} onClick={() => { setBusy(true); void load(cursor).catch((loadError) => setError(messageOf(loadError))).finally(() => setBusy(false)); }}>查看更多</button>}
    {canManage && children.length > 0 && <details className="child-import"><summary>从来源添加{item.itemType === "series" ? "季" : "单集"}</summary><div className="table-actions"><button className="primary-button" disabled={busy} onClick={() => void addAll()}>添加全部{item.itemType === "series" ? "季" : "单集"}</button>{bulk && <button className="ghost-button" onClick={() => { stopAdding.current = true; }}>停止添加</button>}</div>{children.map((child) => { const existing = added[child.sourceId] || items.find((value) => value.itemType === child.itemType && value.indexNumber === child.indexNumber); return <div className="media-list-row" key={child.sourceId}><span>{child.title}</span>{existing ? <Link className="ghost-button" to={`/items/${encodeURIComponent(existing.id)}`}>打开详情</Link> : <button className="ghost-button" disabled={busy} onClick={() => void add(child)}>添加</button>}</div>; })}</details>}
  </section>;
}
