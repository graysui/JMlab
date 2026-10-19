import { useEffect, useState, type FormEvent } from "react";
import { Link } from "react-router-dom";
import { apiFetch, readSession } from "../lib/api";
import { mediaTypeLabel } from "../lib/models";

type Playlist = { id: string; name: string; ownerUserId: string; isPublic: boolean };
type PlaylistItem = { itemId: string; title: string; itemType: string; position: number; year?: number };
const textOf = (error: unknown) => error instanceof Error ? error.message : "操作失败，请稍后重试";

export function PlaylistsPage() {
  const session = readSession();
  const [lists, setLists] = useState<Playlist[]>([]);
  const [selected, setSelected] = useState<Playlist | null>(null);
  const [items, setItems] = useState<PlaylistItem[]>([]);
  const [name, setName] = useState("");
  const [rename, setRename] = useState("");
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [loading, setLoading] = useState(true);
  const [itemsLoading, setItemsLoading] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");
  const own = selected?.ownerUserId === session?.user.id;
  async function loadLists() { const result = await apiFetch<{ items: Playlist[] }>("/api/v1/playlists"); setLists(result.items); }
  useEffect(() => { if (session) void loadLists().catch((e) => setError(textOf(e))).finally(() => setLoading(false)); }, [session?.user.id]);
  useEffect(() => {
    setItems([]); setConfirmDelete(false); setRename(selected?.name || "");
    if (!selected) return;
    let cancelled = false; setItemsLoading(true);
    void apiFetch<{ items: PlaylistItem[] }>(`/api/v1/playlists/${encodeURIComponent(selected.id)}/items`).then((result) => { if (!cancelled) setItems(result.items); }).catch((e) => { if (!cancelled) setError(textOf(e)); }).finally(() => { if (!cancelled) setItemsLoading(false); });
    return () => { cancelled = true; };
  }, [selected?.id]);
  async function run(action: () => Promise<void>) { setBusy(true); setError(""); setMessage(""); try { await action(); } catch (e) { setError(textOf(e)); } finally { setBusy(false); } }
  async function create(event: FormEvent) {
    event.preventDefault(); await run(async () => { const result = await apiFetch<Playlist>("/api/v1/playlists", { method: "POST", body: JSON.stringify({ name: name.trim(), isPublic: false }) }); setName(""); await loadLists(); setSelected(result); setMessage("清单已创建。在作品详情中可以添加内容。"); });
  }
  async function remove(itemId: string) {
    if (!selected) return;
    await run(async () => { await apiFetch(`/api/v1/playlists/${encodeURIComponent(selected.id)}/items/${encodeURIComponent(itemId)}`, { method: "DELETE" }); setItems((current) => current.filter((item) => item.itemId !== itemId)); setMessage("已从清单移除，媒体库中的作品仍会保留。"); });
  }
  if (!session) return <section className="content-card"><h1>登录后使用我的清单</h1><p>按自己的习惯整理想看的内容。</p><Link to="/login" className="primary-button">前往登录</Link></section>;
  return <section className="content-card management-page"><h1>我的清单</h1><p className="muted">把想看的内容整理到一起。新建清单仅自己可见。</p>
    {error && <p className="form-error" role="alert">{error}</p>}{message && <p className="form-success" role="status">{message}</p>}
    <form className="admin-toolbar" onSubmit={create}><label>新清单名称<input required maxLength={100} value={name} onChange={(e) => setName(e.target.value)} placeholder="例如：周末想看" /></label><button className="primary-button" disabled={busy || !name.trim()}>创建清单</button></form>
    {loading ? <p role="status">正在读取清单…</p> : lists.length === 0 ? <p className="muted">还没有清单，先创建一个吧。</p> : <div className="playlist-layout"><aside className="playlist-sidebar" aria-label="选择清单">{lists.map((list) => <button type="button" key={list.id} className={`playlist-choice ${selected?.id === list.id ? "selected" : ""}`} aria-pressed={selected?.id === list.id} disabled={busy} onClick={() => { setSelected(list); setError(""); }}><strong>{list.name}</strong><small>{list.ownerUserId === session.user.id ? "我的清单" : "他人分享"}</small></button>)}</aside><div className="playlist-content">
      {!selected ? <p>选择左侧的清单查看内容。</p> : <><h2>{selected.name}</h2>{own && <details><summary>管理这个清单</summary><form className="admin-toolbar" onSubmit={(event) => { event.preventDefault(); void run(async () => { await apiFetch(`/api/v1/playlists/${encodeURIComponent(selected.id)}`, { method: "PATCH", body: JSON.stringify({ name: rename.trim() }) }); setSelected({ ...selected, name: rename.trim() }); await loadLists(); setMessage("清单名称已保存。"); }); }}><label>清单名称<input required maxLength={100} value={rename} onChange={(e) => setRename(e.target.value)} /></label><button className="ghost-button" disabled={busy}>保存名称</button></form>{confirmDelete ? <div><p>删除“{selected.name}”及其中的清单记录？媒体库中的作品会保留。</p><button className="ghost-button" disabled={busy} onClick={() => void run(async () => { await apiFetch(`/api/v1/playlists/${encodeURIComponent(selected.id)}`, { method: "DELETE" }); setSelected(null); await loadLists(); setMessage("清单已删除。"); })}>确认删除清单</button><button className="ghost-button" disabled={busy} onClick={() => setConfirmDelete(false)}>取消</button></div> : <button className="ghost-button" disabled={busy} onClick={() => setConfirmDelete(true)}>删除清单</button>}</details>}
      {itemsLoading ? <p role="status">正在读取内容…</p> : items.length === 0 ? <p className="muted">暂无可见内容。从作品详情选择“加入清单”即可添加。</p> : items.map((item) => <article className="media-list-row" key={item.itemId}><Link to={`/items/${encodeURIComponent(item.itemId)}`}><strong>{item.title}</strong><p className="muted">{mediaTypeLabel(item.itemType)}{item.year ? ` · ${item.year}` : ""}</p></Link><div className="table-actions"><Link className="ghost-button" to={`/items/${encodeURIComponent(item.itemId)}${item.itemType === "series" || item.itemType === "season" ? "" : "/play"}`}>{item.itemType === "series" || item.itemType === "season" ? "选择单集" : "播放"}</Link>{own && <button className="ghost-button" disabled={busy} onClick={() => void remove(item.itemId)}>移除</button>}</div></article>)}</>}
    </div></div>}
  </section>;
}

