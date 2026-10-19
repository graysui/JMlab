import { useEffect, useState, type FormEvent } from "react";
import { Link } from "react-router-dom";
import { apiFetch } from "../lib/api";

export type LibraryScope = { mode: "all" | "selected"; libraryIds: string[] };
type LibraryChoice = { id: string; name: string; enabled: boolean; selectable: boolean; reason?: string };

export function PlaybackScopePanel({ moduleId, value, onSaved }: { moduleId: string; value?: LibraryScope | null; onSaved: () => Promise<void> }) {
  const [mode, setMode] = useState<LibraryScope["mode"]>(value?.mode || "all");
  const [ids, setIDs] = useState(value?.libraryIds || []);
  const [libraries, setLibraries] = useState<LibraryChoice[]>([]);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");
  const [reload, setReload] = useState(0);
  const stored = JSON.stringify(value || { mode: "all", libraryIds: [] });
  useEffect(() => { const next = JSON.parse(stored) as LibraryScope; setMode(next.mode); setIDs(next.libraryIds || []); }, [stored]);
  useEffect(() => {
    let active = true; setLoading(true); setError("");
    void apiFetch<{ items: LibraryChoice[] }>("/api/v1/admin/modules/playback-libraries").then((result) => { if (active) setLibraries(result.items); }).catch((e) => { if (active) setError(e instanceof Error ? e.message : "读取媒体库失败"); }).finally(() => { if (active) setLoading(false); });
    return () => { active = false; };
  }, [reload]);
  async function save(event: FormEvent) {
    event.preventDefault(); setBusy(true); setError(""); setMessage("");
    try {
      await apiFetch(`/api/v1/admin/modules/${encodeURIComponent(moduleId)}`, { method: "PATCH", body: JSON.stringify({ libraryScope: { mode, libraryIds: mode === "all" ? [] : ids } }) });
      await onSaved(); setMessage("适用媒体库已保存，即时生效。");
    } catch (e) { setError(e instanceof Error ? e.message : "保存失败，请重试"); } finally { setBusy(false); }
  }
  const missing = ids.filter((id) => !libraries.some((library) => library.id === id));
  return <form className="dashboard-panel module-config-form playback-scope-panel" onSubmit={save} aria-label="播放源适用媒体库">
    <h2>适用媒体库</h2><p className="muted">只为所选库中的媒体查找和提供视频。媒体属于多个库时，匹配其中一个即可；用户仍需原有的查看和播放权限。</p>
    <fieldset disabled={busy}>
      <label>适用范围<select value={mode} onChange={(e) => { setMode(e.target.value as LibraryScope["mode"]); setMessage(""); }}><option value="all">全部媒体库（默认）</option><option value="selected">指定媒体库</option></select></label>
      {mode === "selected" && <div className="scope-library-list">
        {loading ? <p role="status">正在读取媒体库…</p> : <>
          {libraries.length === 0 && <p className="muted">尚未创建媒体库，请先<Link to="/settings/libraries">创建内容库</Link>。</p>}
          {libraries.map((library) => <label className="scope-library-choice" key={library.id}><input type="checkbox" checked={ids.includes(library.id)} disabled={!library.selectable && !ids.includes(library.id)} onChange={(e) => { setIDs((current) => e.target.checked ? [...current, library.id] : current.filter((id) => id !== library.id)); setMessage(""); }} /><span>{library.name}{!library.enabled && "（已停用）"}{library.reason && <small>{library.reason}</small>}</span></label>)}
          {missing.map((id) => <label className="scope-library-choice" key={id}><input type="checkbox" checked onChange={() => setIDs((current) => current.filter((value) => value !== id))} /><span>已删除的媒体库<small>取消勾选后保存即可移除。</small></span></label>)}
          <p className="muted">已选择 {ids.length} 个库。未选择时，此模块不会为任何媒体提供播放源；停用或删除的库不参与匹配。</p>
        </>}
      </div>}
      {error && <p className="form-error" role="alert">{error}</p>}{message && <p className="form-success" role="status">{message}</p>}
      <div className="table-actions"><button className="primary-button" disabled={loading}>{busy ? "保存中…" : "保存适用媒体库"}</button><button type="button" className="ghost-button" disabled={loading} onClick={() => setReload((current) => current + 1)}>刷新媒体库</button></div>
    </fieldset>
  </form>;
}
