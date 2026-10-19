import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { apiFetch } from "../lib/api";
export function ModuleRankingLinks() {
  const [sources, setSources] = useState<Array<{ id: string; name: string; rankings: Array<{ id: string; name: string }> }>>([]);
  useEffect(() => { let active = true; void apiFetch<{ items: typeof sources }>("/api/v1/rankings/modules").then((r) => { if (active) setSources(r.items); }).catch(() => {}); return () => { active = false; }; }, []);
  if (!sources.length) return null;
  return <section className="home-module"><h2>发现更多内容</h2><div className="management-card-grid">{sources.map((s) => <article className="management-card" key={s.id}><h3>{s.name}</h3><div className="table-actions">{s.rankings.map((r) => <Link className="ghost-button" key={r.id} to={`/rankings?moduleId=${encodeURIComponent(s.id)}&type=${encodeURIComponent(r.id)}`}>{r.name}</Link>)}</div></article>)}</div></section>;
}
