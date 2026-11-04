import { type CatalogItem, type Relation, mediaTypeLabel, visibleCode } from "../../lib/models";
import { useState } from "react";
import { Link } from "react-router-dom";
import { RelationKind } from "../../lib/appShared";

export function ItemPoster({ item, className = "", alt }: { item: { id: string; code: string; title: string; itemType?: string }; className?: string; alt?: string }) {
  const [loaded, setLoaded] = useState(false);
  const [failed, setFailed] = useState(false);
  const posterUrl = `/api/v1/items/${encodeURIComponent(item.id)}/images/poster`;

  return (
    <div className={`item-poster-wrap ${className}`}>
      {!loaded && !failed && <div className="item-poster-skeleton" />}
      {!failed ? (
        <img
          src={posterUrl}
          alt={alt || item.title || item.code}
          loading="lazy"
          decoding="async"
          className={`item-poster-img ${loaded ? "is-loaded" : ""}`}
          onLoad={() => setLoaded(true)}
          onError={() => setFailed(true)}
        />
      ) : (
        <div className="item-poster-fallback">
          <span>{visibleCode(item) || mediaTypeLabel(item.itemType)}</span>
        </div>
      )}
    </div>
  );
}

export function ItemCard({ item }: { item: CatalogItem }) {
  return (
    <Link to={`/items/${item.id}`} className="item-card" key={item.id}>
      <div className="item-card-media">
        <ItemPoster item={item} />
        <span className="item-card-code-badge">{visibleCode(item) || mediaTypeLabel(item.itemType)}</span>
        {item.sourceRating != null && item.sourceRating > 0 && (
          <span className="item-card-rating-badge">★ {item.sourceRating.toFixed(1)}</span>
        )}
      </div>
      <div className="item-card-info">
        <h3 className="item-card-title" title={item.title}>{item.title}</h3>
        {item.originalTitle && <p className="item-original-title" title={item.originalTitle}>{item.originalTitle}</p>}
        <div className="item-card-meta">
          <span className="item-card-year">{item.year ?? "未知"}</span>
          {item.sourceRating != null && item.sourceRating > 0 && (
            <span className="item-card-score">{item.sourceRating.toFixed(1)} 分</span>
          )}
        </div>
      </div>
    </Link>
  );
}

export function ItemGrid({ items, compact = false }: { items: CatalogItem[]; compact?: boolean }) {
  return (
    <div className={`item-grid${compact ? " item-grid-compact" : ""}`}>
      {items.map((item) => (
        <ItemCard item={item} key={item.id} />
      ))}
    </div>
  );
}

export function RelationBlock({ kind, label, values }: { kind: RelationKind; label: string; values?: Relation[] }) { if (!values || values.length === 0) return null; return <div className="relation-block"><span className="eyebrow">{label}</span><div className="relation-list">{values.map((value) => <Link className="relation-chip" to={`/${kind}/${value.id}`} key={`${value.id}-${value.role ?? ""}`}>{value.name}{value.role === "director" ? " · 导演" : value.role === "writer" ? " · 编剧" : ""}</Link>)}</div></div>; }

