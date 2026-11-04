import { type IconName } from "../components/Icon";
import type { PlaybackSourceRecord } from "./models";
import { type CatalogItem } from "./models";
import { useEffect } from "react";

export type HlsInstance = {
  attachMedia: (media: HTMLVideoElement) => void;
  loadSource: (url: string) => void;
  destroy: () => void;
  on: (event: string, handler: (event: string, data?: { fatal?: boolean }) => void) => void;
};

export type HlsLibrary = {
  new (options: { enableWorker: boolean; lowLatencyMode: boolean }): HlsInstance;
  isSupported: () => boolean;
  Events: { MEDIA_ATTACHED: string; ERROR: string };
};

declare global {
  interface Window {
    Hls?: HlsLibrary;
  }
}

export const navigation = [
  { to: "/", label: "首页", icon: "home" as IconName },
  { to: "/search", label: "搜索", icon: "search" as IconName },
  { to: "/rankings", label: "排行", icon: "ranking" as IconName },
  { to: "/libraries", label: "媒体库", icon: "library" as IconName },
  { to: "/playlists", label: "我的清单", icon: "list" as IconName },
];

export const homeModuleOrder = ["recentSync", "recentRelease", "topRated", "popular", "playable", "recentlyPlayed"] as const;

export const homeModuleLabels: Record<string, string> = { recentSync: "最近同步", recentRelease: "最近发行", topRated: "高分作品", popular: "热门作品", playable: "有在线源", recentlyPlayed: "最近播放" };

export const preferenceModuleKeys = [...homeModuleOrder, "ranking"] as const;

export const preferenceModuleLabels: Record<string, string> = { ...homeModuleLabels, ranking: "模块榜单", playback: "热播", top250: "TOP 250", hot: "热播（兼容）" };

export const defaultModuleFlags: Record<string, boolean> = Object.fromEntries(preferenceModuleKeys.map((key) => [key, true]));

export type ThemePreference = { mode: "dark" | "light"; accent: "blue" | "mint" | "amber"; columns: "auto" | "2" | "3" | "4" | "5"; posterRatio: "portrait" | "square" | "wide"; backgroundBlur: boolean; liquid: boolean; card3d: boolean; showOriginalTitle: boolean };

export const defaultThemePreference: ThemePreference = { mode: "dark", accent: "blue", columns: "auto", posterRatio: "portrait", backgroundBlur: true, liquid: true, card3d: true, showOriginalTitle: false };

export function normalizeTheme(value: Record<string, unknown> | undefined): ThemePreference {
  return {
    ...defaultThemePreference,
    ...value,
    mode: value?.mode === "light" ? "light" : "dark",
    accent: value?.accent === "mint" || value?.accent === "amber" ? value.accent : "blue",
    columns: value?.columns === "2" || value?.columns === "3" || value?.columns === "4" || value?.columns === "5" ? value.columns : "auto",
    posterRatio: value?.posterRatio === "square" || value?.posterRatio === "wide" ? value.posterRatio : "portrait",
    backgroundBlur: value?.backgroundBlur !== false,
    liquid: value?.liquid !== false,
    card3d: value?.card3d !== false,
    showOriginalTitle: value?.showOriginalTitle === true,
  };
}

export function applyThemeToDocument(theme: ThemePreference) {
  document.documentElement.dataset.theme = theme.mode;
  document.documentElement.dataset.accent = theme.accent;
  document.documentElement.dataset.columns = theme.columns;
  document.documentElement.dataset.posterRatio = theme.posterRatio;
  document.documentElement.dataset.backgroundBlur = String(theme.backgroundBlur);
  document.documentElement.dataset.liquid = String(theme.liquid);
  document.documentElement.dataset.card3d = String(theme.card3d);
  document.documentElement.dataset.showOriginalTitle = String(theme.showOriginalTitle);
}

export function isTextEntryTarget(target: EventTarget | null): boolean {
  const element = target instanceof HTMLElement ? target : null;
  if (!element) return false;
  return element.isContentEditable || Boolean(element.closest("input, textarea, select, [contenteditable='true']"));
}

export function focusAdjacentCard(direction: -1 | 1): boolean {
  const active = document.activeElement;
  if (!(active instanceof HTMLElement)) return false;
  const cards = Array.from(document.querySelectorAll<HTMLElement>(".item-card, .ranking-card, .ranking-list-item, .source-option, .relation-card"));
  const index = cards.indexOf(active);
  if (index < 0 || cards.length < 2) return false;
  const next = cards[(index + direction + cards.length) % cards.length];
  next?.focus();
  return Boolean(next);
}

export function getFocusableElements(container: HTMLElement): HTMLElement[] {
  return Array.from(container.querySelectorAll<HTMLElement>([
    "a[href]",
    "button:not([disabled])",
    "input:not([disabled])",
    "select:not([disabled])",
    "textarea:not([disabled])",
    "video[controls]",
    "[tabindex]:not([tabindex='-1'])",
  ].join(","))).filter((element) => !element.hasAttribute("aria-hidden"));
}

