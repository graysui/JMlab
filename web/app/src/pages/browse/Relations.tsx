import { apiFetch, readSession } from "../../lib/api";
import { visibleCode } from "../../lib/models";
import { FormEvent, useEffect, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { RelationKind, RelationEntity, relationLabels } from "../../lib/appShared";

export function RelationListPage({ kind }: { kind: RelationKind }) {
  const [query, setQuery] = useState("");
  const [submittedQuery, setSubmittedQuery] = useState("");
  const [entities, setEntities] = useState<RelationEntity[]>([]);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(true);
  const label = relationLabels[kind];
  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    setError("");
    const suffix = submittedQuery ? `?q=${encodeURIComponent(submittedQuery)}&limit=100` : "?limit=100";
    apiFetch<{ items: RelationEntity[] }>(`/api/v1/${kind}${suffix}`)
      .then((result) => { if (!cancelled) setEntities(result.items); })
      .catch((requestError) => { if (!cancelled) setError(requestError instanceof Error ? requestError.message : `读取${label}失败`); })
      .finally(() => { if (!cancelled) setLoading(false); });
    return () => { cancelled = true; };
  }, [kind, submittedQuery, label]);
  function submit(event: FormEvent) { event.preventDefault(); setSubmittedQuery(query.trim()); }
  return <section className="content-card relation-page"><div className="relation-page-heading"><div><p className="eyebrow">关系目录</p><h1>{label}</h1></div><RelationDirectoryLinks active={kind} /></div><form className="search-row" onSubmit={submit}><input aria-label={`搜索${label}`} placeholder={`搜索${label}名称`} value={query} onChange={(event) => setQuery(event.target.value)} /><button className="primary-button" type="submit">搜索</button></form>{error && <p className="form-error" role="alert">{error}</p>}{loading ? <p className="muted">正在读取{label}…</p> : entities.length === 0 ? <div className="empty-card"><h3>没有找到{label}</h3><p className="muted">请登录并确认当前用户已获媒体库授权。</p></div> : <div className="relation-grid">{entities.map((entity) => <Link className="relation-card" to={`/${kind}/${entity.id}`} key={entity.id}><span className="relation-card-kind">{label}</span><h3>{entity.name}</h3>{entity.aliases && entity.aliases.length > 0 && <p className="muted">{entity.aliases.slice(0, 2).join(" · ")}</p>}{entity.sourceName && <p className="muted">来源：{entity.sourceName}</p>}</Link>)}</div>}</section>;
}

export function RelationDirectoryLinks({ active }: { active: RelationKind }) {
  return <nav className="relation-directory" aria-label="关系目录导航">{(Object.keys(relationLabels) as RelationKind[]).map((kind) => <Link className={active === kind ? "active" : ""} to={`/${kind}`} key={kind}>{relationLabels[kind]}</Link>)}</nav>;
}

