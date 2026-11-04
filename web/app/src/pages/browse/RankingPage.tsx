import { apiFetch, readSession, userHasRole } from "../../lib/api";
import { type CatalogItem, mediaTypeLabel } from "../../lib/models";
import { useEffect, useState } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { ItemPoster } from "../../components/media/MediaCards";

export function RankingPage() {
  const [params, setParams] = useSearchParams();
  const [sources, setSources] = useState<Array<{ id: string; name: string; rankings: Array<{ id: string; name: string; periods: Array<{ value: string; label: string }> }> }>>([]);
  const [items, setItems] = useState<Array<{ rank: number; item: CatalogItem }>>([]);
  const [loading, setLoading] = useState(true); const [error, setError] = useState("");
  const source = sources.find((v) => v.id === params.get("moduleId")) || sources[0];
  const ranking = source?.rankings.find((v) => v.id === params.get("type")) || source?.rankings[0];
  const period = ranking?.periods.find((v) => v.value === params.get("period")) || ranking?.periods[0];
  useEffect(() => { let active = true; void apiFetch<{ items: typeof sources }>("/api/v1/rankings/modules").then((r) => { if (active) setSources(r.items); }).catch((e) => { if (active) setError(e.message); }).finally(() => { if (active) setLoading(false); }); return () => { active = false; }; }, []);
  useEffect(() => {
    if (!source || !ranking || !period) return;
    let active = true; setLoading(true); setError(""); setItems([]);
    void apiFetch<{ items: typeof items }>(`/api/v1/rankings/modules/${encodeURIComponent(source.id)}/${encodeURIComponent(ranking.id)}?period=${encodeURIComponent(period.value)}`).then((r) => { if (active) setItems(r.items); }).catch((e) => { if (active) setError(e.message); }).finally(() => { if (active) setLoading(false); });
    return () => { active = false; };
  }, [source?.id, ranking?.id, period?.value]);
  return <section className="content-card ranking-page"><div className="ranking-page-heading"><div><p className="eyebrow">发现内容</p><h1>模块排行榜</h1><p className="muted">选择内容来源、榜单和范围。只显示当前账号有权访问的已入库内容。</p></div><Link to="/search" className="ghost-button">搜索作品</Link></div>{error && <p className="form-error" role="alert">{error}</p>}{sources.length > 0 && <div className="form-grid"><label>内容来源<select value={source?.id || ""} onChange={(e) => setParams({ moduleId: e.target.value })}>{sources.map((v) => <option key={v.id} value={v.id}>{v.name}</option>)}</select></label><label>榜单<select value={ranking?.id || ""} onChange={(e) => setParams({ moduleId: source!.id, type: e.target.value })}>{source?.rankings.map((v) => <option key={v.id} value={v.id}>{v.name}</option>)}</select></label><label>范围<select value={period?.value || ""} onChange={(e) => setParams({ moduleId: source!.id, type: ranking!.id, period: e.target.value })}>{ranking?.periods.map((v) => <option key={v.value} value={v.value}>{v.label}</option>)}</select></label></div>}{loading ? <p role="status">正在读取榜单…</p> : items.length > 0 ? <ol className="ranking-list">{items.map((entry) => <li key={entry.item.id}><Link to={`/items/${entry.item.id}`} className="ranking-list-item"><span className="ranking-list-rank">{entry.rank}</span><div className="ranking-poster-mini"><ItemPoster item={entry.item} /></div><div className="ranking-list-info"><h3>{entry.item.title}</h3><p className="muted">{mediaTypeLabel(entry.item.itemType)} · {entry.item.year || "年份未知"}</p></div></Link></li>)}</ol> : <div className="empty-card"><h2>{sources.length ? "榜单暂时为空" : "还没有可用榜单"}</h2><p className="muted">管理员可在相关模块中同步榜单，并为账号授权媒体库。</p>{userHasRole(readSession()?.user, "Admin") && <Link to={source ? `/manage/modules/${encodeURIComponent(source.id)}?tab=rankings` : "/manage/modules"} className="ghost-button">管理榜单来源</Link>}</div>}</section>;
}