export type ItemImage = { id: string; itemId: string; imageType: "gallery" | "preview" | "poster" | "backdrop" | "avatar"; url: string; sourceLabel?: string; width: number; height: number; mimeType: string; status: "pending" | "ready" | "error"; sortOrder: number; sourcePriority: number; sha256?: string; updatedAt: string; errorMessage?: string; lastAttemptAt?: string; fetchDurationMs?: number; isFavorite: boolean };

export type HomeModule = { title: string; items: CatalogItem[] };

export type HomeDashboard = { generatedAt: string; libraryId?: string; modules: Record<string, HomeModule>; rankings: Record<string, Array<{ rank: number; item: CatalogItem }>>; stats: { visibleItems: number; playableItems: number; people: number; studios: number; series: number; tags: number } };

export type SearchHint = { kind: "item" | "person" | "studio" | "series" | "tag"; id: string; label: string; code?: string; title?: string; subtitle?: string };

export type DistributionBucket = { key: string; label: string; count: number };

export type PreferencesData = { theme: Record<string, unknown>; moduleFlags: Record<string, unknown>; locale: string; adultConfirmedAt?: string };

export type TaskAttemptRecord = { id: string; attemptNumber: number; status: string; errorCode?: string; errorMessage?: string; startedAt: string; finishedAt?: string };

export type TaskRecord = { id: string; type: string; status: string; progress: number; retryCount: number; maxRetries: number; errorCode?: string; errorMessage?: string; startedAt?: string; finishedAt?: string; createdAt: string; updatedAt: string; attempts?: TaskAttemptRecord[]; canRetry?: boolean; canCancel?: boolean };

export type BackupRunTargetRecord = { targetId: string; targetType: string; displayName: string; status: string; artifactPath?: string; bytesWritten: number; checksum?: string; errorMessage?: string; startedAt?: string; completedAt?: string };

export type BackupRunRecord = { id: string; taskId: string; taskStatus: string; includeUserState: boolean; includeEncryptedConfig: boolean; artifactName?: string; artifactSha256?: string; artifactSize?: number; createdAt: string; completedAt?: string; targets: BackupRunTargetRecord[] };

export type VirtualLibraryRecord = { id: string; name: string; description?: string; itemType: "movie" | "collection" | "people" | "mixed"; filter: Record<string, unknown>; sort: { field: string; direction: string }; posterImageId?: string; isPublic: boolean; isAdult: boolean; enabled: boolean; permissions?: Array<{ subjectType: string; subjectId: string; name?: string; canView: boolean; canPlay: boolean }> };

export type CollectionRecord = { id: string; name: string; description?: string; source: string; sourceId?: string; posterImageId?: string; ownerUserId?: string; isPublic: boolean; itemCount: number; createdAt: string; updatedAt: string };

export type CollectionItemRecord = { itemId: string; code: string; title: string; position: number; addedByUserId?: string; createdAt: string };

export type AdminDashboardData = { generatedAt: string; libraryId?: string; resources: { items: number; playableItems: number; people: number; studios: number; series: number; tags: number; collections: number; libraries: number; providers: number; playbackSources: number }; tasks: Array<{ status: string; count: number }>; recentTasks: TaskRecord[]; errors: { taskFailures: number; auditFailures: number; total: number }; recentErrors: Array<{ source: string; id: string; code?: string; message?: string; occurredAt: string; resourceType?: string; resourceId?: string }>; providerHealth: { healthy: number; unhealthy: number; unknown: number }; online: { sessions: number; users: number; activePlayback: number }; health: { database: string; service: string } };

export type AuditLogRecord = { id: string; actorUserId?: string; actorUsername?: string; actorName?: string; action: string; resourceType: string; resourceId?: string; requestId?: string; result: string; metadata: unknown; createdAt: string };

export type StatisticsOverviewData = { generatedAt: string; libraryId?: string; stats: { visibleItems: number; playableItems: number; people: number; studios: number; series: number; tags: number }; ratingDistribution: DistributionBucket[]; yearDistribution: DistributionBucket[]; watchedItems: number; favoriteItems: number; watchedDurationSeconds: number; sourceHealth: { active: number; unavailable: number; expired: number; probeCount: number; probeSuccessCount: number; successRate: number } };

export type StatisticsItemsData = { generatedAt: string; libraryId?: string; limit: number; stats: StatisticsOverviewData["stats"]; byYear: DistributionBucket[]; byRating: DistributionBucket[]; people: Array<{ id: string; name: string; count: number }>; studios: Array<{ id: string; name: string; count: number }>; series: Array<{ id: string; name: string; count: number }>; tags: Array<{ id: string; name: string; count: number }> };

export type PlaybackSubtitle = { url: string; language?: string; label?: string };

export type PlaybackResolution = { source: PlaybackSourceRecord; playbackUrl: string; protocol: "hls" | "http" | "redirect"; expiresAt: string; directPlay: boolean; requiresProxy: boolean; isPreview: boolean; subtitles?: PlaybackSubtitle[] };

export type PlaybackState = { positionTicks: number; runtimeTicks: number; isWatched: boolean; playbackState?: "playing" | "paused" | "stopped"; playbackPercent?: number };

