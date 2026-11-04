import type { PlaybackSourceRecord } from "../../lib/models";
import { apiFetch, readSession, userHasRole } from "../../lib/api";
import { type CatalogItem, mediaTypeLabel, visibleCode } from "../../lib/models";
import { MediaChildren } from "../DiscoverPage";
import { AddToPlaylist } from "../PlaylistsPage";
import { MediaStatePanel } from "../MediaStatePanel";
import { useEffect, useState } from "react";
import { Link, useParams } from "react-router-dom";
import { ItemImage } from "../../lib/appShared";
import { ItemPoster, RelationBlock } from "../../components/media/MediaCards";
import { ImageGallery } from "../../components/media/ImageGallery";

export function ItemPage() {
  const { itemId } = useParams();
  const [item, setItem] = useState<CatalogItem | null>(null);
  const [images, setImages] = useState<ItemImage[]>([]);
  const [sources, setSources] = useState<PlaybackSourceRecord[]>([]);
  const [error, setError] = useState("");
  const [imageError, setImageError] = useState("");
  const [sourceError, setSourceError] = useState("");
  const [sourcesLoading, setSourcesLoading] = useState(true);
  const [copyMessage, setCopyMessage] = useState("");
  useEffect(() => {
    if (!itemId) return;
    let cancelled = false;
    setItem(null); setImages([]); setSources([]); setError(""); setImageError(""); setSourceError(""); setCopyMessage(""); setSourcesLoading(true);
    void apiFetch<CatalogItem>(`/api/v1/items/${encodeURIComponent(itemId)}`).then((next) => {
      if (cancelled) return; setItem(next);
      if (next.itemType === "series" || next.itemType === "season") { setSourcesLoading(false); return; }
      void apiFetch<{ items: PlaybackSourceRecord[] }>(`/api/v1/items/${encodeURIComponent(itemId)}/sources`).then((result) => { if (!cancelled) setSources(result.items); }).catch((e) => { if (!cancelled) setSourceError(e instanceof Error ? e.message : "读取播放来源失败"); }).finally(() => { if (!cancelled) setSourcesLoading(false); });
    }).catch((e) => { if (!cancelled) setError(e instanceof Error ? e.message : "读取作品失败"); });
    void apiFetch<{ items: ItemImage[] }>(`/api/v1/items/${encodeURIComponent(itemId)}/images`).then((result) => { if (!cancelled) setImages(result.items); }).catch((e) => { if (!cancelled) setImageError(e instanceof Error ? e.message : "读取图片失败"); });
    return () => { cancelled = true; };
  }, [itemId]);
  async function copyCode() {
    if (!item || !visibleCode(item)) return;
    try { await navigator.clipboard.writeText(item.code); setCopyMessage("已复制"); } catch { setCopyMessage("请手动复制"); }
  }
  if (error) return <section className="content-card"><p className="form-error" role="alert">{error}</p><Link to="/libraries" className="ghost-button">返回媒体库</Link></section>;
  if (!item) return <section className="content-card"><p role="status">正在读取作品…</p></section>;
  const container = item.itemType === "series" || item.itemType === "season";
  const playable = sources.filter((source) => source.enabled && source.status === "active");
  const sourceURL = /^https?:\/\//i.test(item.sourceUrl || "") ? item.sourceUrl : "";
  return <div className="item-detail-page">
    <section className="item-hero-section"><div className="item-hero-backdrop" style={{ backgroundImage: `url(/api/v1/items/${encodeURIComponent(item.id)}/images/backdrop)` }} /><div className="item-hero-backdrop-overlay" /><div className="item-hero-inner"><div className="item-hero-poster"><ItemPoster item={item} /></div><div className="item-hero-info">
      <div className="item-hero-badges"><span className="badge-meta">{mediaTypeLabel(item.itemType)}</span>{visibleCode(item) && <button type="button" className="badge-code" onClick={() => void copyCode()} title="复制内容标识">{visibleCode(item)}</button>}{item.sourceRating != null && item.sourceRating > 0 && <span className="badge-rating">★ {item.sourceRating.toFixed(1)} 分</span>}{item.year && <span className="badge-meta">{item.year} 年</span>}{item.runtimeSeconds != null && item.runtimeSeconds > 0 && <span className="badge-meta">{Math.floor(item.runtimeSeconds / 60)} 分钟</span>}</div>
      <h1 className="item-hero-title">{item.title}</h1>{item.originalTitle && item.originalTitle !== item.title && <p className="item-hero-orig-title">{item.originalTitle}</p>}<p className="item-hero-overview">{item.overview || "暂无作品简介"}</p>
      <div className="item-hero-actions">{container ? <a href="#episodes" className="hero-play-button">{item.itemType === "series" ? "选择季与单集" : "选择单集"}</a> : <Link to={`/items/${encodeURIComponent(item.id)}/play`} className="hero-play-button">▶ 播放</Link>}{item.parentId && <Link to={`/items/${encodeURIComponent(item.parentId)}`} className="ghost-button">返回上一级</Link>}<Link to="/libraries" className="ghost-button">返回媒体库</Link></div>{copyMessage && <p role="status">{copyMessage}</p>}
    </div></div></section>
    {container ? <MediaChildren key={item.id} item={item} /> : <section className="content-card"><div className="relation-page-heading"><h2>播放来源</h2>{userHasRole(readSession()?.user, "Admin", "Editor") && <Link to={`/manage/sources?${new URLSearchParams({ itemId: item.id })}`} className="ghost-button">管理播放源</Link>}</div>{sourcesLoading ? <p role="status">正在查找播放来源…</p> : sourceError ? <p className="form-error" role="alert">{sourceError}</p> : playable.length === 0 ? <p className="muted">暂未找到可用视频。作品信息已保留，可以稍后再试，或请管理员检查视频来源。</p> : <div className="builtin-sources-grid">{playable.map((source) => <Link to={`/items/${encodeURIComponent(item.id)}/play?sourceId=${encodeURIComponent(source.id)}`} className="source-card" key={source.id}><span><strong>{source.label}</strong><small>{source.isPreview ? "预告片" : "视频"}{source.quality ? ` · ${source.quality}` : ""}</small></span><span>播放 ▶</span></Link>)}</div>}</section>}
    <MediaStatePanel key={`state-${item.id}`} itemId={item.id} />
    <AddToPlaylist key={item.id} itemId={item.id} />
    {(item.people?.length || item.studios?.length || item.series?.length || item.tags?.length || sourceURL || visibleCode(item)) ? <section className="content-card item-relations-section"><RelationBlock kind="people" label="演职人员" values={item.people} /><RelationBlock kind="studios" label="制作公司" values={item.studios} /><RelationBlock kind="series" label="作品系列" values={item.series} /><RelationBlock kind="tags" label="分类" values={item.tags} />{sourceURL && <a href={sourceURL} target="_blank" rel="noreferrer" className="ghost-button">查看来源信息 ↗</a>}</section> : null}
    <ImageGallery itemId={item.id} initialImages={images} initialError={imageError} onImagesChange={setImages} />
  </div>;
}