export function AddToPlaylist({ itemId }: { itemId: string }) {
  const session = readSession();
  const [lists, setLists] = useState<Playlist[]>([]);
  const [selected, setSelected] = useState("");
  const [name, setName] = useState("");
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("");
  const [error, setError] = useState("");
  useEffect(() => { if (!session) return; let cancelled = false; void apiFetch<{ items: Playlist[] }>("/api/v1/playlists").then((result) => { if (cancelled) return; const own = result.items.filter((list) => list.ownerUserId === session.user.id); setLists(own); setSelected(own[0]?.id || ""); }).catch((e) => { if (!cancelled) setError(textOf(e)); }); return () => { cancelled = true; }; }, [session?.user.id]);
  async function add(event: FormEvent) {
    event.preventDefault(); setBusy(true); setError(""); setMessage("");
    try {
      let id = selected;
      if (!id) { const list = await apiFetch<Playlist>("/api/v1/playlists", { method: "POST", body: JSON.stringify({ name: name.trim(), isPublic: false }) }); id = list.id; setLists((current) => [...current, list]); setSelected(id); setName(""); }
      await apiFetch(`/api/v1/playlists/${encodeURIComponent(id)}/items`, { method: "POST", body: JSON.stringify({ itemId }) }); setMessage("已加入清单。");
    } catch (e) { setError(textOf(e)); } finally { setBusy(false); }
  }
  if (!session) return null;
  return <section className="content-card"><details><summary>加入我的清单</summary><form className="admin-toolbar" onSubmit={add}><label>选择清单<select value={selected} disabled={busy} onChange={(e) => setSelected(e.target.value)}>{lists.map((list) => <option key={list.id} value={list.id}>{list.name}</option>)}<option value="">新建清单…</option></select></label>{!selected && <label>新清单名称<input required maxLength={100} disabled={busy} value={name} onChange={(e) => setName(e.target.value)} placeholder="例如：周末想看" /></label>}<button className="primary-button" disabled={busy}>{busy ? "添加中…" : "加入清单"}</button><Link to="/playlists" className="ghost-button">查看我的清单</Link></form></details>{message && <p role="status" className="form-success">{message}</p>}{error && <p role="alert" className="form-error">{error}</p>}</section>;
}
