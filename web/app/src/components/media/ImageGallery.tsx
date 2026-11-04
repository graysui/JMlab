import { apiFetch, readSession } from "../../lib/api";
import { type PointerEvent as ReactPointerEvent, useEffect, useRef, useState } from "react";
import { ItemImage, isTextEntryTarget, getFocusableElements } from "../../lib/appShared";

export const imageTypeLabels: Record<ItemImage["imageType"], string> = { gallery: "剧照", preview: "预览", poster: "海报", backdrop: "背景", avatar: "头像" };

export function imageVariantURL(image: ItemImage, width: number, height: number, fit: "cover" | "contain", retry = 0) {
  const params = new URLSearchParams({ width: String(width), height: String(height), fit });
  if (retry > 0) params.set("retry", String(retry));
  return `${image.url}?${params.toString()}`;
}

export function ImageGallery({ itemId, initialImages, initialError, onImagesChange }: { itemId: string; initialImages: ItemImage[]; initialError: string; onImagesChange: (images: ItemImage[]) => void }) {
  const session = readSession();
  const [images, setImages] = useState<ItemImage[]>(initialImages);
  const [typeFilter, setTypeFilter] = useState<"all" | ItemImage["imageType"]>("all");
  const [favoriteOnly, setFavoriteOnly] = useState(false);
  const [previewFavoritesOnly, setPreviewFavoritesOnly] = useState(false);
  const [failed, setFailed] = useState<Record<string, boolean>>({});
  const [retryCount, setRetryCount] = useState<Record<string, number>>({});
  const [lightboxIndex, setLightboxIndex] = useState<number | null>(null);
  const [zoom, setZoom] = useState(1);
  const [offset, setOffset] = useState({ x: 0, y: 0 });
  const dragRef = useRef<{ pointerId: number; x: number; y: number; offsetX: number; offsetY: number } | null>(null);
  const lightboxDialogRef = useRef<HTMLDivElement | null>(null);
  const lightboxTriggerRef = useRef<HTMLButtonElement | null>(null);
  const lightboxOpenRef = useRef(false);

  useEffect(() => { setImages(initialImages); }, [initialImages]);
  const readyImages = images.filter((image) => image.status !== "error" && image.imageType !== "avatar");
  const visibleImages = readyImages.filter((image) => (typeFilter === "all" || image.imageType === typeFilter) && (!favoriteOnly || image.isFavorite) && (!previewFavoritesOnly || (image.imageType === "preview" && image.isFavorite)));
  const lightboxImage = lightboxIndex == null ? null : visibleImages[lightboxIndex] ?? null;

  useEffect(() => {
    if (lightboxIndex == null) return;
    const onKeyDown = (event: KeyboardEvent) => {
      if (isTextEntryTarget(event.target)) return;
      if (event.key === "Escape") { setLightboxIndex(null); return; }
      if (event.key === "Tab") {
        const dialog = lightboxDialogRef.current;
        if (!dialog) return;
        const focusable = getFocusableElements(dialog);
        if (focusable.length === 0) { event.preventDefault(); dialog.focus(); return; }
        const currentIndex = focusable.indexOf(document.activeElement as HTMLElement);
        const nextIndex = event.shiftKey
          ? (currentIndex <= 0 ? focusable.length - 1 : currentIndex - 1)
          : (currentIndex === focusable.length - 1 ? 0 : currentIndex + 1);
        event.preventDefault();
        focusable[nextIndex]?.focus();
        return;
      }
      if (event.key === "ArrowLeft") { event.preventDefault(); setLightboxIndex((index) => index == null ? null : (index - 1 + visibleImages.length) % visibleImages.length); setZoom(1); setOffset({ x: 0, y: 0 }); return; }
      if (event.key === "ArrowRight") { event.preventDefault(); setLightboxIndex((index) => index == null ? null : (index + 1) % visibleImages.length); setZoom(1); setOffset({ x: 0, y: 0 }); return; }
      if (event.key === "+" || event.key === "=") { event.preventDefault(); setZoom((value) => Math.min(4, Number((value + .25).toFixed(2)))); return; }
      if (event.key === "-") { event.preventDefault(); setZoom((value) => Math.max(1, Number((value - .25).toFixed(2)))); return; }
      if (event.key === "0") { event.preventDefault(); setZoom(1); setOffset({ x: 0, y: 0 }); }
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [lightboxIndex, visibleImages.length]);

  useEffect(() => {
    if (lightboxIndex != null && lightboxImage) {
      if (!lightboxOpenRef.current) {
        lightboxOpenRef.current = true;
        window.requestAnimationFrame(() => lightboxDialogRef.current?.focus());
      }
      return;
    }
    if (lightboxOpenRef.current) {
      lightboxOpenRef.current = false;
      lightboxTriggerRef.current?.focus();
      lightboxTriggerRef.current = null;
    }
  }, [lightboxIndex, lightboxImage]);

  useEffect(() => {
    if (lightboxIndex == null) return;
    const previousOverflow = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    return () => { document.body.style.overflow = previousOverflow; };
  }, [lightboxIndex]);

  function updateImages(nextImages: ItemImage[]) {
    setImages(nextImages);
    onImagesChange(nextImages);
  }

  async function toggleFavorite(image: ItemImage) {
    if (!session) return;
    try {
      const next = await apiFetch<{ favorite: boolean }>(`/api/v1/me/images/${encodeURIComponent(image.id)}/favorite`, { method: image.isFavorite ? "DELETE" : "POST" });
      updateImages(images.map((value) => value.id === image.id ? { ...value, isFavorite: next.favorite } : value));
    } catch (requestError) {
      setFailed((values) => ({ ...values, [`favorite:${image.id}`]: true }));
    }
  }

  function retry(image: ItemImage) {
    setFailed((values) => ({ ...values, [image.id]: false }));
    setRetryCount((values) => ({ ...values, [image.id]: (values[image.id] ?? 0) + 1 }));
  }

  function openLightbox(index: number, trigger?: HTMLButtonElement) {
    lightboxTriggerRef.current = trigger ?? null;
    setLightboxIndex(index); setZoom(1); setOffset({ x: 0, y: 0 });
  }

  function moveLightbox(direction: -1 | 1) {
    setLightboxIndex((index) => index == null ? null : (index + direction + visibleImages.length) % visibleImages.length);
    setZoom(1); setOffset({ x: 0, y: 0 });
  }

  function startDrag(event: ReactPointerEvent<HTMLDivElement>) {
    if (zoom <= 1 || !lightboxImage) return;
    event.currentTarget.setPointerCapture(event.pointerId);
    dragRef.current = { pointerId: event.pointerId, x: event.clientX, y: event.clientY, offsetX: offset.x, offsetY: offset.y };
  }

  function drag(event: ReactPointerEvent<HTMLDivElement>) {
    const start = dragRef.current;
    if (!start || start.pointerId !== event.pointerId) return;
    setOffset({ x: start.offsetX + event.clientX - start.x, y: start.offsetY + event.clientY - start.y });
  }

  function stopDrag(event: ReactPointerEvent<HTMLDivElement>) {
    if (dragRef.current?.pointerId === event.pointerId) dragRef.current = null;
  }

  return (
    <section className="image-gallery" aria-labelledby="image-gallery-heading">
      <div className="gallery-heading"><div><p className="eyebrow">剧照与预览</p><h2 id="image-gallery-heading">图片画廊</h2><p className="muted">缩略图按需加载；打开灯箱后可用 Esc 关闭、←/→ 翻页、+/- 缩放、0 重置。</p></div><span className="gallery-count" aria-live="polite">{visibleImages.length} 张图片</span></div>
      {initialError && <p className="form-error" role="alert">{initialError}</p>}
      <div className="gallery-toolbar" role="toolbar" aria-label="图片筛选"><div className="gallery-tabs">{(["all", "gallery", "preview", "poster", "backdrop"] as const).map((value) => <button key={value} type="button" className={typeFilter === value ? "active" : ""} aria-pressed={typeFilter === value} onClick={() => setTypeFilter(value)}>{value === "all" ? "全部" : imageTypeLabels[value]}</button>)}</div><div className="gallery-checks"><label className="inline-check"><input type="checkbox" checked={favoriteOnly} onChange={(event) => setFavoriteOnly(event.target.checked)} disabled={!session} />只看收藏</label><label className="inline-check"><input type="checkbox" checked={previewFavoritesOnly} onChange={(event) => setPreviewFavoritesOnly(event.target.checked)} disabled={!session} />仅看收藏预览图</label></div></div>
      {visibleImages.length === 0 ? <div className="gallery-empty"><span aria-hidden="true">▧</span><p>{readyImages.length === 0 ? "暂无剧照或预览图" : favoriteOnly || previewFavoritesOnly ? "当前筛选没有收藏图片" : "当前类型暂无图片"}</p></div> : <div className="gallery-grid">{visibleImages.map((image, index) => {
        const failedImage = failed[image.id];
        return <figure className="gallery-tile" key={image.id}>
          {failedImage ? <div className="gallery-image-button gallery-image-error" aria-live="polite"><span className="gallery-failed"><strong>图片加载失败</strong><button type="button" className="ghost-button compact-button" onClick={() => retry(image)}>重试</button></span></div> : <button type="button" className="gallery-image-button" onClick={(event) => openLightbox(index, event.currentTarget)} onKeyDown={(event) => { if (event.key === "Enter" || event.key === " ") { event.preventDefault(); openLightbox(index, event.currentTarget); } }} aria-label={`打开${imageTypeLabels[image.imageType]} ${index + 1}`}><img src={imageVariantURL(image, 420, 300, "cover", retryCount[image.id] ?? 0)} alt={`${imageTypeLabels[image.imageType]} ${index + 1}`} loading="lazy" decoding="async" onError={() => setFailed((values) => ({ ...values, [image.id]: true }))} /></button>}
          <figcaption><span>{imageTypeLabels[image.imageType]}{image.sourceLabel ? ` · ${image.sourceLabel}` : ""}</span><button type="button" className={`gallery-favorite${image.isFavorite ? " active" : ""}`} aria-label={image.isFavorite ? "取消收藏图片" : "收藏图片"} aria-pressed={image.isFavorite} onClick={() => void toggleFavorite(image)} disabled={!session}>{image.isFavorite ? "★" : "☆"}</button></figcaption>
        </figure>;
      })}</div>}
      {lightboxImage && <div className="lightbox-backdrop" onClick={() => setLightboxIndex(null)}><div ref={lightboxDialogRef} className="lightbox-dialog" role="dialog" aria-modal="true" aria-labelledby="lightbox-title" tabIndex={-1} onClick={(event) => event.stopPropagation()}><div className="lightbox-toolbar"><span id="lightbox-title" aria-live="polite">{lightboxIndex! + 1} / {visibleImages.length} · {imageTypeLabels[lightboxImage.imageType]}</span><div><button type="button" className="ghost-button compact-button" aria-pressed={lightboxImage.isFavorite} onClick={() => void toggleFavorite(lightboxImage)} disabled={!session}>{lightboxImage.isFavorite ? "取消收藏" : "收藏"}</button><button type="button" className="ghost-button compact-button" onClick={() => setLightboxIndex(null)}>关闭</button></div></div><div className="lightbox-viewport" aria-label="图片查看区域" onPointerDown={startDrag} onPointerMove={drag} onPointerUp={stopDrag} onPointerCancel={stopDrag} onWheel={(event) => { event.preventDefault(); setZoom((value) => Math.max(1, Math.min(4, Number((value + (event.deltaY < 0 ? .15 : -.15)).toFixed(2))))); }}><img src={imageVariantURL(lightboxImage, 1600, 1200, "contain", retryCount[lightboxImage.id] ?? 0)} alt={`${imageTypeLabels[lightboxImage.imageType]} ${lightboxIndex! + 1}`} draggable={false} style={{ transform: `translate(${offset.x}px, ${offset.y}px) scale(${zoom})` }} onError={() => setFailed((values) => ({ ...values, [lightboxImage.id]: true }))} /></div><button type="button" className="lightbox-nav lightbox-prev" onClick={() => moveLightbox(-1)} aria-label="上一张">‹</button><button type="button" className="lightbox-nav lightbox-next" onClick={() => moveLightbox(1)} aria-label="下一张">›</button><div className="lightbox-controls"><button type="button" className="ghost-button compact-button" aria-label="缩小" onClick={() => setZoom((value) => Math.max(1, Number((value - .25).toFixed(2))))}>−</button><span aria-live="polite">{Math.round(zoom * 100)}%</span><button type="button" className="ghost-button compact-button" aria-label="放大" onClick={() => setZoom((value) => Math.min(4, Number((value + .25).toFixed(2))))}>＋</button><button type="button" className="ghost-button compact-button" onClick={() => { setZoom(1); setOffset({ x: 0, y: 0 }); }}>重置</button></div></div></div>}
    </section>
  );
}