export type RelationKind = "people" | "studios" | "series" | "tags";

export type RelationEntity = { id: string; kind: RelationKind; name: string; aliases?: string[]; sourceName?: string; sourceUrl?: string; gender?: string; status?: string; birthday?: string; heightCm?: number; cupSize?: string; measurements?: Record<string, string>; biography?: string; parentId?: string; tagKind?: string; isExcluded: boolean; relatedItems?: RelatedItem[] };

export type RelatedItem = { id: string; code: string; title: string };

export type SearchState = { query: string; submittedQuery: string; actorIds: string[]; directorIds: string[]; studioIds: string[]; seriesIds: string[]; tagIds: string[]; excludeTagIds: string[]; years: string; minRating: string; favoriteFilter: string; watchedFilter: string; playableFilter: string };

export const relationLabels: Record<RelationKind, string> = { people: "人物", studios: "片商", series: "系列", tags: "标签" };

export const searchHintLabels: Record<SearchHint["kind"], string> = { item: "作品", person: "人物", studio: "片商", series: "系列", tag: "标签" };

export function useScrollRestoration(key: string) {
  useEffect(() => {
    const storageKey = `jmlab.scroll.${key}`;
    const saved = Number(sessionStorage.getItem(storageKey) ?? "0");
    if (saved > 0) window.requestAnimationFrame(() => window.scrollTo({ top: saved, behavior: "instant" as ScrollBehavior }));
    const save = () => sessionStorage.setItem(storageKey, String(Math.max(0, Math.round(window.scrollY))));
    window.addEventListener("pagehide", save);
    return () => { save(); window.removeEventListener("pagehide", save); };
  }, [key]);
}

export function readSearchState(): SearchState {
  const fallback: SearchState = { query: "", submittedQuery: "", actorIds: [], directorIds: [], studioIds: [], seriesIds: [], tagIds: [], excludeTagIds: [], years: "", minRating: "", favoriteFilter: "", watchedFilter: "", playableFilter: "" };
  try {
    const raw = sessionStorage.getItem("jmlab.search-state");
    return raw ? { ...fallback, ...(JSON.parse(raw) as Partial<SearchState>) } : fallback;
  } catch { return fallback; }
}

export function readSearchHistory(): string[] {
  try {
    const raw = sessionStorage.getItem("jmlab.search-history");
    const value = raw ? JSON.parse(raw) : [];
    return Array.isArray(value) ? value.filter((item): item is string => typeof item === "string").slice(0, 8) : [];
  } catch { return []; }
}

export function emptySearchState(): SearchState {
  return { query: "", submittedQuery: "", actorIds: [], directorIds: [], studioIds: [], seriesIds: [], tagIds: [], excludeTagIds: [], years: "", minRating: "", favoriteFilter: "", watchedFilter: "", playableFilter: "" };
}

export function splitSearchParam(value: string | null): string[] {
  return value ? value.split(",").map((item) => item.trim()).filter(Boolean) : [];
}

export function readSearchStateFromURL(): SearchState | null {
  const params = new URLSearchParams(window.location.search);
  const keys = ["q", "actorIds", "directorIds", "studioIds", "seriesIds", "tagIds", "excludeTagIds", "years", "minRating", "isFavorite", "isWatched", "hasPlayableSource"];
  if (!keys.some((key) => params.has(key))) return null;
  return {
    ...emptySearchState(),
    query: params.get("q") ?? "",
    submittedQuery: "__filters__",
    actorIds: splitSearchParam(params.get("actorIds")),
    directorIds: splitSearchParam(params.get("directorIds")),
    studioIds: splitSearchParam(params.get("studioIds")),
    seriesIds: splitSearchParam(params.get("seriesIds")),
    tagIds: splitSearchParam(params.get("tagIds")),
    excludeTagIds: splitSearchParam(params.get("excludeTagIds")),
    years: params.get("years") ?? "",
    minRating: params.get("minRating") ?? "",
    favoriteFilter: params.get("isFavorite") ?? "",
    watchedFilter: params.get("isWatched") ?? "",
    playableFilter: params.get("hasPlayableSource") ?? "",
  };
}

export function buildSearchParams(state: SearchState): URLSearchParams {
  const params = new URLSearchParams();
  const values: Array<[string, string]> = [["q", state.query.trim()], ["actorIds", state.actorIds.join(",")], ["directorIds", state.directorIds.join(",")], ["studioIds", state.studioIds.join(",")], ["seriesIds", state.seriesIds.join(",")], ["tagIds", state.tagIds.join(",")], ["excludeTagIds", state.excludeTagIds.join(",")], ["years", state.years.replace(/[，\s]+/g, ",")], ["minRating", state.minRating], ["isFavorite", state.favoriteFilter], ["isWatched", state.watchedFilter], ["hasPlayableSource", state.playableFilter]];
  values.forEach(([key, value]) => { if (value) params.set(key, value); });
  return params;
}

export const taskStatusLabels: Record<string, string> = { queued: "排队中", running: "运行中", succeeded: "已完成", partial: "部分完成", failed: "失败", cancelled: "已取消" };

