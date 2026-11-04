import type { PlaybackSourceRecord } from "../../lib/models";
import { apiFetch, readSession } from "../../lib/api";
import { type CatalogItem, mediaTypeLabel, visibleCode } from "../../lib/models";
import { useCallback, useEffect, useRef, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { HlsInstance, PlaybackResolution, PlaybackState, isTextEntryTarget, getFocusableElements } from "../../lib/appShared";

export function PlayPage() {
  const TICKS_PER_SECOND = 10_000_000;
  const { itemId } = useParams();
  const navigate = useNavigate();
  const session = readSession();
  const videoRef = useRef<HTMLVideoElement | null>(null);
  const lastVideoRef = useRef<HTMLVideoElement | null>(null);
  const hlsRef = useRef<HlsInstance | null>(null);
  const playbackSessionID = useRef(`web-${typeof crypto !== "undefined" && "randomUUID" in crypto ? crypto.randomUUID() : `${Date.now()}-${Math.random().toString(36).slice(2)}`}`);
  const pendingResumeTicks = useRef<number | null>(null);
  const [item, setItem] = useState<CatalogItem | null>(null);
  const [sources, setSources] = useState<PlaybackSourceRecord[]>([]);
  const [sourceId, setSourceId] = useState("");
  const [quality, setQuality] = useState("");
  const [language, setLanguage] = useState("");
  const [allowPreview, setAllowPreview] = useState(false);
  const [resolution, setResolution] = useState<PlaybackResolution | null>(null);
  const [loading, setLoading] = useState(true);
  const [resolving, setResolving] = useState(false);
  const [retrying, setRetrying] = useState(false);
  const [error, setError] = useState("");
  const [playerError, setPlayerError] = useState("");
  const [resumeTicks, setResumeTicks] = useState(0);
  const [resumeChoice, setResumeChoice] = useState<"ready" | "pending" | "continue" | "restart">("ready");
  const resumeOverlayRef = useRef<HTMLDivElement | null>(null);
  const resumeOpenRef = useRef(false);

  const selectableSources = sources.filter((source) => source.enabled && source.status !== "unavailable" && (allowPreview || !source.isPreview));
  const qualities = Array.from(new Set(selectableSources.map((source) => source.quality).filter((value): value is string => Boolean(value))));
  const languages = Array.from(new Set(selectableSources.map((source) => source.language).filter((value): value is string => Boolean(value))));

  const sendPlaybackEvent = useCallback(async (eventPath: "Playing" | "Playing/Progress" | "Playing/Stopped", keepalive = false) => {
    const video = videoRef.current ?? lastVideoRef.current;
    if (!video || !itemId || !session?.accessToken) return;
    const currentTime = Number.isFinite(video.currentTime) && video.currentTime >= 0 ? video.currentTime : 0;
    const duration = Number.isFinite(video.duration) && video.duration > 0 ? video.duration : 0;
    const headers = new Headers({ Accept: "application/json", "Content-Type": "application/json", Authorization: `Bearer ${session.accessToken}` });
    if (session.csrfToken) headers.set("X-CSRF-Token", session.csrfToken);
    try {
      await fetch(`/Sessions/${eventPath}`, {
        method: "POST",
        headers,
        credentials: "include",
        keepalive,
        body: JSON.stringify({
          ItemId: itemId,
          PositionTicks: Math.round(currentTime * TICKS_PER_SECOND),
          RunTimeTicks: Math.round(duration * TICKS_PER_SECOND),
          PlaySessionId: playbackSessionID.current,
          DeviceId: "jmlab-web",
          DeviceName: "JMlab Web",
          Client: "JMlab Web",
          IsPaused: video.paused && !video.ended,
        }),
      });
    } catch {
      // 页面关闭或网络切换时，播放事件失败不应打断用户播放。
    }
  }, [itemId, session?.accessToken, session?.csrfToken]);

  function chooseResume(choice: "continue" | "restart") {
    const video = videoRef.current;
    setResumeChoice(choice);
    if (choice === "continue") {
      pendingResumeTicks.current = resumeTicks;
      if (video && video.readyState >= 1 && Number.isFinite(video.duration)) {
        video.currentTime = Math.min(resumeTicks / TICKS_PER_SECOND, Math.max(0, video.duration - 1));
        pendingResumeTicks.current = null;
      }
    } else {
      pendingResumeTicks.current = null;
      if (video && video.readyState >= 1) video.currentTime = 0;
    }
  }

  function applyPendingResume(video: HTMLVideoElement) {
    if (pendingResumeTicks.current == null || !Number.isFinite(video.duration)) return;
    video.currentTime = Math.min(pendingResumeTicks.current / TICKS_PER_SECOND, Math.max(0, video.duration - 1));
    pendingResumeTicks.current = null;
  }

  async function resolvePlayback(nextSourceId = sourceId, refresh = false) {
    if (!itemId || !nextSourceId) return false;
    setResolving(true);
    setError("");
    try {
      const result = await apiFetch<PlaybackResolution>(`/api/v1/items/${encodeURIComponent(itemId)}/sources/${encodeURIComponent(nextSourceId)}/resolve`, { method: "POST", body: JSON.stringify({ quality, language, allowPreview, refresh }) });
      setResolution(result);
      setSourceId(result.source.id);
      setPlayerError("");
      return true;
    } catch (requestError) {
      setError(requestError instanceof Error ? requestError.message : "解析播放源失败");
      return false;
    } finally { setResolving(false); }
  }

  async function retryPlayback() {
    if (retrying) return;
    setRetrying(true);
    setPlayerError("");
    const currentID = resolution?.source.id || sourceId;
    let succeeded = await resolvePlayback(currentID, true);
    if (!succeeded) {
      const fallback = selectableSources.find((source) => source.id !== currentID);
      if (fallback) {
        setSourceId(fallback.id);
        succeeded = await resolvePlayback(fallback.id, true);
      }
    }
    if (!succeeded) setPlayerError("当前播放源不可用，请选择其他来源或稍后重试。");
    setRetrying(false);
  }

  useEffect(() => {
    if (!session) { navigate("/login", { replace: true }); return; }
    if (!itemId) return;
    let cancelled = false;
    setLoading(true); setError("");
    Promise.all([apiFetch<CatalogItem>(`/api/v1/items/${encodeURIComponent(itemId)}`), apiFetch<{ items: PlaybackSourceRecord[] }>(`/api/v1/items/${encodeURIComponent(itemId)}/sources`), apiFetch<PlaybackState>(`/api/v1/me/items/${encodeURIComponent(itemId)}/state`)]).then(([nextItem, sourceResult, nextState]) => {
      if (cancelled) return;
      const nextSources = sourceResult.items.slice().sort((a, b) => a.sortOrder - b.sortOrder || a.id.localeCompare(b.id));
      setItem(nextItem); setSources(nextSources);
      const savedTicks = Math.max(0, nextState.positionTicks || 0);
      const savedRuntime = Math.max(0, nextState.runtimeTicks || 0);
      const canResume = savedTicks >= TICKS_PER_SECOND * 5 && (!savedRuntime || savedTicks < savedRuntime * 0.9);
      setResumeTicks(savedTicks);
      setResumeChoice(canResume ? "pending" : "ready");
      pendingResumeTicks.current = null;
      const first = nextSources.find((source) => source.enabled && source.status !== "unavailable" && !source.isPreview) ?? nextSources.find((source) => source.enabled && source.status !== "unavailable");
      if (first) { setSourceId(first.id); void resolvePlayback(first.id); }
    }).catch((requestError) => { if (!cancelled) setError(requestError instanceof Error ? requestError.message : "读取播放信息失败"); }).finally(() => { if (!cancelled) setLoading(false); });
    return () => { cancelled = true; };
  }, [itemId, session?.accessToken, navigate]);

  useEffect(() => {
    const video = videoRef.current;
    if (!video || !resolution || resolution.protocol === "redirect") return;
    hlsRef.current?.destroy(); hlsRef.current = null;
    setPlayerError("");
    const nativeHLS = Boolean(video.canPlayType("application/vnd.apple.mpegurl") || video.canPlayType("application/x-mpegURL"));
    const hlsLibrary = window.Hls;
    if (resolution.protocol === "hls" && hlsLibrary?.isSupported() && !nativeHLS) {
      const hls = new hlsLibrary({ enableWorker: true, lowLatencyMode: false });
      hlsRef.current = hls;
      hls.attachMedia(video);
      hls.on(hlsLibrary.Events.MEDIA_ATTACHED, () => hls.loadSource(resolution.playbackUrl));
      hls.on(hlsLibrary.Events.ERROR, (_event: string, data?: { fatal?: boolean }) => { if (data?.fatal) setPlayerError("HLS 播放失败，可以重试或切换播放源。"); });
    } else {
      video.src = resolution.playbackUrl;
      video.load();
    }
    const handleError = () => setPlayerError("视频加载失败，可以重试或切换播放源。");
    video.addEventListener("error", handleError);
    return () => { video.removeEventListener("error", handleError); hlsRef.current?.destroy(); hlsRef.current = null; video.removeAttribute("src"); video.load(); };
  }, [resolution]);

  useEffect(() => {
    const video = videoRef.current;
    if (!video || !resolution || resolution.protocol === "redirect") return;
    const handleLoadedMetadata = () => applyPendingResume(video);
    const handlePlay = () => { void sendPlaybackEvent("Playing"); };
    const handlePause = () => { if (!video.ended) void sendPlaybackEvent("Playing/Progress"); };
    const handleEnded = () => { void sendPlaybackEvent("Playing/Stopped"); };
    video.addEventListener("loadedmetadata", handleLoadedMetadata);
    video.addEventListener("play", handlePlay);
    video.addEventListener("pause", handlePause);
    video.addEventListener("ended", handleEnded);
    return () => {
      video.removeEventListener("loadedmetadata", handleLoadedMetadata);
      video.removeEventListener("play", handlePlay);
      video.removeEventListener("pause", handlePause);
      video.removeEventListener("ended", handleEnded);
    };
  }, [resolution, sendPlaybackEvent]);

  useEffect(() => {
    if (!resolution || resolution.protocol === "redirect") return;
    const timer = window.setInterval(() => {
      const video = videoRef.current;
      if (video && !video.paused && !video.ended) void sendPlaybackEvent("Playing/Progress");
    }, 5_000);
    return () => window.clearInterval(timer);
  }, [resolution, sendPlaybackEvent]);

  useEffect(() => {
    const finishPlayback = () => { void sendPlaybackEvent("Playing/Stopped", true); };
    window.addEventListener("pagehide", finishPlayback);
    return () => {
      window.removeEventListener("pagehide", finishPlayback);
      finishPlayback();
    };
  }, [sendPlaybackEvent]);

  useEffect(() => {
    if (!resolution) return;
    const expiry = Date.parse(resolution.expiresAt);
    if (!Number.isFinite(expiry)) return;
    const delay = Math.max(5_000, expiry - Date.now() - 30_000);
    const timer = window.setTimeout(() => { void resolvePlayback(resolution.source.id, true); }, delay);
    return () => window.clearTimeout(timer);
  }, [resolution]);

  useEffect(() => {
    const handleShortcut = (event: KeyboardEvent) => {
      if (!resolution || isTextEntryTarget(event.target)) return;
      const video = videoRef.current;
      if (!video) return;
      if (event.key === "Escape") {
        if (resumeChoice === "pending") {
          event.preventDefault();
          setResumeChoice("ready");
        }
        return;
      }
      if (event.key === "Enter" && resumeChoice === "pending") {
        event.preventDefault();
        chooseResume("continue");
        return;
      }
      if (event.key === " ") {
        event.preventDefault();
        if (video.paused) void video.play(); else video.pause();
        return;
      }
      if (event.key === "ArrowLeft" || event.key === "ArrowRight") {
        event.preventDefault();
        const delta = event.key === "ArrowLeft" ? -5 : 5;
        const duration = Number.isFinite(video.duration) && video.duration > 0 ? video.duration : Number.POSITIVE_INFINITY;
        video.currentTime = Math.max(0, Math.min(duration, video.currentTime + delta));
        return;
      }
      if (event.key === "+" || event.key === "=") {
        event.preventDefault();
        video.volume = Math.min(1, Number((video.volume + 0.1).toFixed(2)));
        return;
      }
      if (event.key === "-") {
        event.preventDefault();
        video.volume = Math.max(0, Number((video.volume - 0.1).toFixed(2)));
        return;
      }
      if (event.key === "0") {
        event.preventDefault();
        video.currentTime = 0;
      }
    };
    window.addEventListener("keydown", handleShortcut);
    return () => window.removeEventListener("keydown", handleShortcut);
  }, [resolution, resumeChoice]);

  useEffect(() => {
    if (resumeChoice === "pending") {
      resumeOpenRef.current = true;
      window.requestAnimationFrame(() => resumeOverlayRef.current?.querySelector<HTMLElement>("[data-autofocus]")?.focus());
      return;
    }
    if (resumeOpenRef.current) {
      resumeOpenRef.current = false;
      videoRef.current?.focus();
    }
  }, [resumeChoice]);

  function handleResumeOverlayKeyDown(event: React.KeyboardEvent<HTMLDivElement>) {
    if (event.key === "Escape") {
      event.preventDefault();
      event.stopPropagation();
      setResumeChoice("ready");
      return;
    }
    if (event.key !== "Tab") return;
    const focusable = getFocusableElements(event.currentTarget);
    if (focusable.length === 0) { event.preventDefault(); return; }
    const currentIndex = focusable.indexOf(document.activeElement as HTMLElement);
    const nextIndex = event.shiftKey
      ? (currentIndex <= 0 ? focusable.length - 1 : currentIndex - 1)
      : (currentIndex === focusable.length - 1 ? 0 : currentIndex + 1);
    event.preventDefault();
    focusable[nextIndex]?.focus();
  }

  if (!session) return null;
  if (loading) return <section className="content-card play-page"><p className="eyebrow">播放器</p><h1>正在准备播放…</h1><div className="player-placeholder">正在读取作品和播放源</div></section>;
  if (error && !item) return <section className="content-card play-page"><p className="eyebrow">播放器</p><h1>无法打开播放页</h1><p className="play-error" role="alert">{error}</p><Link to={itemId ? `/items/${itemId}` : "/libraries"} className="ghost-button">返回详情</Link></section>;
  if (!item) return null;
  return (
    <section className="play-page">
      <div className="play-page-heading">
        <div>
          <p className="eyebrow">在线播放</p>
          <h1>{item.title}</h1>
          <p className="muted">{visibleCode(item) || mediaTypeLabel(item.itemType)} · {resolution?.source.providerName || "选择播放源"}</p>
        </div>
        <Link to={`/items/${item.id}`} className="ghost-button">返回详情</Link>
      </div>
      <div className="player-layout">
        <div className="player-card">
          <div className="video-frame" tabIndex={0} aria-label="播放器。空格播放或暂停，左右方向键快进或后退 5 秒，加减号调节音量，0 回到开头，Esc 关闭续播提示。">
            {resolution ? (
              resolution.protocol === "redirect" ? (
                <div className="external-player-view">
                  <div className="external-player-icon">🎬</div>
                  <h2>外部在线网页播放源</h2>
                  <p className="external-source-name">{resolution.source.label} · {resolution.source.providerName || "在线流媒体"}</p>
                  <p className="external-source-tip">
                    该播放源为第三方在线正片站点（如 MissAV / Jable / 123AV 等），受浏览器防盗链与 CORS 限制，请直接在新标签页中访问观看。
                  </p>
                  <div className="external-source-buttons">
                    <a
                      href={resolution.playbackUrl}
                      target="_blank"
                      rel="noopener noreferrer"
                      className="primary-button external-open-btn"
                    >
                      在新标签页打开观看 ↗
                    </a>
                    <button
                      type="button"
                      className="ghost-button"
                      onClick={() => {
                        void navigator.clipboard.writeText(resolution.playbackUrl);
                        alert("已复制播放地址到剪贴板");
                      }}
                    >
                      复制播放链接
                    </button>
                    {visibleCode(item) && (
                      <button
                        type="button"
                        className="ghost-button"
                        onClick={() => {
                          void navigator.clipboard.writeText(item.code);
                          alert("已复制内容标识: " + item.code);
                        }}
                      >
                        复制内容标识 ({visibleCode(item)})
                      </button>
                    )}
                  </div>
                </div>
              ) : (
                <>
                  <video
                    ref={(element) => {
                      videoRef.current = element;
                      if (element) lastVideoRef.current = element;
                    }}
                    aria-label={`播放 ${item.title}`}
                    controls
                    playsInline
                    preload="metadata"
                    onLoadedMetadata={() => {
                      if (videoRef.current) applyPendingResume(videoRef.current);
                    }}
                    onError={() => setPlayerError("视频加载失败，可以重试或切换播放源。")}
                  >
                    {resolution.subtitles?.map((subtitle, index) => (
                      <track
                        key={`${subtitle.url}-${index}`}
                        kind="subtitles"
                        src={subtitle.url}
                        srcLang={subtitle.language || "zh"}
                        label={subtitle.label || subtitle.language || `字幕 ${index + 1}`}
                        default={index === 0}
                      />
                    ))}
                  </video>
                  {resumeChoice === "pending" && (
                    <div
                      ref={resumeOverlayRef}
                      className="resume-overlay"
                      role="dialog"
                      aria-modal="true"
                      aria-labelledby="resume-title"
                      aria-describedby="resume-description"
                      onKeyDown={handleResumeOverlayKeyDown}
                    >
                      <p id="resume-title">继续播放？</p>
                      <p id="resume-description">
                        检测到上次播放进度：{Math.floor(resumeTicks / TICKS_PER_SECOND / 60)} 分 {Math.floor(resumeTicks / TICKS_PER_SECOND) % 60} 秒
                      </p>
                      <div className="resume-actions">
                        <button type="button" data-autofocus className="primary-button compact-button" onClick={() => chooseResume("continue")}>继续播放</button>
                        <button type="button" className="ghost-button compact-button" onClick={() => chooseResume("restart")}>从头播放</button>
                      </div>
                    </div>
                  )}
                </>
              )
            ) : (
              <div className="player-placeholder">{selectableSources.length ? "选择播放源后开始播放" : "暂无可播放来源"}</div>
            )}
          </div>
          {(playerError || error) && <div className="play-error" role="alert">{playerError || error}</div>}
          <div className="player-toolbar">
            <span className="play-meta">
              {resolution ? `${resolution.protocol.toUpperCase()} · ${resolution.directPlay ? "直连" : "网关代理"}` : "未解析"}
              {resolution?.isPreview ? " · 预览" : ""}
            </span>
            <span className="player-shortcut-hint">快捷键：空格播放/暂停 · ←/→ ±5 秒 · +/- 音量 · 0 回到开头</span>
            <button
              type="button"
              className="ghost-button compact-button"
              disabled={resolving || retrying || !sourceId}
              onClick={() => void retryPlayback()}
            >
              {retrying ? "重试中…" : "重试/刷新"}
            </button>
          </div>
        </div>
        <aside className="source-picker">
          <div className="source-picker-heading">
            <div>
              <p className="eyebrow">播放源</p>
              <h2>{selectableSources.length} 个可用来源</h2>
            </div>
            {resolving && <span className="muted">解析中…</span>}
          </div>
          <label className="inline-check">
            <input
              type="checkbox"
              checked={allowPreview}
              onChange={(event) => { setAllowPreview(event.target.checked); setResolution(null); }}
            />
            显示预览/预告片
          </label>
          {qualities.length > 0 && (
            <label>
              画质
              <select value={quality} onChange={(event) => setQuality(event.target.value)}>
                <option value="">自动选择</option>
                {qualities.map((value) => <option value={value} key={value}>{value}</option>)}
              </select>
            </label>
          )}
          {languages.length > 0 && (
            <label>
              语言
              <select value={language} onChange={(event) => setLanguage(event.target.value)}>
                <option value="">自动选择</option>
                {languages.map((value) => <option value={value} key={value}>{value}</option>)}
              </select>
            </label>
          )}
          <div className="source-options">
            {selectableSources.map((source) => (
              <button
                type="button"
                className={`source-option${source.id === resolution?.source.id ? " active" : ""}`}
                key={source.id}
                disabled={resolving}
                onClick={() => { setSourceId(source.id); void resolvePlayback(source.id); }}
              >
                <span>
                  <strong>{source.label}</strong>
                  <small>{source.providerName || source.providerId} · {source.protocol.toUpperCase()}{source.quality ? ` · ${source.quality}` : ""}{source.language ? ` · ${source.language}` : ""}</small>
                </span>
                <em>{source.isPreview ? "预览" : source.status === "expired" ? "过期" : "可用"}</em>
              </button>
            ))}
          </div>
          {selectableSources.length === 0 && <p className="muted">请返回详情或联系管理员配置在线来源。</p>}
        </aside>
      </div>
      <div className="play-meta">
        <span>来源：{resolution?.source.providerName || "—"}</span>
        <span>过期：{resolution?.expiresAt ? new Date(resolution.expiresAt).toLocaleString() : "—"}</span>
        <span>字幕：{resolution?.subtitles?.length ? `${resolution.subtitles.length} 条` : "无"}</span>
      </div>
    </section>
  );
}

