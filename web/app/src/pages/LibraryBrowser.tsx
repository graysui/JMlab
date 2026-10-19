import { useCallback, useEffect, useRef, useState, type ReactNode } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { apiFetch, readSession, userHasRole } from "../lib/api";
import type { CatalogItem } from "../lib/models";

type Library = { id: string; name: string };
type Page = { items: CatalogItem[]; nextCursor: string };
const types = [{ value: "", label: "全部内容" }, { value: "movie", label: "电影" }, { value: "series", label: "剧集" }, { value: "video", label: "独立视频" }];
const sorts = [{ value: "updatedAt", label: "最近添加或更新" }, { value: "title", label: "名称顺序" }, { value: "year", label: "最新年份" }, { value: "rating", label: "评分从高到低" }];

export function LibraryBrowser({ renderItems }: { renderItems: (items: CatalogItem[]) => ReactNode }) {
  const [params, setParams] = useSearchParams();
  const kind = types.some((value) => value.value === params.get("type")) ? params.get("type") || "" : "";
  const sort = sorts.some((value) => value.value === params.get("sort")) ? params.get("sort")! : "updatedAt";
  const libraryID = params.get("library") || "";
  const query = new URLSearchParams({ limit: "24", topLevel: "true", sort, sortOrder: sort === "title" ? "asc" : "desc" });
  if (kind) query.set("itemType", kind);
  const requestKey = `${libraryID ? `/api/v1/libraries/${encodeURIComponent(libraryID)}/items` : "/api/v1/items"}?${query}`;
  const [libraries, setLibraries] = useState<Library[]>([]);
  const [libraryError, setLibraryError] = useState("");
  const [view, setView] = useState<Page & { key: string }>({ key: "", items: [], nextCursor: "" });
  const [busy, setBusy] = useState(true);
  const [error, setError] = useState("");
  const generation = useRef(0);
  const controller = useRef<AbortController | null>(null);
  const items = view.key === requestKey ? view.items : [];
  const cursor = view.key === requestKey ? view.nextCursor : "";
  const manage = userHasRole(readSession()?.user, "Admin", "Editor");

  useEffect(() => {
    const abort = new AbortController();
    void apiFetch<{ items: Library[] }>("/api/v1/libraries", { signal: abort.signal }).then((result) => setLibraries(result.items)).catch((e) => { if (!abort.signal.aborted) setLibraryError(e instanceof Error ? e.message : "读取媒体库列表失败"); });
    return () => abort.abort();
  }, []);
  const load = useCallback(async (next: string) => {
    controller.current?.abort();
    const abort = new AbortController(); controller.current = abort;
    const id = ++generation.current;
    setBusy(true); setError("");
    try {
      const page = await apiFetch<Page>(`${requestKey}${next ? `&cursor=${encodeURIComponent(next)}` : ""}`, { signal: abort.signal });
      if (id !== generation.current || abort.signal.aborted) return;
      setView((current) => {
        const combined = next && current.key === requestKey ? [...current.items, ...page.items] : page.items;
        return { key: requestKey, items: [...new Map(combined.map((item) => [item.id, item])).values()], nextCursor: page.nextCursor || "" };
      });
    } catch (e) {
      if (id === generation.current && !abort.signal.aborted) setError(e instanceof Error ? e.message : "读取内容失败，请重试");
    } finally { if (id === generation.current && !abort.signal.aborted) setBusy(false); }
  }, [requestKey]);
  useEffect(() => { void load(""); return () => { generation.current++; controller.current?.abort(); }; }, [load]);
  function choose(key: string, value: string) { const next = new URLSearchParams(params); if (value) next.set(key, value); else next.delete(key); setParams(next); }

  return <section className="content-card library-browser">
    <div className="relation-page-heading"><div><p className="eyebrow">我的媒体</p><h1>媒体库</h1><p className="muted">选择想看的内容。剧集中的季与单集可从剧集详情打开。</p></div>{manage && <Link to="/settings/discover" className="ghost-button">添加内容</Link>}</div>
    <div className="library-browser-filters">
      <label>媒体库范围<select value={libraryID} onChange={(event) => choose("library", event.target.value)}><option value="">所有可见媒体库</option>{libraries.map((library) => <option value={library.id} key={library.id}>{library.name}</option>)}{libraryID && !libraries.some((library) => library.id === libraryID) && <option value={libraryID}>所选媒体库</option>}</select></label>
      <label>内容类型<select value={kind} onChange={(event) => choose("type", event.target.value)}>{types.map((value) => <option key={value.value} value={value.value}>{value.label}</option>)}</select></label>
      <label>排序方式<select value={sort} onChange={(event) => choose("sort", event.target.value)}>{sorts.map((value) => <option key={value.value} value={value.value}>{value.label}</option>)}</select></label>
    </div>
    {libraryError && <p role="alert" className="form-error">{libraryError}</p>}
    {error && <div role="alert" className="form-error">{error} <button className="ghost-button" onClick={() => void load(cursor)} disabled={busy}>重试</button></div>}
    {items.length > 0 && renderItems(items)}
    {busy && <p role="status">正在读取内容…</p>}
    {!busy && !error && items.length === 0 && <div className="empty-card"><h3>这里还没有可见内容</h3><p className="muted">可以换一个媒体库或内容类型。需要新内容时，请联系管理员。</p>{manage && <Link className="primary-button" to="/settings/discover">搜索并添加内容</Link>}</div>}
    {items.length > 0 && <div className="library-browser-footer"><span className="muted">已显示 {items.length} 部内容</span>{cursor ? <button className="primary-button" disabled={busy} onClick={() => void load(cursor)}>{busy ? "正在读取…" : "加载更多"}</button> : !busy && <span className="muted">已显示全部内容</span>}</div>}
  </section>;
}