export function RelationDetailPage({ kind }: { kind: RelationKind }) {
  const { entityId } = useParams();
  const navigate = useNavigate();
  const [entity, setEntity] = useState<RelationEntity | null>(null);
  const [favorite, setFavorite] = useState(false);
  const [note, setNote] = useState("");
  const [noteBusy, setNoteBusy] = useState(false);
  const [noteMessage, setNoteMessage] = useState("");
  const [loadingFavorite, setLoadingFavorite] = useState(false);
  const [error, setError] = useState("");
  const [favoriteError, setFavoriteError] = useState("");
  const session = readSession();
  const label = relationLabels[kind];
  useEffect(() => {
    if (!entityId) return;
    setEntity(null);
    setError("");
    apiFetch<RelationEntity>(`/api/v1/${kind}/${entityId}`)
      .then(setEntity)
      .catch((requestError) => setError(requestError instanceof Error ? requestError.message : `读取${label}失败`));
  }, [entityId, kind, label]);
  useEffect(() => {
    if (kind !== "people" || !session || !entityId) return;
    apiFetch<{ items: Array<{ id: string }> }>("/api/v1/me/people/favorites")
      .then((result) => setFavorite(result.items.some((item) => item.id === entityId)))
      .catch(() => setFavorite(false));
  }, [entityId, kind, session?.accessToken]);
  useEffect(() => {
    if (!session || !entityId) { setNote(""); return; }
    apiFetch<{ note: string }>(`/api/v1/me/relations/${kind}/${entityId}/note`).then((result) => setNote(result.note)).catch(() => setNote(""));
  }, [entityId, kind, session?.accessToken]);
  async function toggleFavorite() {
    if (!entityId) return;
    if (!session) { navigate("/login"); return; }
    setLoadingFavorite(true);
    setFavoriteError("");
    try {
      const method = favorite ? "DELETE" : "POST";
      await apiFetch<{ favorite: boolean }>(`/api/v1/me/people/${entityId}/favorite`, { method });
      setFavorite(!favorite);
    } catch (requestError) {
      setFavoriteError(requestError instanceof Error ? requestError.message : "更新人物收藏失败");
    } finally { setLoadingFavorite(false); }
  }
  async function saveNote(event: FormEvent) {
    event.preventDefault();
    if (!session || !entityId) { navigate("/login"); return; }
    setNoteBusy(true); setNoteMessage("");
    try { await apiFetch(`/api/v1/me/relations/${kind}/${entityId}/note`, { method: "PUT", body: JSON.stringify({ note }) }); setNoteMessage("已保存"); } catch (requestError) { setNoteMessage(requestError instanceof Error ? requestError.message : "保存备注失败"); } finally { setNoteBusy(false); }
  }
  if (error) return <section className="content-card"><p className="form-error" role="alert">{error}</p><Link to={`/${kind}`} className="ghost-button">返回{label}列表</Link></section>;
  if (!entity) return <section className="content-card"><p className="muted">正在读取{label}…</p></section>;
  return <section className="content-card relation-detail"><div className="relation-page-heading"><div><p className="eyebrow">{label}详情</p><h1>{entity.name}</h1></div><RelationDirectoryLinks active={kind} /></div>{kind === "people" && <div className="relation-detail-actions"><button className={favorite ? "primary-button" : "ghost-button"} onClick={toggleFavorite} disabled={loadingFavorite}>{loadingFavorite ? "更新中…" : favorite ? "已收藏人物" : "收藏人物"}</button>{favoriteError && <span className="form-error">{favoriteError}</span>}</div>}<div className="relation-facts">{entity.gender && <span>性别：{entity.gender}</span>}{entity.status && <span>状态：{entity.status}</span>}{entity.birthday && <span>生日：{entity.birthday}</span>}{entity.heightCm && <span>身高：{entity.heightCm} cm</span>}{entity.cupSize && <span>罩杯：{entity.cupSize}</span>}{entity.sourceName && <span>来源：{entity.sourceName}</span>}{entity.tagKind && <span>类型：{entity.tagKind}</span>}{entity.measurements && Object.entries(entity.measurements).map(([key, value]) => <span key={key}>{key}：{value}</span>)}</div>{entity.sourceUrl && <div className="relation-block"><span className="eyebrow">情报站链接</span><a className="source-link" href={entity.sourceUrl} target="_blank" rel="noreferrer">打开来源页面</a></div>}{entity.aliases && entity.aliases.length > 0 && <div className="relation-block"><span className="eyebrow">别名</span><div className="relation-list">{entity.aliases.map((alias) => <span className="relation-chip" key={alias}>{alias}</span>)}</div></div>}{entity.biography && <div className="relation-block"><span className="eyebrow">简介</span><p className="muted relation-biography">{entity.biography}</p></div>}{session ? <form className="relation-note" onSubmit={saveNote}><label><span className="eyebrow">个人备注</span><textarea value={note} maxLength={2000} placeholder="只对当前账号可见" onChange={(event) => setNote(event.target.value)} /></label><div className="relation-note-actions"><button className="ghost-button" disabled={noteBusy}>{noteBusy ? "保存中…" : "保存备注"}</button>{noteMessage && <span className="muted">{noteMessage}</span>}</div></form> : <p className="muted">登录后可以添加仅自己可见的个人备注。</p>}{kind === "tags" && entity.parentId && <div className="relation-block"><span className="eyebrow">上级标签</span><Link className="relation-chip" to={`/tags/${entity.parentId}`}>{entity.parentId}</Link></div>}<div className="relation-block"><span className="eyebrow">关联作品</span>{entity.relatedItems && entity.relatedItems.length > 0 ? <div className="related-item-list">{entity.relatedItems.map((item) => <Link to={`/items/${item.id}`} className="related-item" key={item.id}><span className="item-code">{visibleCode(item)}</span><span>{item.title}</span></Link>)}</div> : <p className="muted">暂无可见关联作品。</p>}</div><Link to={`/${kind}`} className="ghost-button">返回{label}列表</Link></section>;
}

