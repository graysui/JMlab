import { useEffect, useState, type FormEvent } from "react";
import { apiFetch, readSession } from "../lib/api";

type State = { isFavorite: boolean; isWantToWatch: boolean; isWatched: boolean; version: number; userRating?: number; note?: string };
export function MediaStatePanel({ itemId }: { itemId: string }) {
  const [state, setState] = useState<State | null>(null);
  const [rating, setRating] = useState("");
  const [note, setNote] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");
  const session = readSession();
  useEffect(() => {
    if (!session) return;
    let cancelled = false;
    void apiFetch<State>(`/api/v1/me/items/${encodeURIComponent(itemId)}/state`).then((value) => { if (!cancelled) { setState(value); setRating(value.userRating == null ? "" : String(value.userRating)); setNote(value.note || ""); } }).catch((e) => { if (!cancelled) setError(e instanceof Error ? e.message : "读取观看记录失败"); });
    return () => { cancelled = true; };
  }, [itemId, session?.user.id]);
  async function save(patch: Record<string, unknown>) {
    if (!state) return; setBusy(true); setError(""); setMessage("");
    try { const value = await apiFetch<State>(`/api/v1/me/items/${encodeURIComponent(itemId)}/state`, { method: "PUT", body: JSON.stringify({ version: state.version, ...patch }) }); setState(value); setMessage("已保存到你的账号。"); }
    catch (e) { setError(e instanceof Error ? e.message : "保存失败"); } finally { setBusy(false); }
  }
  if (!session) return null;
  return <section className="content-card"><h2>我的观看记录</h2>{state && <><div className="table-actions"><button className="ghost-button" disabled={busy} aria-pressed={state.isFavorite} onClick={() => void save({ favorite: !state.isFavorite })}>{state.isFavorite ? "★ 已收藏" : "☆ 收藏"}</button><button className="ghost-button" disabled={busy} aria-pressed={state.isWantToWatch} onClick={() => void save({ wantToWatch: !state.isWantToWatch })}>{state.isWantToWatch ? "已加入想看" : "想看"}</button><button className="ghost-button" disabled={busy} aria-pressed={state.isWatched} onClick={() => void save({ watched: !state.isWatched })}>{state.isWatched ? "✓ 已看" : "标记已看"}</button></div><details><summary>评分与备注</summary><form className="module-config-form" onSubmit={(e: FormEvent) => { e.preventDefault(); void save({ rating: rating === "" ? null : Number(rating), note }); }}><label>我的评分（0～10 分）<input type="number" min="0" max="10" step="1" value={rating} onChange={(e) => setRating(e.target.value)} /></label><label>私人备注<textarea maxLength={2000} value={note} onChange={(e) => setNote(e.target.value)} /></label><button className="ghost-button" disabled={busy}>保存评分与备注</button></form></details></>}{message && <p className="form-success" role="status">{message}</p>}{error && <p className="form-error" role="alert">{error}</p>}</section>;
}
