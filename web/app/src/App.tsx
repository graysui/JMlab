import { ModuleRankingLinks } from "./components/ModuleRankingLinks";
import { LibraryTemplatePicker, type LibraryTemplateSelection, type ResolvedLibraryTemplate } from "./components/LibraryTemplatePicker";
import { Icon, type IconName } from "./components/Icon";
import { BackupAdminPage } from "./pages/BackupAdminPage";
import { SourceAdminPage } from "./pages/SourceAdminPage";
import type { PlaybackSourceRecord } from "./lib/models";
import { apiFetch, readSession, saveSession, userHasRole, type User, type SessionData } from "./lib/api";
import { type CatalogItem, type Relation, mediaTypeLabel, visibleCode } from "./lib/models";
import { ModuleCenterPage } from "./pages/ModuleCenterPage";
import { DiscoverPage, MediaChildren } from "./pages/DiscoverPage";
import { PlaylistsPage, AddToPlaylist } from "./pages/PlaylistsPage";
import { MediaStatePanel } from "./pages/MediaStatePanel";
import { LibraryBrowser } from "./pages/LibraryBrowser";
import { SettingsCenterPage } from "./pages/SettingsCenterPage";
import { settingsEntries } from "./lib/settings";
import { Component, FormEvent, type ErrorInfo, type PointerEvent as ReactPointerEvent, type ReactNode, useCallback, useEffect, useRef, useState } from "react";
import { NavLink, Route, Routes, Link, Navigate, useNavigate, useParams, useLocation, useSearchParams } from "react-router-dom";

type HlsInstance = {
  attachMedia: (media: HTMLVideoElement) => void;
  loadSource: (url: string) => void;
  destroy: () => void;
  on: (event: string, handler: (event: string, data?: { fatal?: boolean }) => void) => void;
};
type HlsLibrary = {
  new (options: { enableWorker: boolean; lowLatencyMode: boolean }): HlsInstance;
  isSupported: () => boolean;
  Events: { MEDIA_ATTACHED: string; ERROR: string };
};
declare global {
  interface Window {
    Hls?: HlsLibrary;
  }
}

const navigation = [
  { to: "/", label: "首页", icon: "home" as IconName },
  { to: "/search", label: "搜索", icon: "search" as IconName },
  { to: "/rankings", label: "排行", icon: "ranking" as IconName },
  { to: "/libraries", label: "媒体库", icon: "library" as IconName },
  { to: "/playlists", label: "我的清单", icon: "list" as IconName },
];

const homeModuleOrder = ["recentSync", "recentRelease", "topRated", "popular", "playable", "recentlyPlayed"] as const;
const homeModuleLabels: Record<string, string> = { recentSync: "最近同步", recentRelease: "最近发行", topRated: "高分作品", popular: "热门作品", playable: "有在线源", recentlyPlayed: "最近播放" };
const preferenceModuleKeys = [...homeModuleOrder, "ranking"] as const;
const preferenceModuleLabels: Record<string, string> = { ...homeModuleLabels, ranking: "模块榜单", playback: "热播", top250: "TOP 250", hot: "热播（兼容）" };
const defaultModuleFlags: Record<string, boolean> = Object.fromEntries(preferenceModuleKeys.map((key) => [key, true]));
type ThemePreference = { mode: "dark" | "light"; accent: "blue" | "mint" | "amber"; columns: "auto" | "2" | "3" | "4" | "5"; posterRatio: "portrait" | "square" | "wide"; backgroundBlur: boolean; liquid: boolean; card3d: boolean; showOriginalTitle: boolean };
const defaultThemePreference: ThemePreference = { mode: "dark", accent: "blue", columns: "auto", posterRatio: "portrait", backgroundBlur: true, liquid: true, card3d: true, showOriginalTitle: false };

function normalizeTheme(value: Record<string, unknown> | undefined): ThemePreference {
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

function applyThemeToDocument(theme: ThemePreference) {
  document.documentElement.dataset.theme = theme.mode;
  document.documentElement.dataset.accent = theme.accent;
  document.documentElement.dataset.columns = theme.columns;
  document.documentElement.dataset.posterRatio = theme.posterRatio;
  document.documentElement.dataset.backgroundBlur = String(theme.backgroundBlur);
  document.documentElement.dataset.liquid = String(theme.liquid);
  document.documentElement.dataset.card3d = String(theme.card3d);
  document.documentElement.dataset.showOriginalTitle = String(theme.showOriginalTitle);
}

function isTextEntryTarget(target: EventTarget | null): boolean {
  const element = target instanceof HTMLElement ? target : null;
  if (!element) return false;
  return element.isContentEditable || Boolean(element.closest("input, textarea, select, [contenteditable='true']"));
}

function focusAdjacentCard(direction: -1 | 1): boolean {
  const active = document.activeElement;
  if (!(active instanceof HTMLElement)) return false;
  const cards = Array.from(document.querySelectorAll<HTMLElement>(".item-card, .ranking-card, .ranking-list-item, .source-option, .relation-card"));
  const index = cards.indexOf(active);
  if (index < 0 || cards.length < 2) return false;
  const next = cards[(index + direction + cards.length) % cards.length];
  next?.focus();
  return Boolean(next);
}

function getFocusableElements(container: HTMLElement): HTMLElement[] {
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

type ItemImage = { id: string; itemId: string; imageType: "gallery" | "preview" | "poster" | "backdrop" | "avatar"; url: string; sourceLabel?: string; width: number; height: number; mimeType: string; status: "pending" | "ready" | "error"; sortOrder: number; sourcePriority: number; sha256?: string; updatedAt: string; errorMessage?: string; lastAttemptAt?: string; fetchDurationMs?: number; isFavorite: boolean };
type HomeModule = { title: string; items: CatalogItem[] };
type HomeDashboard = { generatedAt: string; libraryId?: string; modules: Record<string, HomeModule>; rankings: Record<string, Array<{ rank: number; item: CatalogItem }>>; stats: { visibleItems: number; playableItems: number; people: number; studios: number; series: number; tags: number } };
type SearchHint = { kind: "item" | "person" | "studio" | "series" | "tag"; id: string; label: string; code?: string; title?: string; subtitle?: string };
type DistributionBucket = { key: string; label: string; count: number };
type PreferencesData = { theme: Record<string, unknown>; moduleFlags: Record<string, unknown>; locale: string; adultConfirmedAt?: string };
type TaskAttemptRecord = { id: string; attemptNumber: number; status: string; errorCode?: string; errorMessage?: string; startedAt: string; finishedAt?: string };
type TaskRecord = { id: string; type: string; status: string; progress: number; retryCount: number; maxRetries: number; errorCode?: string; errorMessage?: string; startedAt?: string; finishedAt?: string; createdAt: string; updatedAt: string; attempts?: TaskAttemptRecord[]; canRetry?: boolean; canCancel?: boolean };
type BackupRunTargetRecord = { targetId: string; targetType: string; displayName: string; status: string; artifactPath?: string; bytesWritten: number; checksum?: string; errorMessage?: string; startedAt?: string; completedAt?: string };
type BackupRunRecord = { id: string; taskId: string; taskStatus: string; includeUserState: boolean; includeEncryptedConfig: boolean; artifactName?: string; artifactSha256?: string; artifactSize?: number; createdAt: string; completedAt?: string; targets: BackupRunTargetRecord[] };
type VirtualLibraryRecord = { id: string; name: string; description?: string; itemType: "movie" | "collection" | "people" | "mixed"; filter: Record<string, unknown>; sort: { field: string; direction: string }; posterImageId?: string; isPublic: boolean; isAdult: boolean; enabled: boolean; permissions?: Array<{ subjectType: string; subjectId: string; name?: string; canView: boolean; canPlay: boolean }> };
type CollectionRecord = { id: string; name: string; description?: string; source: string; sourceId?: string; posterImageId?: string; ownerUserId?: string; isPublic: boolean; itemCount: number; createdAt: string; updatedAt: string };
type CollectionItemRecord = { itemId: string; code: string; title: string; position: number; addedByUserId?: string; createdAt: string };
type AdminDashboardData = { generatedAt: string; libraryId?: string; resources: { items: number; playableItems: number; people: number; studios: number; series: number; tags: number; collections: number; libraries: number; providers: number; playbackSources: number }; tasks: Array<{ status: string; count: number }>; recentTasks: TaskRecord[]; errors: { taskFailures: number; auditFailures: number; total: number }; recentErrors: Array<{ source: string; id: string; code?: string; message?: string; occurredAt: string; resourceType?: string; resourceId?: string }>; providerHealth: { healthy: number; unhealthy: number; unknown: number }; online: { sessions: number; users: number; activePlayback: number }; health: { database: string; service: string } };
type AuditLogRecord = { id: string; actorUserId?: string; actorUsername?: string; actorName?: string; action: string; resourceType: string; resourceId?: string; requestId?: string; result: string; metadata: unknown; createdAt: string };
type StatisticsOverviewData = { generatedAt: string; libraryId?: string; stats: { visibleItems: number; playableItems: number; people: number; studios: number; series: number; tags: number }; ratingDistribution: DistributionBucket[]; yearDistribution: DistributionBucket[]; watchedItems: number; favoriteItems: number; watchedDurationSeconds: number; sourceHealth: { active: number; unavailable: number; expired: number; probeCount: number; probeSuccessCount: number; successRate: number } };
type StatisticsItemsData = { generatedAt: string; libraryId?: string; limit: number; stats: StatisticsOverviewData["stats"]; byYear: DistributionBucket[]; byRating: DistributionBucket[]; people: Array<{ id: string; name: string; count: number }>; studios: Array<{ id: string; name: string; count: number }>; series: Array<{ id: string; name: string; count: number }>; tags: Array<{ id: string; name: string; count: number }> };

type PlaybackSubtitle = { url: string; language?: string; label?: string };
type PlaybackResolution = { source: PlaybackSourceRecord; playbackUrl: string; protocol: "hls" | "http" | "redirect"; expiresAt: string; directPlay: boolean; requiresProxy: boolean; isPreview: boolean; subtitles?: PlaybackSubtitle[] };
type PlaybackState = { positionTicks: number; runtimeTicks: number; isWatched: boolean; playbackState?: "playing" | "paused" | "stopped"; playbackPercent?: number };
type RelationKind = "people" | "studios" | "series" | "tags";
type RelationEntity = { id: string; kind: RelationKind; name: string; aliases?: string[]; sourceName?: string; sourceUrl?: string; gender?: string; status?: string; birthday?: string; heightCm?: number; cupSize?: string; measurements?: Record<string, string>; biography?: string; parentId?: string; tagKind?: string; isExcluded: boolean; relatedItems?: RelatedItem[] };
type RelatedItem = { id: string; code: string; title: string };
type SearchState = { query: string; submittedQuery: string; actorIds: string[]; directorIds: string[]; studioIds: string[]; seriesIds: string[]; tagIds: string[]; excludeTagIds: string[]; years: string; minRating: string; favoriteFilter: string; watchedFilter: string; playableFilter: string };

const relationLabels: Record<RelationKind, string> = { people: "人物", studios: "片商", series: "系列", tags: "标签" };
const searchHintLabels: Record<SearchHint["kind"], string> = { item: "作品", person: "人物", studio: "片商", series: "系列", tag: "标签" };



function useScrollRestoration(key: string) {
  useEffect(() => {
    const storageKey = `jmlab.scroll.${key}`;
    const saved = Number(sessionStorage.getItem(storageKey) ?? "0");
    if (saved > 0) window.requestAnimationFrame(() => window.scrollTo({ top: saved, behavior: "instant" as ScrollBehavior }));
    const save = () => sessionStorage.setItem(storageKey, String(Math.max(0, Math.round(window.scrollY))));
    window.addEventListener("pagehide", save);
    return () => { save(); window.removeEventListener("pagehide", save); };
  }, [key]);
}

function readSearchState(): SearchState {
  const fallback: SearchState = { query: "", submittedQuery: "", actorIds: [], directorIds: [], studioIds: [], seriesIds: [], tagIds: [], excludeTagIds: [], years: "", minRating: "", favoriteFilter: "", watchedFilter: "", playableFilter: "" };
  try {
    const raw = sessionStorage.getItem("jmlab.search-state");
    return raw ? { ...fallback, ...(JSON.parse(raw) as Partial<SearchState>) } : fallback;
  } catch { return fallback; }
}

function readSearchHistory(): string[] {
  try {
    const raw = sessionStorage.getItem("jmlab.search-history");
    const value = raw ? JSON.parse(raw) : [];
    return Array.isArray(value) ? value.filter((item): item is string => typeof item === "string").slice(0, 8) : [];
  } catch { return []; }
}

function emptySearchState(): SearchState {
  return { query: "", submittedQuery: "", actorIds: [], directorIds: [], studioIds: [], seriesIds: [], tagIds: [], excludeTagIds: [], years: "", minRating: "", favoriteFilter: "", watchedFilter: "", playableFilter: "" };
}

function splitSearchParam(value: string | null): string[] {
  return value ? value.split(",").map((item) => item.trim()).filter(Boolean) : [];
}

function readSearchStateFromURL(): SearchState | null {
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

function buildSearchParams(state: SearchState): URLSearchParams {
  const params = new URLSearchParams();
  const values: Array<[string, string]> = [["q", state.query.trim()], ["actorIds", state.actorIds.join(",")], ["directorIds", state.directorIds.join(",")], ["studioIds", state.studioIds.join(",")], ["seriesIds", state.seriesIds.join(",")], ["tagIds", state.tagIds.join(",")], ["excludeTagIds", state.excludeTagIds.join(",")], ["years", state.years.replace(/[，\s]+/g, ",")], ["minRating", state.minRating], ["isFavorite", state.favoriteFilter], ["isWatched", state.watchedFilter], ["hasPlayableSource", state.playableFilter]];
  values.forEach(([key, value]) => { if (value) params.set(key, value); });
  return params;
}



function Layout() {
  const [session, setSession] = useState<SessionData | null>(() => readSession());
  const [themeMenu, setThemeMenu] = useState<{ x: number; y: number } | null>(null);
  const [themeMenuBusy, setThemeMenuBusy] = useState(false);
  const [themeMenuError, setThemeMenuError] = useState("");
  const themeMenuRef = useRef<HTMLDivElement | null>(null);
  const themeMenuTriggerRef = useRef<HTMLButtonElement | null>(null);
  const restoreThemeMenuFocusRef = useRef(false);
  useEffect(() => { const sync = () => setSession(readSession()); window.addEventListener("jmlab-session-change", sync); return () => window.removeEventListener("jmlab-session-change", sync); }, []);
  useEffect(() => {
    const syncTheme = () => { applyThemeToDocument((window as Window & { jmlabTheme?: ThemePreference }).jmlabTheme ?? defaultThemePreference); };
    window.addEventListener("jmlab-preferences-change", syncTheme);
    if (!session) { applyThemeToDocument(defaultThemePreference); return () => window.removeEventListener("jmlab-preferences-change", syncTheme); }
    apiFetch<PreferencesData>("/api/v1/me/preferences").then((preferences) => {
      const theme = normalizeTheme(preferences.theme);
      (window as Window & { jmlabTheme?: ThemePreference }).jmlabTheme = theme;
      applyThemeToDocument(theme);
    }).catch(() => applyThemeToDocument(defaultThemePreference));
    return () => window.removeEventListener("jmlab-preferences-change", syncTheme);
  }, [session?.accessToken]);
  useEffect(() => {
    const closeMenu = (event: PointerEvent) => {
      if (themeMenuRef.current && event.target instanceof Node && themeMenuRef.current.contains(event.target)) return;
      closeThemeMenu(false);
    };
    const handleShortcut = (event: KeyboardEvent) => {
      if (event.key === "Escape" && themeMenu) {
        event.preventDefault();
        closeThemeMenu(true);
        return;
      }
      if (isTextEntryTarget(event.target)) return;
      if (event.key === "ArrowLeft") {
        if (focusAdjacentCard(-1)) event.preventDefault();
      } else if (event.key === "ArrowRight") {
        if (focusAdjacentCard(1)) event.preventDefault();
      }
    };
    window.addEventListener("pointerdown", closeMenu);
    window.addEventListener("keydown", handleShortcut);
    return () => { window.removeEventListener("pointerdown", closeMenu); window.removeEventListener("keydown", handleShortcut); };
  }, [themeMenu]);

  useEffect(() => {
    if (themeMenu) {
      window.requestAnimationFrame(() => themeMenuRef.current?.querySelector<HTMLElement>("[role='menuitem']")?.focus());
      return;
    }
    if (restoreThemeMenuFocusRef.current) {
      themeMenuTriggerRef.current?.focus();
      restoreThemeMenuFocusRef.current = false;
    }
  }, [themeMenu]);

  function openThemeMenu(clientX: number, clientY: number) {
    const width = 220;
    const height = 188;
    restoreThemeMenuFocusRef.current = false;
    setThemeMenu({ x: Math.min(Math.max(8, clientX), Math.max(8, window.innerWidth - width - 8)), y: Math.min(Math.max(8, clientY), Math.max(8, window.innerHeight - height - 8)) });
    setThemeMenuError("");
  }

  function closeThemeMenu(restoreFocus: boolean) {
    restoreThemeMenuFocusRef.current = restoreFocus;
    setThemeMenu(null);
  }

  function handleThemeMenuKeyDown(event: React.KeyboardEvent<HTMLDivElement>) {
    const menuItems = getFocusableElements(event.currentTarget).filter((element) => element.getAttribute("role") === "menuitem");
    const currentIndex = menuItems.indexOf(document.activeElement as HTMLElement);
    if (event.key === "Escape") {
      event.preventDefault();
      closeThemeMenu(true);
      return;
    }
    if (!menuItems.length || !["ArrowDown", "ArrowUp", "Home", "End"].includes(event.key)) return;
    event.preventDefault();
    const nextIndex = event.key === "Home" ? 0 : event.key === "End" ? menuItems.length - 1 : (currentIndex + (event.key === "ArrowDown" ? 1 : -1) + menuItems.length) % menuItems.length;
    menuItems[nextIndex]?.focus();
  }

  async function saveQuickTheme(patch: Partial<ThemePreference>) {
    if (!session || themeMenuBusy) return;
    setThemeMenuBusy(true); setThemeMenuError("");
    try {
      const preferences = await apiFetch<PreferencesData>("/api/v1/me/preferences");
      const theme = normalizeTheme({ ...preferences.theme, ...patch } as Record<string, unknown>);
      await apiFetch<PreferencesData>("/api/v1/me/preferences", { method: "PATCH", body: JSON.stringify({ theme, moduleFlags: preferences.moduleFlags, locale: preferences.locale }) });
      (window as Window & { jmlabTheme?: ThemePreference }).jmlabTheme = theme;
      applyThemeToDocument(theme);
      window.dispatchEvent(new Event("jmlab-preferences-change"));
      closeThemeMenu(true);
    } catch (requestError) {
      setThemeMenuError(requestError instanceof Error ? requestError.message : "主题保存失败");
    } finally { setThemeMenuBusy(false); }
  }

  return (
    <div className="app-shell" onContextMenu={(event) => {
      if (!session || isTextEntryTarget(event.target) || (event.target instanceof HTMLElement && event.target.closest("button, a"))) return;
      event.preventDefault();
      openThemeMenu(event.clientX, event.clientY);
    }}>
      <a className="skip-link" href="#main-content">跳到主要内容</a>
      <header className="topbar">
        <Link to="/" className="brand" aria-label="返回首页">
          <span className="brand-mark">J</span>
          <span className="brand-name">JMlab</span>
        </Link>
        <nav className="main-nav" aria-label="主导航">
          {navigation.map((item) => (
            <NavLink key={item.to} to={item.to} end={item.to === "/"} className={({ isActive }) => (isActive ? "active" : "")}>
              <Icon name={item.icon} /><span>{item.label}</span>
            </NavLink>
          ))}
          {userHasRole(session?.user, "Admin") && <NavLink to="/admin/modules" className={({ isActive }) => (isActive ? "active" : "")}><Icon name="module" /><span>模块管理</span></NavLink>}
          {session && <NavLink to="/settings" className={({ isActive }) => (isActive ? "active" : "")}><Icon name="settings" /><span>设置</span></NavLink>}
        </nav>
        <div className="topbar-actions">
          {session && <button ref={themeMenuTriggerRef} id="theme-menu-trigger" type="button" className="ghost-button theme-menu-trigger" aria-haspopup="menu" aria-controls="theme-context-menu" aria-expanded={Boolean(themeMenu)} onClick={(event) => { event.stopPropagation(); const rect = event.currentTarget.getBoundingClientRect(); openThemeMenu(rect.left, rect.bottom + 6); }}><Icon name="theme" /><span>主题</span></button>}
          {userHasRole(session?.user, "Admin") && <NavLink to="/admin/modules" className="ghost-button module-mobile-entry" aria-label="打开模块管理"><Icon name="module" /><span>模块</span></NavLink>}
          {session && <NavLink to="/settings" className="ghost-button settings-mobile-entry" aria-label="打开设置"><Icon name="settings" /><span>设置</span></NavLink>}
          {session ? <button className="ghost-button" onClick={async () => { await apiFetch("/api/v1/auth/logout", { method: "POST" }); saveSession(null); }}><Icon name="logout" /><span>退出</span></button> : <Link to="/login" className="primary-button">登录</Link>}
        </div>
      </header>
      <main id="main-content" className="page-shell" tabIndex={-1}>
        <Routes>
          <Route path="/" element={<HomePage />} />
          <Route path="/search" element={<SearchPage />} />
          <Route path="/rankings" element={<RankingPage />} />
          <Route path="/libraries" element={<LibraryPage />} />
          <Route path="/playlists" element={<PlaylistsPage />} />
          <Route path="/items/:itemId" element={<ItemPage />} />
          <Route path="/items/:itemId/play" element={<PlayPage />} />
          <Route path="/people" element={<RelationListPage kind="people" />} />
          <Route path="/people/:entityId" element={<RelationDetailPage kind="people" />} />
          <Route path="/studios" element={<RelationListPage kind="studios" />} />
          <Route path="/studios/:entityId" element={<RelationDetailPage kind="studios" />} />
          <Route path="/series" element={<RelationListPage kind="series" />} />
          <Route path="/series/:entityId" element={<RelationDetailPage kind="series" />} />
          <Route path="/tags" element={<RelationListPage kind="tags" />} />
          <Route path="/tags/:entityId" element={<RelationDetailPage kind="tags" />} />
          <Route path="/settings/*" element={<SettingsPage />} />
          <Route path="/login" element={<LoginPage />} />
          <Route path="/admin/modules/*" element={<ModuleManagementPage />} />
          <Route path="/admin/*" element={<LegacyAdminRedirect />} />
          <Route path="*" element={<NotFoundPage />} />
        </Routes>
      </main>
      {themeMenu && <div ref={themeMenuRef} id="theme-context-menu" className="theme-context-menu" role="menu" aria-labelledby="theme-menu-trigger" style={{ left: themeMenu.x, top: themeMenu.y }} onKeyDown={handleThemeMenuKeyDown} onContextMenu={(event) => { event.preventDefault(); event.stopPropagation(); }}>
        <strong>快速主题</strong>
        <div className="theme-menu-row"><button type="button" role="menuitem" disabled={themeMenuBusy} onClick={() => void saveQuickTheme({ mode: "dark" })}>深色</button><button type="button" role="menuitem" disabled={themeMenuBusy} onClick={() => void saveQuickTheme({ mode: "light" })}>浅色</button></div>
        <div className="theme-menu-row"><button type="button" role="menuitem" disabled={themeMenuBusy} onClick={() => void saveQuickTheme({ accent: "blue" })}>蓝色</button><button type="button" role="menuitem" disabled={themeMenuBusy} onClick={() => void saveQuickTheme({ accent: "mint" })}>薄荷</button><button type="button" role="menuitem" disabled={themeMenuBusy} onClick={() => void saveQuickTheme({ accent: "amber" })}>琥珀</button></div>
        <small>右键空白处或点击“主题”打开</small>
        {themeMenuError && <span className="theme-menu-error" role="alert">{themeMenuError}</span>}
      </div>}
    </div>
  );
}

function HomePage() {
  const session = readSession();
  const [dashboard, setDashboard] = useState<HomeDashboard | null>(null);
  const [preferences, setPreferences] = useState<PreferencesData | null>(null);
  const [loading, setLoading] = useState(Boolean(session));
  const [error, setError] = useState("");
  useEffect(() => {
    if (!session) { setLoading(false); return; }
    let cancelled = false;
    setLoading(true); setError("");
    Promise.all([apiFetch<HomeDashboard>("/api/v1/home?limit=12"), apiFetch<PreferencesData>("/api/v1/me/preferences")])
      .then(([home, prefs]) => { if (!cancelled) { setDashboard(home); setPreferences(prefs); } })
      .catch((requestError) => { if (!cancelled) setError(requestError instanceof Error ? requestError.message : "读取首页失败"); })
      .finally(() => { if (!cancelled) setLoading(false); });
    return () => { cancelled = true; };
  }, [session?.accessToken]);
  if (!session) return <section className="hero-grid"><div className="hero-card"><p className="eyebrow">虚拟媒体服务器</p><h1>把电影、剧集和个人视频整理成自己的媒体库。</h1><p className="muted">登录后浏览媒体库、继续播放，也可以用自己的清单整理想看的内容。</p><div className="hero-actions"><Link to="/login" className="primary-button">登录开始</Link><Link to="/search" className="ghost-button">浏览搜索</Link></div></div><div className="stat-panel"><div><span className="stat-value">—</span><span className="stat-label">需要登录</span></div><div><span className="stat-value">—</span><span className="stat-label">用户隔离</span></div><div><span className="stat-value">—</span><span className="stat-label">Jellyfin</span></div></div></section>;
  if (loading) return <section className="content-card home-dashboard"><p className="eyebrow">JMlab 首页</p><h1>正在加载你的媒体库…</h1><div className="skeleton-grid">{[1, 2, 3, 4].map((value) => <div className="skeleton-card" key={value} />)}</div></section>;
  if (error) return <section className="content-card"><p className="eyebrow">首页</p><h1>暂时无法读取首页</h1><p className="form-error" role="alert">{error}</p><Link to="/libraries" className="ghost-button">打开媒体库</Link></section>;
  if (!dashboard) return null;
  const enabled = (key: string) => preferences?.moduleFlags?.[key] !== false;
  const hasVisibleModule = homeModuleOrder.some((key) => enabled(key) && Boolean(dashboard.modules[key]?.items?.length));

  return <section className="home-dashboard"><div className="home-hero"><div><p className="eyebrow">我的媒体</p><h1>欢迎回来，{session.user.displayName || session.user.username}</h1><p className="muted">从上次观看的地方继续，或发现下一部想看的作品。</p><div className="hero-actions"><Link to="/search" className="primary-button">搜索作品</Link><Link to="/rankings" className="ghost-button">查看排行</Link><Link to="/libraries" className="ghost-button">浏览媒体库</Link><Link to="/settings/profile" className="ghost-button">调整首页</Link></div></div><div className="home-stats"><div><strong>{dashboard.stats.visibleItems}</strong><span>可见作品</span></div><div><strong>{dashboard.stats.playableItems}</strong><span>可播放作品</span></div><div><strong>{dashboard.stats.people}</strong><span>演员</span></div></div></div>{error && <p className="form-error" role="alert">{error}</p>}{homeModuleOrder.filter(enabled).map((key) => { const module = dashboard.modules[key]; if (!module || module.items.length === 0) return null; return <section className="home-module" key={key}><div className="home-module-heading"><div><p className="eyebrow">{module.title || homeModuleLabels[key]}</p><h2>{homeModuleLabels[key]}</h2></div><Link to="/search" className="ghost-button compact-button">查看全部</Link></div><ItemGrid items={module.items} compact /></section>; })}{enabled("ranking") && <ModuleRankingLinks />}{!hasVisibleModule && <div className="empty-card"><div className="empty-icon">＋</div><h3>你的媒体库还没有可见作品</h3><p className="muted">管理员可以连接来源并添加内容，其他账号需要获得媒体库授权。</p>{userHasRole(session.user, "Admin", "Editor") ? <Link to="/settings/discover" className="primary-button">添加第一部内容</Link> : <Link to="/libraries" className="ghost-button">查看媒体库</Link>}</div>}</section>;
}

function RankingPage() {
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
  return <section className="content-card ranking-page"><div className="ranking-page-heading"><div><p className="eyebrow">发现内容</p><h1>模块排行榜</h1><p className="muted">选择内容来源、榜单和范围。只显示当前账号有权访问的已入库内容。</p></div><Link to="/search" className="ghost-button">搜索作品</Link></div>{error && <p className="form-error" role="alert">{error}</p>}{sources.length > 0 && <div className="form-grid"><label>内容来源<select value={source?.id || ""} onChange={(e) => setParams({ moduleId: e.target.value })}>{sources.map((v) => <option key={v.id} value={v.id}>{v.name}</option>)}</select></label><label>榜单<select value={ranking?.id || ""} onChange={(e) => setParams({ moduleId: source!.id, type: e.target.value })}>{source?.rankings.map((v) => <option key={v.id} value={v.id}>{v.name}</option>)}</select></label><label>范围<select value={period?.value || ""} onChange={(e) => setParams({ moduleId: source!.id, type: ranking!.id, period: e.target.value })}>{ranking?.periods.map((v) => <option key={v.value} value={v.value}>{v.label}</option>)}</select></label></div>}{loading ? <p role="status">正在读取榜单…</p> : items.length > 0 ? <ol className="ranking-list">{items.map((entry) => <li key={entry.item.id}><Link to={`/items/${entry.item.id}`} className="ranking-list-item"><span className="ranking-list-rank">{entry.rank}</span><div className="ranking-poster-mini"><ItemPoster item={entry.item} /></div><div className="ranking-list-info"><h3>{entry.item.title}</h3><p className="muted">{mediaTypeLabel(entry.item.itemType)} · {entry.item.year || "年份未知"}</p></div></Link></li>)}</ol> : <div className="empty-card"><h2>{sources.length ? "榜单暂时为空" : "还没有可用榜单"}</h2><p className="muted">管理员可在相关模块中同步榜单，并为账号授权媒体库。</p>{userHasRole(readSession()?.user, "Admin") && <Link to={source ? `/admin/modules/${encodeURIComponent(source.id)}?tab=rankings` : "/admin/modules"} className="ghost-button">管理榜单来源</Link>}</div>}</section>;
}

function DistributionBars({ title, buckets }: { title: string; buckets: DistributionBucket[] }) {
  const maximum = Math.max(1, ...buckets.map((bucket) => bucket.count));
  return <section className="distribution-card"><h2>{title}</h2>{buckets.length === 0 ? <p className="muted">暂无统计数据</p> : <div className="distribution-list">{buckets.map((bucket) => <div className="distribution-row" key={bucket.key}><span>{bucket.label}</span><div className="distribution-track"><span style={{ width: `${Math.round((bucket.count / maximum) * 100)}%` }} /></div><strong>{bucket.count}</strong></div>)}</div>}</section>;
}

function LibraryPage() {
  useScrollRestoration("libraries");
  return <LibraryBrowser renderItems={(items) => <ItemGrid items={items} />} />;
}


function ItemPage() {
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
    {container ? <MediaChildren key={item.id} item={item} /> : <section className="content-card"><div className="relation-page-heading"><h2>播放来源</h2>{userHasRole(readSession()?.user, "Admin", "Editor") && <Link to={`/settings/sources?${new URLSearchParams({ itemId: item.id })}`} className="ghost-button">管理播放源</Link>}</div>{sourcesLoading ? <p role="status">正在查找播放来源…</p> : sourceError ? <p className="form-error" role="alert">{sourceError}</p> : playable.length === 0 ? <p className="muted">暂未找到可用视频。作品信息已保留，可以稍后再试，或请管理员检查视频来源。</p> : <div className="builtin-sources-grid">{playable.map((source) => <Link to={`/items/${encodeURIComponent(item.id)}/play?sourceId=${encodeURIComponent(source.id)}`} className="source-card" key={source.id}><span><strong>{source.label}</strong><small>{source.isPreview ? "预告片" : "视频"}{source.quality ? ` · ${source.quality}` : ""}</small></span><span>播放 ▶</span></Link>)}</div>}</section>}
    <MediaStatePanel key={`state-${item.id}`} itemId={item.id} />
    <AddToPlaylist key={item.id} itemId={item.id} />
    {(item.people?.length || item.studios?.length || item.series?.length || item.tags?.length || sourceURL || visibleCode(item)) ? <section className="content-card item-relations-section"><RelationBlock kind="people" label="演职人员" values={item.people} /><RelationBlock kind="studios" label="制作公司" values={item.studios} /><RelationBlock kind="series" label="作品系列" values={item.series} /><RelationBlock kind="tags" label="分类" values={item.tags} />{sourceURL && <a href={sourceURL} target="_blank" rel="noreferrer" className="ghost-button">查看来源信息 ↗</a>}</section> : null}
    <ImageGallery itemId={item.id} initialImages={images} initialError={imageError} onImagesChange={setImages} />
  </div>;
}

const imageTypeLabels: Record<ItemImage["imageType"], string> = { gallery: "剧照", preview: "预览", poster: "海报", backdrop: "背景", avatar: "头像" };

function imageVariantURL(image: ItemImage, width: number, height: number, fit: "cover" | "contain", retry = 0) {
  const params = new URLSearchParams({ width: String(width), height: String(height), fit });
  if (retry > 0) params.set("retry", String(retry));
  return `${image.url}?${params.toString()}`;
}

function ImageGallery({ itemId, initialImages, initialError, onImagesChange }: { itemId: string; initialImages: ItemImage[]; initialError: string; onImagesChange: (images: ItemImage[]) => void }) {
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

function PlayPage() {
  const TICKS_PER_SECOND = 10_000_000;
  const { itemId } = useParams();
  const navigate = useNavigate();
  const session = readSession();
  const videoRef = useRef<HTMLVideoElement | null>(null);
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
    const video = videoRef.current;
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
                    ref={videoRef}
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

function RelationListPage({ kind }: { kind: RelationKind }) {
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

function RelationDirectoryLinks({ active }: { active: RelationKind }) {
  return <nav className="relation-directory" aria-label="关系目录导航">{(Object.keys(relationLabels) as RelationKind[]).map((kind) => <Link className={active === kind ? "active" : ""} to={`/${kind}`} key={kind}>{relationLabels[kind]}</Link>)}</nav>;
}

function RelationDetailPage({ kind }: { kind: RelationKind }) {
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

function ProfileSettingsPage() {
  const navigate = useNavigate();
  const session = readSession();
  const [theme, setTheme] = useState<ThemePreference>(defaultThemePreference);
  const [moduleFlags, setModuleFlags] = useState<Record<string, unknown>>(defaultModuleFlags);
  const locale = "zh-CN";
  const [adultConfirmed, setAdultConfirmed] = useState(false);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState("");
  const [error, setError] = useState("");

  useEffect(() => {
    if (!session) {
      navigate("/login", { replace: true });
      return;
    }
    let cancelled = false;
    apiFetch<PreferencesData>("/api/v1/me/preferences")
      .then((preferences) => {
        if (cancelled) return;
        setTheme(normalizeTheme(preferences.theme));
        setModuleFlags({ ...defaultModuleFlags, ...preferences.moduleFlags });
        setAdultConfirmed(Boolean(preferences.adultConfirmedAt));
      })
      .catch((requestError) => { if (!cancelled) setError(requestError instanceof Error ? requestError.message : "读取偏好失败"); })
      .finally(() => { if (!cancelled) setLoading(false); });
    return () => { cancelled = true; };
  }, [session?.accessToken, navigate]);

  function applyTheme(themeValue: ThemePreference) {
    setTheme(themeValue);
    (window as Window & { jmlabTheme?: ThemePreference }).jmlabTheme = themeValue;
    window.dispatchEvent(new Event("jmlab-preferences-change"));
  }

  async function savePreferences(event: FormEvent) {
    event.preventDefault();
    setBusy(true); setMessage(""); setError("");
    try {
      await apiFetch<PreferencesData>("/api/v1/me/preferences", { method: "PATCH", body: JSON.stringify({ theme, moduleFlags, locale, adultConfirmed }) });
      setMessage("偏好已保存");
    } catch (requestError) {
      setError(requestError instanceof Error ? requestError.message : "保存偏好失败");
    } finally { setBusy(false); }
  }

  function toggleModule(key: string) {
    setModuleFlags((current) => ({ ...current, [key]: current[key] === false }));
  }

  if (!session) return null;
  return <form className="content-card settings-page" onSubmit={savePreferences}><div className="settings-heading"><div><p className="eyebrow">个人偏好</p><h1>主题与首页</h1><p className="muted">偏好只影响当前账号；媒体库权限仍由管理员控制。</p></div><Link to="/settings" className="ghost-button">返回设置</Link></div>{loading ? <div className="settings-loading"><div className="skeleton-line" /><div className="skeleton-line short" /><div className="skeleton-grid"><div className="skeleton-card" /><div className="skeleton-card" /></div></div> : <><section className="settings-section"><div><p className="eyebrow">外观</p><h2>选择显示风格</h2></div><div className="settings-options"><label className={`settings-option ${theme.mode === "dark" ? "selected" : ""}`}><input type="radio" name="theme-mode" checked={theme.mode === "dark"} onChange={() => applyTheme({ ...theme, mode: "dark" })} /><span><strong>深色</strong><small>适合夜间浏览</small></span></label><label className={`settings-option ${theme.mode === "light" ? "selected" : ""}`}><input type="radio" name="theme-mode" checked={theme.mode === "light"} onChange={() => applyTheme({ ...theme, mode: "light" })} /><span><strong>浅色</strong><small>更明亮的阅读界面</small></span></label></div><div className="accent-options"><span className="settings-label">强调色</span>{(["blue", "mint", "amber"] as const).map((accent) => <button type="button" className={`accent-option accent-${accent} ${theme.accent === accent ? "selected" : ""}`} aria-label={`选择${accent === "blue" ? "蓝色" : accent === "mint" ? "薄荷色" : "琥珀色"}强调色`} key={accent} onClick={() => applyTheme({ ...theme, accent })}><span /></button>)}</div></section><section className="settings-section"><div><p className="eyebrow">布局</p><h2>调整卡片密度</h2><p className="muted">这些设置会即时作用于首页和媒体库网格。</p></div><div className="layout-settings"><label><span className="settings-label">每行卡片数</span><select value={theme.columns} onChange={(event) => applyTheme({ ...theme, columns: event.target.value as ThemePreference["columns"] })}><option value="auto">自动</option><option value="2">2 列</option><option value="3">3 列</option><option value="4">4 列</option><option value="5">5 列</option></select></label><label><span className="settings-label">海报比例</span><select value={theme.posterRatio} onChange={(event) => applyTheme({ ...theme, posterRatio: event.target.value as ThemePreference["posterRatio"] })}><option value="portrait">竖向 2:3</option><option value="square">方形 1:1</option><option value="wide">横向 16:9</option></select></label></div><details className="appearance-advanced"><summary>更多视觉效果</summary><p className="muted">关闭动效可减少干扰，设备开启“减少动态效果”时也会自动简化动画。</p><div className="module-settings layout-toggles"><label className="module-setting"><span><strong>背景模糊</strong><small>为顶部栏和浮层保留玻璃质感</small></span><input type="checkbox" checked={theme.backgroundBlur} onChange={(event) => applyTheme({ ...theme, backgroundBlur: event.target.checked })} /></label><label className="module-setting"><span><strong>液态背景</strong><small>显示缓慢移动的渐变光斑</small></span><input type="checkbox" checked={theme.liquid} onChange={(event) => applyTheme({ ...theme, liquid: event.target.checked })} /></label><label className="module-setting"><span><strong>3D 卡片</strong><small>悬停时显示轻微透视效果</small></span><input type="checkbox" checked={theme.card3d} onChange={(event) => applyTheme({ ...theme, card3d: event.target.checked })} /></label></div></details><div className="module-settings"><label className="module-setting"><span><strong>原标题</strong><small>在作品卡片中显示来源原标题</small></span><input type="checkbox" checked={theme.showOriginalTitle} onChange={(event) => applyTheme({ ...theme, showOriginalTitle: event.target.checked })} /></label></div></section><section className="settings-section"><div><p className="eyebrow">首页内容</p><h2>显示哪些内容</h2><p className="muted">只调整自己的首页推荐，不影响服务模块的启停。</p></div><div className="module-settings">{preferenceModuleKeys.map((key) => <label className="module-setting" key={key}><span><strong>{preferenceModuleLabels[key]}</strong><small>{key === "ranking" ? "排行榜" : "首页推荐"}</small></span><input type="checkbox" checked={moduleFlags[key] !== false} onChange={() => toggleModule(key)} /></label>)}</div></section><section className="settings-section"><h2>成人内容</h2><label className="module-setting"><span><strong>我已成年，允许显示成人内容</strong><small>仍需管理员授予相应媒体库权限；取消后不再显示成人媒体。</small></span><input type="checkbox" checked={adultConfirmed} onChange={(event) => setAdultConfirmed(event.target.checked)} /></label></section><section className="settings-section settings-locale"><p className="muted">界面语言：简体中文</p></section>{error && <p className="form-error" role="alert">{error}</p>}<div className="settings-actions"><button className="primary-button" disabled={busy}>{busy ? "保存中…" : "保存偏好"}</button>{message && <span className="form-success" role="status">{message}</span>}</div></>}</form>;
}

function LoginPage() {
  const navigate = useNavigate();
  const [username, setUsername] = useState(""); const [password, setPassword] = useState(""); const [error, setError] = useState(""); const [busy, setBusy] = useState(false);
  async function submit(event: FormEvent) { event.preventDefault(); setBusy(true); setError(""); try { const session = await apiFetch<SessionData>("/api/v1/auth/login", { method: "POST", body: JSON.stringify({ username, password, clientName: "JMlab Web", deviceId: "web" }) }); saveSession(session); navigate("/"); } catch (requestError) { setError(requestError instanceof Error ? requestError.message : "登录失败"); } finally { setBusy(false); } }
  return <form className="auth-card" onSubmit={submit}><p className="eyebrow">JMlab 账号</p><h1>登录媒体库</h1><label>用户名<input required autoComplete="username" value={username} onChange={(event) => setUsername(event.target.value)} /></label><label>密码<input required type="password" autoComplete="current-password" value={password} onChange={(event) => setPassword(event.target.value)} /></label>{error && <p className="form-error" role="alert">{error}</p>}<button className="primary-button" disabled={busy}>{busy ? "登录中…" : "登录"}</button><p className="muted">登录后，Web 与 Jellyfin 客户端会共享同一份用户状态。</p></form>;
}

function AdminDashboardPage() {
  const [dashboard, setDashboard] = useState<AdminDashboardData | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  async function loadDashboard() {
    try { setDashboard(await apiFetch<AdminDashboardData>("/api/v1/dashboard")); setError(""); }
    catch (requestError) { setError(requestError instanceof Error ? requestError.message : "读取运行概况 失败"); }
    finally { setLoading(false); }
  }
  useEffect(() => { void loadDashboard(); const timer = window.setInterval(() => { void loadDashboard(); }, 15000); return () => window.clearInterval(timer); }, []);
  if (loading && !dashboard) return <section className="content-card management-page"><p className="eyebrow">服务运行</p><h1>正在读取运营概览…</h1><div className="skeleton-grid"><div className="skeleton-card" /><div className="skeleton-card" /><div className="skeleton-card" /></div></section>;
  if (!dashboard) return <section className="content-card management-page"><p className="form-error" role="alert">{error || "暂时无法读取运行概况"}</p><Link to="/settings" className="ghost-button">返回设置</Link></section>;
  const resourceLabels: Array<[keyof AdminDashboardData["resources"], string]> = [["items", "作品"], ["playableItems", "可播放作品"], ["people", "演员"], ["studios", "片商"], ["series", "系列"], ["tags", "标签"], ["collections", "合集"], ["libraries", "媒体库"], ["providers", "启用 Provider"], ["playbackSources", "可用播放源"]];
  return <section className="management-page admin-dashboard-page"><div className="relation-page-heading"><div><p className="eyebrow">内容与系统管理</p><h1>运行概况</h1><p className="muted">资源、任务、在线状态和播放站点健康会自动刷新。</p></div><Link to="/settings" className="ghost-button">返回设置</Link></div>{error && <p className="form-error" role="alert">{error}</p>}<div className="dashboard-stat-grid">{resourceLabels.map(([key, label]) => <div className="dashboard-stat" key={key}><span>{label}</span><strong>{dashboard.resources[key]}</strong></div>)}</div><div className="admin-dashboard-columns"><section className="dashboard-panel"><div className="home-module-heading"><div><p className="eyebrow">任务状态</p><h2>队列概览</h2></div><Link to="/settings/tasks" className="ghost-button compact-button">打开任务中心</Link></div><div className="dashboard-task-list">{dashboard.tasks.map((task) => <div className="dashboard-task-row" key={task.status}><span>{taskStatusLabels[task.status] ?? task.status}</span><div className="task-progress"><span style={{ width: `${dashboard.tasks.reduce((total, item) => total + item.count, 0) ? Math.round((task.count / dashboard.tasks.reduce((total, item) => total + item.count, 0)) * 100) : 0}%` }} /></div><strong>{task.count}</strong></div>)}</div></section><section className="dashboard-panel"><p className="eyebrow">健康与在线</p><h2>服务状态</h2><div className="dashboard-health-grid"><span>数据库<strong className={dashboard.health.database === "healthy" ? "status-active" : "status-disabled"}>{dashboard.health.database === "healthy" ? "健康" : "异常"}</strong></span><span>服务<strong className="status-active">{dashboard.health.service === "healthy" ? "健康" : dashboard.health.service}</strong></span><span>在线会话<strong>{dashboard.online.sessions}</strong></span><span>在线用户<strong>{dashboard.online.users}</strong></span><span>播放中<strong>{dashboard.online.activePlayback}</strong></span><span>Provider<strong>{dashboard.providerHealth.healthy}/{dashboard.providerHealth.unhealthy}/{dashboard.providerHealth.unknown}</strong><small>健康 / 异常 / 未探测</small></span></div></section></div><div className="admin-dashboard-columns"><section className="dashboard-panel"><div className="home-module-heading"><div><p className="eyebrow">最近任务</p><h2>执行记录</h2></div><span className="muted">{dashboard.recentTasks.length} 条</span></div>{dashboard.recentTasks.length === 0 ? <p className="muted">暂无任务</p> : <div className="dashboard-list">{dashboard.recentTasks.slice(0, 6).map((task) => <Link to="/settings/tasks" className="dashboard-list-row" key={task.id}><span><strong>{task.type}</strong><small>{task.id}</small></span><span className={`task-status task-status-${task.status}`}>{taskStatusLabels[task.status] ?? task.status}</span><small>{task.progress}%</small></Link>)}</div>}</section><section className="dashboard-panel"><div className="home-module-heading"><div><p className="eyebrow">错误摘要</p><h2>需要关注</h2></div><span className="muted">共 {dashboard.errors.total} 条</span></div><div className="dashboard-error-counts"><span>任务失败<strong>{dashboard.errors.taskFailures}</strong></span><span>审计失败<strong>{dashboard.errors.auditFailures}</strong></span></div>{dashboard.recentErrors.length === 0 ? <p className="muted">暂无错误</p> : <div className="dashboard-list">{dashboard.recentErrors.slice(0, 5).map((item) => <div className="dashboard-list-row" key={`${item.source}-${item.id}`}><span><strong>{item.code || item.source}</strong><small>{item.resourceType ? `${item.resourceType} ${item.resourceId || ""}` : item.id}</small></span><small>{item.message || "—"}</small></div>)}</div>}</section></div></section>;
}

type LibraryAdminForm = { name: string; description: string; itemType: VirtualLibraryRecord["itemType"]; filterJSON: string; sortField: string; sortDirection: string; posterImageId: string; isPublic: boolean; isAdult: boolean; enabled: boolean };
const emptyLibraryAdminForm: LibraryAdminForm = { name: "", description: "", itemType: "movie", filterJSON: "{}", sortField: "updatedAt", sortDirection: "desc", posterImageId: "", isPublic: true, isAdult: true, enabled: true };
function libraryAdminForm(library: VirtualLibraryRecord): LibraryAdminForm { return { name: library.name, description: library.description ?? "", itemType: library.itemType, filterJSON: JSON.stringify(library.filter ?? {}, null, 2), sortField: library.sort?.field ?? "updatedAt", sortDirection: library.sort?.direction ?? "desc", posterImageId: library.posterImageId ?? "", isPublic: library.isPublic, isAdult: library.isAdult, enabled: library.enabled }; }
type LibraryPermissionRecord = { subjectType: "user" | "role" | string; subjectId: string; name?: string; canView: boolean; canPlay: boolean };
const libraryRoleOptions: Array<{ id: string; name: string }> = [{ id: "role-admin", name: "管理员（Admin）" }, { id: "role-editor", name: "编辑者（Editor）" }, { id: "role-viewer", name: "查看者（Viewer）" }];

function LibraryAdminPage() {
  const session = readSession();
  const canManage = userHasRole(session?.user, "Admin");
  const [libraries, setLibraries] = useState<VirtualLibraryRecord[]>([]);
  const [editing, setEditing] = useState<VirtualLibraryRecord | null>(null);
  const [form, setForm] = useState<LibraryAdminForm>(emptyLibraryAdminForm);
  const [editorOpen, setEditorOpen] = useState(false);
  const [previewCounts, setPreviewCounts] = useState<Record<string, number>>({});
  const [permissionLibrary, setPermissionLibrary] = useState<VirtualLibraryRecord | null>(null);
  const [permissionUsers, setPermissionUsers] = useState<User[]>([]);
  const [permissionDraft, setPermissionDraft] = useState<Record<string, LibraryPermissionRecord>>({});
  const [loading, setLoading] = useState(true); const [busy, setBusy] = useState(false); const [error, setError] = useState(""); const [message, setMessage] = useState("");

  const [showAdvancedJson, setShowAdvancedJson] = useState(false);
  const [templateSelection, setTemplateSelection] = useState<LibraryTemplateSelection | null>(null);
  const [editorKey, setEditorKey] = useState(0);
  const [yearsDraft, setYearsDraft] = useState("");
  const suggestedName = useRef("");
  const suggestedDescription = useRef("");
  let visualFilter: Record<string, unknown> = {};
  let visualAvailable = true;
  try {
    const parsed: unknown = JSON.parse(form.filterJSON);
    if (!parsed || typeof parsed !== "object" || Array.isArray(parsed) || ["all", "any", "not", "field"].some((key) => key in parsed)) visualAvailable = false;
    else visualFilter = parsed as Record<string, unknown>;
  } catch { visualAvailable = false; }
  const minRating = visualFilter.minRating == null ? "" : String(visualFilter.minRating);
  const years = Array.isArray(visualFilter.years) ? visualFilter.years.join(",") : "";
  const hasPlayableSource = visualFilter.hasPlayableSource === true;
  const isFavorite = visualFilter.isFavorite === true;
  useEffect(() => { setYearsDraft(years); }, [years, editorKey]);

  async function loadLibraries() { setLoading(true); try { const result = await apiFetch<{ items: VirtualLibraryRecord[] }>(`/api/v1/libraries${canManage ? "?includeDisabled=1" : ""}`); setLibraries(result.items); setError(""); } catch (requestError) { setError(requestError instanceof Error ? requestError.message : "读取虚拟媒体库失败"); } finally { setLoading(false); } }
  useEffect(() => { void loadLibraries(); }, [canManage]);
  useEffect(() => { if (!canManage) return; apiFetch<{ items: User[] }>("/api/v1/admin/users").then((result) => setPermissionUsers(result.items)).catch(() => setPermissionUsers([])); }, [canManage]);
  function updateForm<K extends keyof LibraryAdminForm>(key: K, value: LibraryAdminForm[K]) { setForm((current) => ({ ...current, [key]: value })); }

  function syncVisualToFilter(overrides: Record<string, unknown>) {
    if (!visualAvailable) return;
    const next = { ...visualFilter };
    for (const [key, value] of Object.entries(overrides)) {
      if (value == null || value === "" || value === false) delete next[key];
      else next[key] = value;
    }
    updateForm("filterJSON", JSON.stringify(next, null, 2));
  }
  function applyTemplate(template: ResolvedLibraryTemplate, selection: LibraryTemplateSelection) {
    setTemplateSelection(selection);
    const previousName = suggestedName.current;
    const previousDescription = suggestedDescription.current;
    setForm((current) => ({ ...current,
      name: !current.name || current.name === previousName ? template.name : current.name,
      description: !current.description || current.description === previousDescription ? template.description : current.description,
      itemType: template.itemType, isAdult: template.isAdult, filterJSON: JSON.stringify(template.filter, null, 2),
      sortField: template.sort.field, sortDirection: template.sort.direction,
    }));
    // Keep names entered by the user when a template or range changes.
    suggestedName.current = template.name; suggestedDescription.current = template.description;
    setShowAdvancedJson(false);
  }
  function resetTemplate() {
    setTemplateSelection(null);
    if (!editing) { setForm(emptyLibraryAdminForm); suggestedName.current = ""; suggestedDescription.current = ""; }
  }
  function startCreate() {
    setEditing(null); setForm(emptyLibraryAdminForm); setTemplateSelection(null);
    suggestedName.current = ""; suggestedDescription.current = "";
    setEditorKey((key) => key + 1); setShowAdvancedJson(false); setEditorOpen(true); setMessage(""); setError("");
  }
  function startEdit(library: VirtualLibraryRecord) {
    setEditing(library); setForm(libraryAdminForm(library)); setTemplateSelection(null);
    suggestedName.current = ""; suggestedDescription.current = "";
    setEditorKey((key) => key + 1); setShowAdvancedJson(true); setEditorOpen(true); setMessage(""); setError("");
    window.scrollTo({ top: 0, behavior: "smooth" });
  }

  async function saveLibrary(event: FormEvent) {
    event.preventDefault();
    if (!editing && !templateSelection) { setError("请先选择模块和预设模板"); return; }
    setBusy(true);
    setError("");
    setMessage("");
    let filter: unknown;
    try {
      filter = JSON.parse(form.filterJSON || "{}");
    } catch {
      setError("过滤器必须是合法 JSON");
      setBusy(false);
      return;
    }
    if (visualAvailable) {
      const values = yearsDraft.trim() ? yearsDraft.trim().split(/[\s,，]+/).map(Number) : [];
      if (values.some((year) => !Number.isInteger(year) || year < 1900 || year > 2100)) {
        setError("请填写 1900 到 2100 之间的年份，用逗号分隔"); setBusy(false); return;
      }
      const next = { ...visualFilter };
      if (values.length) next.years = values; else delete next.years;
      filter = next;
    }
    const payload = {
      ...(!editing && templateSelection ? { template: templateSelection } : {}),
      name: form.name.trim(),
      description: form.description.trim(),
      itemType: form.itemType,
      filter,
      sort: { field: form.sortField, direction: form.sortDirection },
      posterImageId: form.posterImageId.trim(),
      isPublic: form.isPublic,
      isAdult: form.isAdult,
      enabled: form.enabled,
    };
    try {
      await apiFetch<VirtualLibraryRecord>(editing ? `/api/v1/libraries/${editing.id}` : "/api/v1/libraries", { method: editing ? "PATCH" : "POST", body: JSON.stringify(payload) });
      setMessage(editing ? "媒体库已更新" : "媒体库已创建");
      setEditorOpen(false);
      setEditing(null);
      setForm(emptyLibraryAdminForm);
      await loadLibraries();
    } catch (requestError) {
      setError(requestError instanceof Error ? requestError.message : "保存媒体库失败");
    } finally {
      setBusy(false);
    }
  }

  async function removeLibrary(library: VirtualLibraryRecord) { if (!window.confirm(`确定删除「${library.name}」吗？`)) return; setBusy(true); setError(""); try { await apiFetch(`/api/v1/libraries/${library.id}`, { method: "DELETE" }); if (permissionLibrary?.id === library.id) setPermissionLibrary(null); await loadLibraries(); setMessage("媒体库已删除"); } catch (requestError) { setError(requestError instanceof Error ? requestError.message : "删除媒体库失败"); } finally { setBusy(false); } }
  async function previewLibrary(library: VirtualLibraryRecord) { try { const result = await apiFetch<{ itemCount: number }>(`/api/v1/libraries/${library.id}/preview`); setPreviewCounts((current) => ({ ...current, [library.id]: result.itemCount })); } catch (requestError) { setError(requestError instanceof Error ? requestError.message : "预览媒体库失败"); } }
  async function openPermissions(library: VirtualLibraryRecord) { setPermissionLibrary(library); setError(""); try { const result = await apiFetch<{ permissions: LibraryPermissionRecord[] }>(`/api/v1/libraries/${library.id}/permissions`); const next: Record<string, LibraryPermissionRecord> = {}; result.permissions.forEach((permission) => { next[`${permission.subjectType}:${permission.subjectId}`] = permission; }); setPermissionDraft(next); } catch (requestError) { setPermissionLibrary(null); setError(requestError instanceof Error ? requestError.message : "读取媒体库授权失败"); } }
  function updatePermission(subjectType: "user" | "role", subjectId: string, field: "canView" | "canPlay", value: boolean) { const key = `${subjectType}:${subjectId}`; setPermissionDraft((current) => ({ ...current, [key]: { ...(current[key] ?? {}), subjectType, subjectId, [field]: value } })); }
  async function savePermissions() { if (!permissionLibrary) return; setBusy(true); setError(""); try { const permissions = Object.values(permissionDraft).filter((permission) => permission.canView || permission.canPlay); await apiFetch(`/api/v1/libraries/${permissionLibrary.id}/permissions`, { method: "PUT", body: JSON.stringify({ permissions }) }); setMessage("媒体库授权已保存"); await openPermissions(permissionLibrary); } catch (requestError) { setError(requestError instanceof Error ? requestError.message : "保存媒体库授权失败"); } finally { setBusy(false); } }
  const permissionSubjects = [...libraryRoleOptions.map((role) => ({ subjectType: "role" as const, subjectId: role.id, name: role.name, detail: "角色" })), ...permissionUsers.map((user) => ({ subjectType: "user" as const, subjectId: user.id, name: user.displayName || user.username, detail: `用户 · ${user.username}` }))];

  return (
    <section className="content-card management-page">
      <div className="relation-page-heading">
        <div>
          <p className="eyebrow">虚拟媒体库</p>
          <h1>媒体库配置</h1>
          <p className="muted">先选择模块，再从模块提供的模板和榜单范围创建虚拟库。</p>
        </div>
        <Link to="/settings" className="ghost-button">返回设置</Link>
      </div>
      <div className="admin-toolbar">
        {canManage && <button type="button" className="primary-button" onClick={startCreate}>新建媒体库</button>}
        <Link to="/settings/collections" className="ghost-button">管理合集</Link>
        <Link to="/settings/dashboard" className="ghost-button">查看运行概况</Link>
      </div>
      {error && <p className="form-error" role="alert">{error}</p>}
      {message && <p className="form-success" role="status">{message}</p>}
      {canManage && editorOpen && (
        <form className="relation-editor" onSubmit={saveLibrary}>
          <div className="relation-editor-heading">
            <div>
              <p className="eyebrow">{editing ? "编辑媒体库" : "新建媒体库"}</p>
              <h2>{editing ? editing.name : "定义一个虚拟库"}</h2>
            </div>
            <button type="button" className="ghost-button" onClick={() => setEditorOpen(false)}>关闭</button>
          </div>
          {editing ? <details className="wide-field"><summary>使用模块模板替换筛选规则</summary><p className="muted">选择模板会替换当前范围和排序；直接保存会保留原规则。</p><LibraryTemplatePicker key={editorKey} onApply={applyTemplate} onReset={resetTemplate} /></details> : <LibraryTemplatePicker key={editorKey} onApply={applyTemplate} onReset={resetTemplate} />}
          <fieldset className="library-fields wide-field" disabled={!editing && !templateSelection}>
          <label>
            名称
            <input required maxLength={200} value={form.name} onChange={(event) => updateForm("name", event.target.value)} />
          </label>
          <label>
            类型
            <select value={form.itemType} onChange={(event) => updateForm("itemType", event.target.value as LibraryAdminForm["itemType"])}>
              <option value="movie">作品</option>
              <option value="collection">合集</option>
              <option value="people">演员</option>
              <option value="mixed">混合</option>
            </select>
          </label>
          <label className="wide-field">
            说明
            <textarea maxLength={2000} value={form.description} onChange={(event) => updateForm("description", event.target.value)} />
          </label>

          <div className="wide-field filter-builder-container">
            <div className="filter-presets-header">
              <strong>调整筛选条件</strong>
              <button type="button" className="ghost-button compact-button" onClick={() => setShowAdvancedJson(!showAdvancedJson)}>{showAdvancedJson ? "收起 JSON 模式" : "高级 JSON 模式"}</button>
            </div>
            {(visualFilter.metadataModuleId != null || visualFilter.streamModuleId != null || visualFilter.rankingType != null) && <p className="muted">当前范围：{visualFilter.metadataModuleId != null ? "所选模块的已入库内容" : visualFilter.streamModuleId != null ? "所选模块的可播放内容" : "已有榜单"}{visualFilter.rankingType != null ? ` · ${{ ranking: "热门榜", playback: "热播榜", top250: "TOP 250", hot: "热播榜" }[String(visualFilter.rankingType)] ?? "榜单"} · ${{ daily: "日榜", weekly: "周榜", monthly: "月榜", all: "历史总榜" }[String(visualFilter.rankingPeriod)] ?? `${visualFilter.rankingPeriod ?? "全部"} 年`}` : ""}</p>}
            {!visualAvailable && <p className="muted">当前为自定义规则，请在高级 JSON 模式中编辑；原有条件会完整保留。</p>}
            <fieldset className="filter-visual-grid library-visual-fields" disabled={!visualAvailable}>
              <label>
                <span>最低评分要求</span>
                <select value={minRating} onChange={(e) => { const v = e.target.value; syncVisualToFilter({ minRating: v ? Number(v) : null }); }}>
                  <option value="">不限评分</option>
                  {minRating && !["6", "7", "7.5", "8", "8.5"].includes(minRating) && <option value={minRating}>{minRating} 分及以上</option>}
                  <option value="6">6.0 分及以上</option>
                  <option value="7">7.0 分及以上</option>
                  <option value="7.5">7.5 分及以上（推荐）</option>
                  <option value="8">8.0 分及以上（优秀）</option>
                  <option value="8.5">8.5 分及以上（神作）</option>
                </select>
              </label>
              <label>
                <span>发行年份</span>
                <input placeholder="如 2025 或 2024,2025" value={yearsDraft} onChange={(e) => { e.target.setCustomValidity(""); setYearsDraft(e.target.value); }} onBlur={(e) => {
                  const values = yearsDraft.trim() ? yearsDraft.trim().split(/[\s,，]+/).map(Number) : [];
                  if (values.some((year) => !Number.isInteger(year) || year < 1900 || year > 2100)) { e.target.setCustomValidity("请填写 1900 到 2100 之间的年份，用逗号分隔"); e.target.reportValidity(); return; }
                  syncVisualToFilter({ years: values.length ? values : null });
                }} />
              </label>
              <label className="inline-check">
                <input type="checkbox" checked={hasPlayableSource} onChange={(e) => { const v = e.target.checked; syncVisualToFilter({ hasPlayableSource: v }); }} />
                仅包含可播放作品
              </label>
              <label className="inline-check">
                <input type="checkbox" checked={isFavorite} onChange={(e) => { const v = e.target.checked; syncVisualToFilter({ isFavorite: v }); }} />
                仅包含我的收藏
              </label>
            </fieldset>
          </div>

          {showAdvancedJson && (
            <label className="wide-field">
              过滤器 JSON（高级自定义规则）
              <textarea className="code-input" value={form.filterJSON} onChange={(event) => updateForm("filterJSON", event.target.value)} spellCheck={false} rows={4} />
            </label>
          )}

          <label>
            排序字段
            <select value={form.sortField} onChange={(event) => updateForm("sortField", event.target.value)}>
              {Object.entries({ title: "标题", code: "编号", year: "年份", rating: "评分", releaseDate: "发行日期", updatedAt: "最近更新", createdAt: "添加时间" }).map(([value, label]) => <option value={value} key={value}>{label}</option>)}
            </select>
          </label>
          <label>
            排序方向
            <select value={form.sortDirection} onChange={(event) => updateForm("sortDirection", event.target.value)}>
              <option value="desc">降序</option>
              <option value="asc">升序</option>
            </select>
          </label>
          <label>
            海报图片 ID
            <input value={form.posterImageId} onChange={(event) => updateForm("posterImageId", event.target.value)} />
          </label>
          <label className="inline-check">
            <input type="checkbox" checked={form.isPublic} onChange={(event) => updateForm("isPublic", event.target.checked)} />
            公开媒体库（所有客户端和用户默认可见）
          </label>
          <label className="inline-check">
            <input type="checkbox" checked={form.isAdult} onChange={(event) => updateForm("isAdult", event.target.checked)} />
            成人内容
          </label>
          <label className="inline-check">
            <input type="checkbox" checked={form.enabled} onChange={(event) => updateForm("enabled", event.target.checked)} />
            启用
          </label>
          <button className="primary-button" disabled={busy}>{busy ? "保存中…" : editing ? "保存修改" : "创建媒体库"}</button>
          </fieldset>
        </form>
      )}
      {loading ? <p className="muted">正在读取媒体库…</p> : libraries.length === 0 ? <div className="empty-card"><h3>还没有可见媒体库</h3><p className="muted">管理员可以新建媒体库或检查授权。</p></div> : <div className="management-card-grid">{libraries.map((library) => <article className="management-card" key={library.id}><div className="management-card-heading"><div><p className="eyebrow">{library.itemType}</p><h2>{library.name}</h2></div><span className={library.enabled ? "status-active" : "status-disabled"}>{library.enabled ? "启用" : "停用"}</span></div><p className="muted">{library.description || "暂无说明"}</p><div className="management-card-facts"><span>{library.isPublic ? "公开" : "需授权"}</span><span>{library.isAdult ? "成人" : "普通"}</span><span>{library.sort?.field} · {library.sort?.direction}</span>{previewCounts[library.id] != null && <strong>{previewCounts[library.id]} 部匹配作品</strong>}</div><div className="table-actions"><button type="button" className="ghost-button compact-button" onClick={() => void previewLibrary(library)}>预览数量</button>{canManage && <><button type="button" className="ghost-button compact-button" onClick={() => startEdit(library)}>编辑</button><button type="button" className="ghost-button compact-button" onClick={() => void openPermissions(library)}>授权</button><button type="button" className="ghost-button compact-button" disabled={busy} onClick={() => void removeLibrary(library)}>删除</button></>}</div></article>)}</div>}
      {permissionLibrary && <section className="dashboard-panel permission-panel"><div className="home-module-heading"><div><p className="eyebrow">{permissionLibrary.name}</p><h2>可见用户与角色</h2></div><button type="button" className="ghost-button compact-button" onClick={() => setPermissionLibrary(null)}>关闭</button></div><p className="muted">勾选查看或播放权限；公开媒体库仍会受成人内容确认和启用状态限制。</p><div className="permission-list">{permissionSubjects.map((subject) => { const key = `${subject.subjectType}:${subject.subjectId}`; const permission = permissionDraft[key] ?? { subjectType: subject.subjectType, subjectId: subject.subjectId, canView: false, canPlay: false }; return <div className="permission-row" key={key}><span><strong>{subject.name}</strong><small>{subject.detail}</small></span><label className="inline-check"><input type="checkbox" checked={permission.canView} onChange={(event) => updatePermission(subject.subjectType, subject.subjectId, "canView", event.target.checked)} />可查看</label><label className="inline-check"><input type="checkbox" checked={permission.canPlay} onChange={(event) => updatePermission(subject.subjectType, subject.subjectId, "canPlay", event.target.checked)} />可播放</label></div>; })}</div><button type="button" className="primary-button" disabled={busy} onClick={() => void savePermissions()}>{busy ? "保存中…" : "保存授权"}</button></section>}
    </section>
  );
}

type CollectionAdminForm = { name: string; description: string; source: string; sourceId: string; itemIds: string; isPublic: boolean };
const emptyCollectionAdminForm: CollectionAdminForm = { name: "", description: "", source: "manual", sourceId: "", itemIds: "", isPublic: false };
function collectionAdminForm(collection: CollectionRecord): CollectionAdminForm { return { name: collection.name, description: collection.description ?? "", source: collection.source, sourceId: collection.sourceId ?? "", itemIds: "", isPublic: collection.isPublic }; }

function CollectionAdminPage() {
  const session = readSession(); const canManage = userHasRole(session?.user, "Admin", "Editor"); const canCleanup = userHasRole(session?.user, "Admin");
  const [collections, setCollections] = useState<CollectionRecord[]>([]); const [editing, setEditing] = useState<CollectionRecord | null>(null); const [form, setForm] = useState<CollectionAdminForm>(emptyCollectionAdminForm); const [editorOpen, setEditorOpen] = useState(false); const [selected, setSelected] = useState<CollectionRecord | null>(null); const [selectedItems, setSelectedItems] = useState<CollectionItemRecord[]>([]); const [itemCodes, setItemCodes] = useState(""); const [loading, setLoading] = useState(true); const [busy, setBusy] = useState(false); const [error, setError] = useState(""); const [message, setMessage] = useState("");
  async function loadCollections() { setLoading(true); try { const result = await apiFetch<{ items: CollectionRecord[] }>("/api/v1/collections"); setCollections(result.items); setError(""); } catch (requestError) { setError(requestError instanceof Error ? requestError.message : "读取合集失败"); } finally { setLoading(false); } }
  useEffect(() => { void loadCollections(); }, []);
  function updateForm<K extends keyof CollectionAdminForm>(key: K, value: CollectionAdminForm[K]) { setForm((current) => ({ ...current, [key]: value })); }
  function startCreate() { setEditing(null); setForm(emptyCollectionAdminForm); setEditorOpen(true); }
  function startEdit(collection: CollectionRecord) { setEditing(collection); setForm(collectionAdminForm(collection)); setEditorOpen(true); }
  async function saveCollection(event: FormEvent) { event.preventDefault(); setBusy(true); setError(""); setMessage(""); const itemIds = form.itemIds.split(/[\s,，]+/).map((value) => value.trim()).filter(Boolean); const payload = { name: form.name.trim(), description: form.description.trim(), source: form.source, sourceId: form.sourceId.trim(), isPublic: form.isPublic, itemIds }; try { if (editing) await apiFetch(`/api/v1/collections/${editing.id}`, { method: "PATCH", body: JSON.stringify({ name: payload.name, description: payload.description, isPublic: payload.isPublic }) }); else await apiFetch("/api/v1/collections", { method: "POST", body: JSON.stringify(payload) }); setMessage(editing ? "合集已更新" : "合集已创建"); setEditorOpen(false); setEditing(null); setForm(emptyCollectionAdminForm); await loadCollections(); } catch (requestError) { setError(requestError instanceof Error ? requestError.message : "保存合集失败"); } finally { setBusy(false); } }
  async function removeCollection(collection: CollectionRecord) { if (!window.confirm(`确定删除「${collection.name}」吗？请输入确认后继续。`)) return; setBusy(true); setError(""); try { await apiFetch(`/api/v1/collections/${collection.id}?confirm=DELETE`, { method: "DELETE" }); if (selected?.id === collection.id) { setSelected(null); setSelectedItems([]); } setMessage("合集已删除"); await loadCollections(); } catch (requestError) { setError(requestError instanceof Error ? requestError.message : "删除合集失败"); } finally { setBusy(false); } }
  async function showItems(collection: CollectionRecord) { setSelected(collection); setError(""); try { const result = await apiFetch<{ items: CollectionItemRecord[] }>(`/api/v1/collections/${collection.id}/items`); setSelectedItems(result.items); } catch (requestError) { setError(requestError instanceof Error ? requestError.message : "读取合集作品失败"); } }
  async function addItems() { if (!selected) return; const itemIds = itemCodes.split(/[\s,，]+/).map((value) => value.trim()).filter(Boolean); if (!itemIds.length) return; setBusy(true); setError(""); try { await apiFetch(`/api/v1/collections/${selected.id}/items`, { method: "POST", body: JSON.stringify({ itemIds }) }); setItemCodes(""); await showItems(selected); await loadCollections(); setMessage("作品已加入合集"); } catch (requestError) { setError(requestError instanceof Error ? requestError.message : "添加合集作品失败"); } finally { setBusy(false); } }
  async function moveItem(item: CollectionItemRecord, direction: -1 | 1) { if (!selected || !canManage) return; const index = selectedItems.findIndex((value) => value.itemId === item.itemId); const target = index + direction; if (index < 0 || target < 0 || target >= selectedItems.length) return; setBusy(true); setError(""); try { await apiFetch(`/api/v1/collections/${selected.id}/items/${item.itemId}`, { method: "PATCH", body: JSON.stringify({ position: target }) }); await showItems(selected); setMessage("合集顺序已更新"); } catch (requestError) { setError(requestError instanceof Error ? requestError.message : "更新合集顺序失败"); } finally { setBusy(false); } }
  async function removeItem(item: CollectionItemRecord) { if (!selected || !canManage || !window.confirm(`确定从「${selected.name}」移除 ${visibleCode(item)} 吗？`)) return; setBusy(true); setError(""); try { await apiFetch(`/api/v1/collections/${selected.id}/items/${item.itemId}`, { method: "DELETE" }); await showItems(selected); await loadCollections(); setMessage("作品已从合集移除"); } catch (requestError) { setError(requestError instanceof Error ? requestError.message : "移除合集作品失败"); } finally { setBusy(false); } }
  async function clearCollection(collection: CollectionRecord) { if (!canCleanup || !window.confirm(`确定清空「${collection.name}」吗？请输入确认后继续。`)) return; setBusy(true); setError(""); try { await apiFetch(`/api/v1/collections/${collection.id}/clear?confirmation=CLEAR`, { method: "POST" }); if (selected?.id === collection.id) await showItems(collection); await loadCollections(); setMessage("合集已清空"); } catch (requestError) { setError(requestError instanceof Error ? requestError.message : "清空合集失败"); } finally { setBusy(false); } }
  async function cleanupCollections() { if (!window.confirm("确定清理失效关系并删除空合集吗？此操作会写入审计日志。")) return; setBusy(true); setError(""); try { const result = await apiFetch<{ removedItems: number; removedCollections: number }>("/api/v1/collections/cleanup?deleteEmpty=1", { method: "POST", body: JSON.stringify({ confirmation: "CLEANUP_ORPHANS" }) }); setMessage(`清理完成：移除 ${result.removedItems} 条关系，删除 ${result.removedCollections} 个空合集`); await loadCollections(); } catch (requestError) { setError(requestError instanceof Error ? requestError.message : "清理合集失败"); } finally { setBusy(false); } }
  return <section className="content-card management-page"><div className="relation-page-heading"><div><p className="eyebrow">固定合集与清单</p><h1>合集管理</h1><p className="muted">将已入库的电影、剧集和视频整理为固定合集。</p></div><Link to="/settings" className="ghost-button">返回设置</Link></div><div className="admin-toolbar">{canManage && <button type="button" className="primary-button" onClick={startCreate}>新建合集</button>}{canCleanup && <button type="button" className="ghost-button" disabled={busy} onClick={() => void cleanupCollections()}>清理孤儿与空合集</button>}<Link to="/settings/libraries" className="ghost-button">管理媒体库</Link></div>{error && <p className="form-error" role="alert">{error}</p>}{message && <p className="form-success" role="status">{message}</p>}{canManage && editorOpen && <form className="relation-editor" onSubmit={saveCollection}><div className="relation-editor-heading"><div><p className="eyebrow">{editing ? "编辑合集" : "新建合集"}</p><h2>{editing ? editing.name : "固定合集"}</h2></div><button type="button" className="ghost-button" onClick={() => setEditorOpen(false)}>关闭</button></div><label>名称<input required maxLength={200} value={form.name} onChange={(event) => updateForm("name", event.target.value)} /></label><label className="wide-field">说明<textarea maxLength={2000} value={form.description} onChange={(event) => updateForm("description", event.target.value)} /></label><label>来源 ID<input disabled={Boolean(editing)} value={form.sourceId} onChange={(event) => updateForm("sourceId", event.target.value)} /></label><label className="inline-check"><input type="checkbox" checked={form.isPublic} onChange={(event) => updateForm("isPublic", event.target.checked)} />公开合集</label>{!editing && <label className="wide-field">初始媒体 ID（可选，空格或逗号分隔）<textarea value={form.itemIds} placeholder="item_xxx item_yyy" onChange={(event) => updateForm("itemIds", event.target.value)} /></label>}<button className="primary-button" disabled={busy}>{busy ? "保存中…" : editing ? "保存修改" : "创建合集"}</button></form>}{loading ? <p className="muted">正在读取合集…</p> : collections.length === 0 ? <div className="empty-card"><h3>暂无合集</h3><p className="muted">创建合集后，可添加任意已入库媒体。</p></div> : <div className="management-card-grid">{collections.map((collection) => <article className={`management-card ${selected?.id === collection.id ? "selected" : ""}`} key={collection.id}><div className="management-card-heading"><div><p className="eyebrow">{collection.source === "manual" ? "人工合集" : collection.source}</p><h2>{collection.name}</h2></div><span className={collection.isPublic ? "status-active" : "status-disabled"}>{collection.isPublic ? "公开" : "私有"}</span></div><p className="muted">{collection.description || "暂无说明"}</p><div className="management-card-facts"><span>{collection.itemCount} 部作品</span><span>{new Date(collection.updatedAt).toLocaleString()}</span></div><div className="table-actions"><button type="button" className="ghost-button compact-button" onClick={() => void showItems(collection)}>查看作品</button>{canManage && <><button type="button" className="ghost-button compact-button" onClick={() => startEdit(collection)}>编辑</button><button type="button" className="ghost-button compact-button" disabled={busy} onClick={() => void clearCollection(collection)}>清空</button><button type="button" className="ghost-button compact-button" disabled={busy} onClick={() => void removeCollection(collection)}>删除</button></>}</div></article>)}</div>}{selected && <section className="dashboard-panel collection-items-panel"><div className="home-module-heading"><div><p className="eyebrow">{selected.name}</p><h2>合集作品</h2></div><span className="muted">{selectedItems.length} 部</span></div>{canManage && <div className="admin-toolbar"><input className="compact-input collection-code-input" placeholder="输入媒体 ID，可用空格分隔" value={itemCodes} onChange={(event) => setItemCodes(event.target.value)} /><button type="button" className="primary-button compact-button" disabled={busy || !itemCodes.trim()} onClick={() => void addItems()}>添加作品</button></div>}{selectedItems.length === 0 ? <p className="muted">暂无可见作品</p> : <ol className="collection-item-list">{selectedItems.map((item, index) => <li key={item.itemId}><span><strong>{item.position + 1}. {visibleCode(item)}</strong><small>{item.title}</small></span><div className="table-actions"><button type="button" className="ghost-button compact-button" disabled={!canManage || busy || index === 0} onClick={() => void moveItem(item, -1)}>上移</button><button type="button" className="ghost-button compact-button" disabled={!canManage || busy || index === selectedItems.length - 1} onClick={() => void moveItem(item, 1)}>下移</button><button type="button" className="ghost-button compact-button" disabled={!canManage || busy} onClick={() => void removeItem(item)}>移除</button><Link to={`/items/${item.itemId}`} className="ghost-button compact-button">打开作品</Link></div></li>)}</ol>}</section>}</section>;
}


function SettingsPage() {
  const session = readSession();
  const location = useLocation();
  const path = location.pathname.replace(/\/+$/, "");
  if (!session) return <Navigate to="/login" replace />;
  if (path === "/settings") return <SettingsCenterPage user={session.user} />;
  const entry = settingsEntries.find((item) => path === "/settings/" + item.id);
  if (!entry) return <NotFoundPage />;
  if (entry.roles.length > 0 && !userHasRole(session.user, ...entry.roles)) return <section className="content-card"><p className="eyebrow">设置</p><h1>没有管理权限</h1><p className="muted">当前账号不能使用这项功能，请联系管理员授权。</p><Link to="/settings" className="ghost-button">返回设置</Link></section>;
  const pages: Record<string, ReactNode> = {
    profile: <ProfileSettingsPage />, discover: <DiscoverPage />, dashboard: <AdminDashboardPage />,
    libraries: <LibraryAdminPage />, collections: <CollectionAdminPage />, users: <UserAdminPage />,
    relations: <RelationAdminPage />, tasks: <TaskAdminPage />, logs: <AuditLogsPage />,
    statistics: <StatisticsAdminPage />, backup: <BackupAdminPage />, sources: <SourceAdminPage />,
  };
  return pages[entry.id];
}

function ModuleManagementPage() {
  const session = readSession();
  if (!session) return <Navigate to="/login" replace />;
  if (!userHasRole(session.user, "Admin")) return <section className="content-card"><p className="eyebrow">模块设置</p><h1>没有管理权限</h1><p className="muted">模块连接和凭证只允许管理员维护。</p><Link to="/settings" className="ghost-button">返回设置</Link></section>;
  return <ModuleCenterPage />;
}

function LegacyAdminRedirect() {
  const location = useLocation();
  const path = location.pathname.replace(/\/+$/, "");
  const id = path.slice("/admin/".length);
  let target: string;
  const params = new URLSearchParams(location.search);
  if (path === "/admin") target = "/settings";
  else if (id === "items") { target = "/admin/modules"; }
  else if (id === "providers") target = "/admin/modules";
  else if (id === "data") target = "/settings/backup";
  else if (settingsEntries.some((entry) => entry.id === id)) target = "/settings/" + id;
  else return <NotFoundPage />;
  return <Navigate to={{ pathname: target, search: params.toString() ? "?" + params.toString() : "", hash: location.hash }} replace />;
}

const taskStatusLabels: Record<string, string> = { queued: "排队中", running: "运行中", succeeded: "已完成", partial: "部分完成", failed: "失败", cancelled: "已取消" };

function TaskAdminPage() {
  const [tasks, setTasks] = useState<TaskRecord[]>([]);
  const [total, setTotal] = useState(0);
  const [status, setStatus] = useState("");
  const [type, setType] = useState("");
  const [limit, setLimit] = useState(20);
  const [offset, setOffset] = useState(0);
  const [selectedTask, setSelectedTask] = useState<TaskRecord | null>(null);
  const [backupRun, setBackupRun] = useState<BackupRunRecord | null>(null);
  const [backupRunError, setBackupRunError] = useState("");
  const [loading, setLoading] = useState(true);
  const [detailLoading, setDetailLoading] = useState(false);
  const [busyTask, setBusyTask] = useState("");
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");

  async function loadTasks() {
    setLoading(true);
    try {
      const params = new URLSearchParams({ limit: String(limit), offset: String(offset) });
      if (status) params.set("status", status);
      if (type.trim()) params.set("type", type.trim());
      const result = await apiFetch<{ items: TaskRecord[]; total: number }>(`/api/v1/tasks?${params.toString()}`);
      setTasks(result.items);
      setTotal(result.total);
      setError("");
    } catch (requestError) {
      setError(requestError instanceof Error ? requestError.message : "读取任务失败");
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    void loadTasks();
    const timer = window.setInterval(() => { void loadTasks(); }, 5000);
    return () => window.clearInterval(timer);
  }, [status, type, limit, offset]);

  async function cancelTask(taskID: string) {
    setBusyTask(taskID);
    setMessage("");
    try {
      await apiFetch<TaskRecord>(`/api/v1/tasks/${taskID}/cancel`, { method: "POST" });
      setMessage("任务已取消");
      await loadTasks();
    } catch (requestError) {
      setError(requestError instanceof Error ? requestError.message : "取消任务失败");
    } finally {
      setBusyTask("");
    }
  }

  async function openTask(taskID: string) {
    setDetailLoading(true); setError("");
    setBackupRun(null); setBackupRunError("");
    try {
      const task = await apiFetch<TaskRecord>(`/api/v1/tasks/${taskID}`);
      setSelectedTask(task);
      if (task.type === "backup") {
        try { setBackupRun(await apiFetch<BackupRunRecord>(`/api/v1/backup/runs/${encodeURIComponent(taskID)}`)); }
        catch (requestError) { setBackupRunError(requestError instanceof Error ? requestError.message : "读取备份目标结果失败"); }
      }
    }
    catch (requestError) { setError(requestError instanceof Error ? requestError.message : "读取任务详情失败"); }
    finally { setDetailLoading(false); }
  }

  async function retryTask(taskID: string) {
    setBusyTask(taskID); setMessage(""); setError("");
    try { const task = await apiFetch<TaskRecord>(`/api/v1/tasks/${taskID}/retry`, { method: "POST" }); setSelectedTask(task); setBackupRun(null); setBackupRunError(""); setMessage("任务已重新排队"); await loadTasks(); }
    catch (requestError) { setError(requestError instanceof Error ? requestError.message : "重试任务失败"); }
    finally { setBusyTask(""); }
  }

  function resetFilters(event: FormEvent) { event.preventDefault(); setStatus(""); setType(""); setOffset(0); }
  const page = Math.floor(offset / limit) + 1; const pages = Math.max(1, Math.ceil(total / limit));
  return <section className="content-card task-admin-page"><div className="relation-page-heading"><div><p className="eyebrow">后台任务</p><h1>同步任务中心</h1><p className="muted">任务会自动刷新；失败、部分成功和取消任务可以从详情页重新排队。</p></div><Link to="/settings" className="ghost-button">返回设置</Link></div><form className="admin-filter-bar" onSubmit={resetFilters}><label>状态<select value={status} onChange={(event) => { setStatus(event.target.value); setOffset(0); }}><option value="">全部状态</option>{Object.entries(taskStatusLabels).map(([key, label]) => <option key={key} value={key}>{label}</option>)}</select></label><label>任务类型<input placeholder="sync_item" value={type} onChange={(event) => setType(event.target.value)} /></label><label>每页<select value={limit} onChange={(event) => { setLimit(Number(event.target.value)); setOffset(0); }}><option value={20}>20</option><option value={50}>50</option><option value={100}>100</option></select></label><button className="ghost-button compact-button">应用筛选</button></form>{error && <p className="form-error" role="alert">{error}</p>}{message && <p className="form-success" role="status">{message}</p>}{loading ? <p className="muted">正在读取任务…</p> : tasks.length === 0 ? <div className="empty-card"><h3>暂无匹配任务</h3><p className="muted">调整筛选条件或从作品详情发起同步。</p></div> : <><div className="table-wrap"><table className="task-table"><thead><tr><th>任务</th><th>状态</th><th>进度</th><th>重试</th><th>错误</th><th>更新时间</th><th>操作</th></tr></thead><tbody>{tasks.map((task) => <tr key={task.id}><td><button type="button" className="table-link" onClick={() => void openTask(task.id)}><strong>{task.type === "backup" ? "备份" : task.type}</strong><small>{task.id}</small></button></td><td><span className={`task-status task-status-${task.status}`}>{taskStatusLabels[task.status] ?? task.status}</span></td><td><div className="task-progress"><span style={{ width: `${task.progress}%` }} /></div><small>{task.progress}%</small></td><td>{task.retryCount}/{task.maxRetries}</td><td>{task.errorMessage ? <span title={task.errorCode}>{task.errorMessage}</span> : <span className="muted">—</span>}</td><td>{new Date(task.updatedAt).toLocaleString()}</td><td><div className="table-actions">{task.canCancel && <button type="button" className="ghost-button compact-button" disabled={busyTask === task.id} onClick={() => void cancelTask(task.id)}>{busyTask === task.id ? "取消中…" : "取消"}</button>}{task.canRetry && <button type="button" className="ghost-button compact-button" disabled={busyTask === task.id} onClick={() => void retryTask(task.id)}>重试</button>}</div></td></tr>)}</tbody></table></div><div className="pagination-bar"><span className="muted">共 {total} 条 · 第 {page}/{pages} 页</span><div className="table-actions"><button type="button" className="ghost-button compact-button" disabled={offset === 0} onClick={() => setOffset(Math.max(0, offset - limit))}>上一页</button><button type="button" className="ghost-button compact-button" disabled={offset + limit >= total} onClick={() => setOffset(offset + limit)}>下一页</button></div></div></>}{selectedTask && <section className="dashboard-panel task-detail-panel"><div className="home-module-heading"><div><p className="eyebrow">任务详情</p><h2>{selectedTask.type === "backup" ? "备份" : selectedTask.type}</h2><p className="muted">{selectedTask.id} · {taskStatusLabels[selectedTask.status] ?? selectedTask.status}</p></div><button type="button" className="ghost-button compact-button" onClick={() => { setSelectedTask(null); setBackupRun(null); setBackupRunError(""); }}>关闭</button></div>{detailLoading ? <p className="muted">正在读取 attempt 历史…</p> : <><div className="dashboard-error-counts"><span>进度<strong>{selectedTask.progress}%</strong></span><span>重试<strong>{selectedTask.retryCount}/{selectedTask.maxRetries}</strong></span></div>{selectedTask.errorMessage && <p className="form-error">{selectedTask.errorCode ? `${selectedTask.errorCode}：` : ""}{selectedTask.errorMessage}</p>}{selectedTask.type === "backup" && <section className="dashboard-panel"><div className="home-module-heading"><div><p className="eyebrow">备份目标结果</p><h3>{backupRun?.artifactName || "备份归档"}</h3></div><span className={`task-status task-status-${backupRun?.taskStatus || selectedTask.status}`}>{taskStatusLabels[backupRun?.taskStatus || selectedTask.status] ?? backupRun?.taskStatus}</span></div>{backupRunError && <p className="form-error" role="alert">{backupRunError}</p>}{backupRun && <><p className="muted">归档 {backupRun.artifactSize ?? 0} 字节 · SHA256 {backupRun.artifactSha256 || "生成中"}{backupRun.includeUserState ? " · 含用户状态" : ""}{backupRun.includeEncryptedConfig ? " · 含加密目标配置" : ""}</p><div className="table-wrap"><table className="management-table"><thead><tr><th>目标</th><th>类型</th><th>状态</th><th>归档路径</th><th>写入大小</th><th>结果</th></tr></thead><tbody>{backupRun.targets.map((target) => <tr key={target.targetId}><td>{target.displayName}<small>{target.targetId}</small></td><td>{target.targetType === "local" ? "本地" : target.targetType === "webdav" ? "WebDAV" : "Git"}</td><td><span className={`task-status task-status-${target.status}`}>{taskStatusLabels[target.status] ?? target.status}</span></td><td><code>{target.artifactPath || "—"}</code></td><td>{target.bytesWritten} 字节</td><td>{target.errorMessage ? <span className="form-error">{target.errorMessage}</span> : target.checksum ? <small>SHA256 {target.checksum}</small> : <span className="muted">—</span>}</td></tr>)}</tbody></table></div></>}</section>}<div className="attempt-list">{selectedTask.attempts?.length ? selectedTask.attempts.map((attempt) => <article className="attempt-card" key={attempt.id}><div><strong>Attempt #{attempt.attemptNumber}</strong><small>{taskStatusLabels[attempt.status] ?? attempt.status} · {new Date(attempt.startedAt).toLocaleString()}</small></div><span>{attempt.errorCode || "—"}</span>{attempt.errorMessage && <p className="muted">{attempt.errorMessage}</p>}</article>) : <p className="muted">暂无 attempt 历史</p>}</div>{selectedTask.canRetry && <button type="button" className="primary-button" disabled={busyTask === selectedTask.id} onClick={() => void retryTask(selectedTask.id)}>重新排队</button>}</>}</section>}</section>;
}

function AuditLogsPage() {
  const [items, setItems] = useState<AuditLogRecord[]>([]); const [total, setTotal] = useState(0); const [action, setAction] = useState(""); const [resourceType, setResourceType] = useState(""); const [resourceId, setResourceId] = useState(""); const [actorUserId, setActorUserId] = useState(""); const [result, setResult] = useState(""); const [from, setFrom] = useState(""); const [to, setTo] = useState(""); const [limit, setLimit] = useState(30); const [offset, setOffset] = useState(0); const [loading, setLoading] = useState(true); const [error, setError] = useState("");
  async function loadLogs() { setLoading(true); try { const params = new URLSearchParams({ limit: String(limit), offset: String(offset) }); if (action.trim()) params.set("action", action.trim()); if (resourceType.trim()) params.set("resourceType", resourceType.trim()); if (resourceId.trim()) params.set("resourceId", resourceId.trim()); if (actorUserId.trim()) params.set("actorUserId", actorUserId.trim()); if (result) params.set("result", result); if (from) params.set("from", new Date(from).toISOString()); if (to) params.set("to", new Date(to).toISOString()); const response = await apiFetch<{ items: AuditLogRecord[]; total: number }>(`/api/v1/audit-logs?${params.toString()}`); setItems(response.items); setTotal(response.total); setError(""); } catch (requestError) { setError(requestError instanceof Error ? requestError.message : "读取审计日志失败"); } finally { setLoading(false); } }
  useEffect(() => { void loadLogs(); }, [limit, offset, action, resourceType, resourceId, actorUserId, result, from, to]);
  const pages = Math.max(1, Math.ceil(total / limit)); const page = Math.floor(offset / limit) + 1;
  return <section className="content-card management-page"><div className="relation-page-heading"><div><p className="eyebrow">日志中心</p><h1>审计日志</h1><p className="muted">展示登录、同步、权限、解析和管理操作；敏感元数据由服务端脱敏。</p></div><Link to="/settings" className="ghost-button">返回设置</Link></div><div className="admin-filter-bar"><label>操作<input placeholder="create_library" value={action} onChange={(event) => { setAction(event.target.value); setOffset(0); }} /></label><label>资源类型<input placeholder="library" value={resourceType} onChange={(event) => { setResourceType(event.target.value); setOffset(0); }} /></label><label>资源 ID<input placeholder="library_xxx" value={resourceId} onChange={(event) => { setResourceId(event.target.value); setOffset(0); }} /></label><label>操作者 ID<input placeholder="user_xxx" value={actorUserId} onChange={(event) => { setActorUserId(event.target.value); setOffset(0); }} /></label><label>结果<select value={result} onChange={(event) => { setResult(event.target.value); setOffset(0); }}><option value="">全部</option><option value="success">成功</option><option value="failed">失败</option></select></label><label>开始时间<input type="datetime-local" value={from} onChange={(event) => { setFrom(event.target.value); setOffset(0); }} /></label><label>结束时间<input type="datetime-local" value={to} onChange={(event) => { setTo(event.target.value); setOffset(0); }} /></label><label>每页<select value={limit} onChange={(event) => { setLimit(Number(event.target.value)); setOffset(0); }}><option value={30}>30</option><option value={50}>50</option><option value={100}>100</option></select></label></div>{error && <p className="form-error" role="alert">{error}</p>}{loading ? <p className="muted">正在读取日志…</p> : items.length === 0 ? <div className="empty-card"><h3>暂无日志</h3><p className="muted">没有匹配当前筛选条件的审计记录。</p></div> : <><div className="table-wrap"><table className="management-table audit-table"><thead><tr><th>时间</th><th>操作者</th><th>操作</th><th>资源</th><th>结果</th><th>元数据</th></tr></thead><tbody>{items.map((item) => <tr key={item.id}><td>{new Date(item.createdAt).toLocaleString()}</td><td>{item.actorName || item.actorUsername || item.actorUserId || "系统"}</td><td><strong>{item.action}</strong><small>{item.requestId || "—"}</small></td><td>{item.resourceType}<small>{item.resourceId || "—"}</small></td><td><span className={item.result === "success" ? "status-active" : "status-disabled"}>{item.result}</span></td><td><code className="metadata-preview">{formatAuditMetadata(item.metadata)}</code></td></tr>)}</tbody></table></div><div className="pagination-bar"><span className="muted">共 {total} 条 · 第 {page}/{pages} 页</span><div className="table-actions"><button type="button" className="ghost-button compact-button" disabled={offset === 0} onClick={() => setOffset(Math.max(0, offset - limit))}>上一页</button><button type="button" className="ghost-button compact-button" disabled={offset + limit >= total} onClick={() => setOffset(offset + limit)}>下一页</button></div></div></>}</section>;
}

function formatAuditMetadata(value: unknown): string { try { const encoded = JSON.stringify(value); return encoded && encoded !== "{}" ? encoded : "—"; } catch { return "—"; } }

function StatisticsAdminPage() {
  const [overview, setOverview] = useState<StatisticsOverviewData | null>(null); const [items, setItems] = useState<StatisticsItemsData | null>(null); const [libraryId, setLibraryId] = useState(""); const [limit, setLimit] = useState(10); const [loading, setLoading] = useState(true); const [error, setError] = useState("");
  async function loadStatistics() { setLoading(true); try { const query = libraryId.trim() ? `?libraryId=${encodeURIComponent(libraryId.trim())}` : ""; const itemsQuery = `${query ? `${query}&` : "?"}limit=${limit}`; const [overviewResult, itemsResult] = await Promise.all([apiFetch<StatisticsOverviewData>(`/api/v1/statistics/overview${query}`), apiFetch<StatisticsItemsData>(`/api/v1/statistics/items${itemsQuery}`)]); setOverview(overviewResult); setItems(itemsResult); setError(""); } catch (requestError) { setError(requestError instanceof Error ? requestError.message : "读取统计失败"); } finally { setLoading(false); } }
  useEffect(() => { void loadStatistics(); }, [libraryId, limit]);
  const distribution = (title: string, buckets: DistributionBucket[]) => <section className="dashboard-panel"><div className="home-module-heading"><h2>{title}</h2><span className="muted">{buckets.length} 组</span></div><div className="stat-bar-list">{buckets.slice(0, 10).map((bucket) => { const max = Math.max(1, ...buckets.map((item) => item.count)); return <div className="stat-bar-row" key={bucket.key}><span>{bucket.label}</span><div className="stat-bar"><i style={{ width: `${Math.round((bucket.count / max) * 100)}%` }} /></div><strong>{bucket.count}</strong></div>; })}</div></section>;
  const ranking = (title: string, values: Array<{ id: string; name: string; count: number }>) => <section className="dashboard-panel"><div className="home-module-heading"><h2>{title}</h2><span className="muted">Top {values.length}</span></div><ol className="ranking-list">{values.slice(0, 10).map((value) => <li key={value.id}><span>{value.name}</span><strong>{value.count}</strong></li>)}</ol></section>;
  return <section className="content-card management-page"><div className="relation-page-heading"><div><p className="eyebrow">统计中心</p><h1>媒体库统计</h1><p className="muted">统计范围沿用管理角色可见媒体库；播放时长和源健康来自用户状态与探测记录。</p></div><Link to="/settings" className="ghost-button">返回设置</Link></div><div className="admin-filter-bar"><label>媒体库 ID（可选）<input placeholder="library_xxx" value={libraryId} onChange={(event) => setLibraryId(event.target.value)} /></label><label>排行数量<select value={limit} onChange={(event) => setLimit(Number(event.target.value))}><option value={10}>10</option><option value={20}>20</option><option value={50}>50</option></select></label></div>{error && <p className="form-error" role="alert">{error}</p>}{loading && !overview ? <p className="muted">正在读取统计…</p> : overview && items && <><div className="dashboard-stat-grid"><div className="dashboard-stat"><span>可见作品</span><strong>{overview.stats.visibleItems}</strong></div><div className="dashboard-stat"><span>可播放作品</span><strong>{overview.stats.playableItems}</strong></div><div className="dashboard-stat"><span>已看作品</span><strong>{overview.watchedItems}</strong></div><div className="dashboard-stat"><span>收藏作品</span><strong>{overview.favoriteItems}</strong></div><div className="dashboard-stat"><span>播放时长</span><strong>{Math.round(overview.watchedDurationSeconds / 60)} 分钟</strong></div><div className="dashboard-stat"><span>源成功率</span><strong>{Math.round(overview.sourceHealth.successRate * 100)}%</strong></div></div><div className="admin-dashboard-columns">{distribution("评分分布", overview.ratingDistribution)}{distribution("年代分布", overview.yearDistribution)}</div><div className="admin-dashboard-columns">{ranking("演员排行", items.people)}{ranking("片商排行", items.studios)}</div><div className="admin-dashboard-columns">{ranking("系列排行", items.series)}{ranking("标签排行", items.tags)}</div></>}</section>;
}

type RelationAdminForm = { name: string; aliases: string; sourceName: string; sourceUrl: string; gender: string; status: string; birthday: string; heightCm: string; cupSize: string; bust: string; waist: string; hip: string; biography: string; parentId: string; tagKind: string; isExcluded: boolean };

const emptyRelationAdminForm: RelationAdminForm = { name: "", aliases: "", sourceName: "", sourceUrl: "", gender: "", status: "unknown", birthday: "", heightCm: "", cupSize: "", bust: "", waist: "", hip: "", biography: "", parentId: "", tagKind: "custom", isExcluded: false };

function relationAdminForm(entity: RelationEntity): RelationAdminForm {
  return { name: entity.name, aliases: entity.aliases?.join(", ") ?? "", sourceName: entity.sourceName ?? "", sourceUrl: entity.sourceUrl ?? "", gender: entity.gender ?? "", status: entity.status ?? "unknown", birthday: entity.birthday ?? "", heightCm: entity.heightCm?.toString() ?? "", cupSize: entity.cupSize ?? "", bust: entity.measurements?.bust ?? "", waist: entity.measurements?.waist ?? "", hip: entity.measurements?.hip ?? "", biography: entity.biography ?? "", parentId: entity.parentId ?? "", tagKind: entity.tagKind ?? "custom", isExcluded: entity.isExcluded };
}

function RelationAdminPage() {
  const [kind, setKind] = useState<RelationKind>("people");
  const [query, setQuery] = useState("");
  const [submittedQuery, setSubmittedQuery] = useState("");
  const [includeExcluded, setIncludeExcluded] = useState(true);
  const [entities, setEntities] = useState<RelationEntity[]>([]);
  const [selected, setSelected] = useState<string[]>([]);
  const [mergeTargetId, setMergeTargetId] = useState("");
  const [editing, setEditing] = useState<RelationEntity | null>(null);
  const [editorOpen, setEditorOpen] = useState(false);
  const [form, setForm] = useState<RelationAdminForm>(emptyRelationAdminForm);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const label = relationLabels[kind];

  async function loadEntities() {
    setLoading(true);
    setError("");
    try {
      const suffix = `?limit=200&includeExcluded=${includeExcluded ? "1" : "0"}${submittedQuery ? `&q=${encodeURIComponent(submittedQuery)}` : ""}`;
      const result = await apiFetch<{ items: RelationEntity[] }>(`/api/v1/${kind}${suffix}`);
      setEntities(result.items);
      setSelected([]);
      setMergeTargetId("");
    } catch (requestError) {
      setError(requestError instanceof Error ? requestError.message : `读取${label}失败`);
    } finally { setLoading(false); }
  }
  useEffect(() => { void loadEntities(); }, [kind, includeExcluded, submittedQuery]);
  function chooseKind(next: RelationKind) { setKind(next); setEditing(null); setEditorOpen(false); setForm(emptyRelationAdminForm); }
  function toggleSelected(id: string) { setSelected((current) => current.includes(id) ? current.filter((value) => value !== id) : [...current, id]); }
  function toggleAll() { setSelected(selected.length === entities.length ? [] : entities.map((entity) => entity.id)); }
  function startEdit(entity: RelationEntity) { setEditing(entity); setEditorOpen(true); setForm(relationAdminForm(entity)); }
  function startCreate() { setEditing(null); setEditorOpen(true); setForm(emptyRelationAdminForm); }
  function updateForm<K extends keyof RelationAdminForm>(key: K, value: RelationAdminForm[K]) { setForm((current) => ({ ...current, [key]: value })); }
  async function saveEntity(event: FormEvent) {
    event.preventDefault();
    if (!form.name.trim()) { setError("名称不能为空"); return; }
    setBusy(true); setError("");
    const payload: Record<string, unknown> = { name: form.name.trim(), aliases: form.aliases.split(",").map((value) => value.trim()).filter(Boolean), isExcluded: form.isExcluded };
    if (kind === "people") Object.assign(payload, { gender: form.gender, status: form.status, birthday: form.birthday, heightCm: form.heightCm ? Number(form.heightCm) : null, cupSize: form.cupSize, measurements: Object.fromEntries([["bust", form.bust], ["waist", form.waist], ["hip", form.hip]].filter(([, value]) => value)), biography: form.biography, sourceUrl: form.sourceUrl });
    if (kind === "tags") Object.assign(payload, { sourceName: form.sourceName, tagKind: form.tagKind, parentId: form.parentId, clearParent: !form.parentId });
    if (kind === "studios" || kind === "series") Object.assign(payload, { sourceName: form.sourceName });
    try {
      await apiFetch<RelationEntity>(editing ? `/api/v1/${kind}/${editing.id}` : `/api/v1/${kind}`, { method: editing ? "PATCH" : "POST", body: JSON.stringify(payload) });
      setEditing(null); setEditorOpen(false); setForm(emptyRelationAdminForm); await loadEntities();
    } catch (requestError) { setError(requestError instanceof Error ? requestError.message : "保存关系实体失败"); } finally { setBusy(false); }
  }
  async function batch(action: "exclude" | "include") {
    if (selected.length === 0) return;
    setBusy(true); setError("");
    try { await apiFetch(`/api/v1/${kind}/batch`, { method: "POST", body: JSON.stringify({ ids: selected, action }) }); await loadEntities(); } catch (requestError) { setError(requestError instanceof Error ? requestError.message : "批量更新失败"); } finally { setBusy(false); }
  }
  async function mergeEntity() {
    if (selected.length !== 1 || !mergeTargetId) return;
    setBusy(true); setError("");
    try { await apiFetch(`/api/v1/${kind}/${selected[0]}/merge`, { method: "POST", body: JSON.stringify({ targetId: mergeTargetId }) }); await loadEntities(); } catch (requestError) { setError(requestError instanceof Error ? requestError.message : "合并关系实体失败"); } finally { setBusy(false); }
  }
  const canMerge = selected.length === 1 && Boolean(mergeTargetId);
  return <section className="content-card relation-admin-page"><div className="relation-page-heading"><div><p className="eyebrow">人物与分类</p><h1>编辑、排除与合并</h1></div><Link to="/settings" className="ghost-button">返回设置</Link></div><div className="relation-admin-tabs">{(Object.keys(relationLabels) as RelationKind[]).map((value) => <button type="button" className={kind === value ? "active" : ""} onClick={() => chooseKind(value)} key={value}>{relationLabels[value]}</button>)}</div><div className="admin-toolbar"><form className="search-row" onSubmit={(event) => { event.preventDefault(); setSubmittedQuery(query.trim()); }}><input aria-label={`搜索${label}`} placeholder={`搜索${label}`} value={query} onChange={(event) => setQuery(event.target.value)} /><button className="primary-button" type="submit">搜索</button></form><label className="inline-check"><input type="checkbox" checked={includeExcluded} onChange={(event) => setIncludeExcluded(event.target.checked)} />显示已排除</label><button type="button" className="primary-button" onClick={startCreate}>新建{label}</button></div>{error && <p className="form-error" role="alert">{error}</p>}<div className="batch-toolbar"><span className="muted">已选 {selected.length} 项</span><button type="button" className="ghost-button" disabled={busy || selected.length === 0} onClick={() => batch("exclude")}>批量排除</button><button type="button" className="ghost-button" disabled={busy || selected.length === 0} onClick={() => batch("include")}>批量启用</button>{selected.length === 1 && <><select aria-label="合并目标" value={mergeTargetId} onChange={(event) => setMergeTargetId(event.target.value)}><option value="">选择合并目标</option>{entities.filter((entity) => entity.id !== selected[0]).map((entity) => <option value={entity.id} key={entity.id}>{entity.name}</option>)}</select><button type="button" className="ghost-button" disabled={busy || !canMerge} onClick={mergeEntity}>合并</button></>}</div>{loading ? <p className="muted">正在读取{label}…</p> : <div className="table-wrap"><table className="relation-admin-table"><thead><tr><th><input type="checkbox" aria-label="全选" checked={entities.length > 0 && selected.length === entities.length} onChange={toggleAll} /></th><th>名称</th><th>来源</th><th>状态</th><th>操作</th></tr></thead><tbody>{entities.map((entity) => <tr key={entity.id} className={entity.isExcluded ? "is-excluded" : ""}><td><input type="checkbox" checked={selected.includes(entity.id)} onChange={() => toggleSelected(entity.id)} /></td><td><strong>{entity.name}</strong>{entity.aliases && entity.aliases.length > 0 && <small>{entity.aliases.join(" · ")}</small>}</td><td>{entity.sourceName || entity.sourceUrl || "手工"}</td><td>{entity.isExcluded ? <span className="status-disabled">已排除</span> : <span className="status-active">启用</span>}</td><td><button type="button" className="ghost-button compact-button" onClick={() => startEdit(entity)}>编辑</button></td></tr>)}</tbody></table></div>}{editorOpen && <form className="relation-editor" onSubmit={saveEntity}><div className="relation-editor-heading"><div><p className="eyebrow">{editing ? "编辑关系实体" : `新建${label}`}</p><h2>{editing ? editing.name : `补录${label}`}</h2></div><button type="button" className="ghost-button" onClick={() => { setEditing(null); setEditorOpen(false); setForm(emptyRelationAdminForm); }}>关闭</button></div><label>名称<input required value={form.name} onChange={(event) => updateForm("name", event.target.value)} /></label><label>别名（逗号分隔）<input value={form.aliases} onChange={(event) => updateForm("aliases", event.target.value)} /></label>{(kind === "studios" || kind === "series" || kind === "tags") && <label>来源名称<input value={form.sourceName} onChange={(event) => updateForm("sourceName", event.target.value)} /></label>}{kind === "people" && <><label>性别<input value={form.gender} onChange={(event) => updateForm("gender", event.target.value)} /></label><label>状态<select value={form.status} onChange={(event) => updateForm("status", event.target.value)}><option value="active">活跃</option><option value="retired">隐退</option><option value="unknown">未知</option></select></label><label>生日<input type="date" value={form.birthday} onChange={(event) => updateForm("birthday", event.target.value)} /></label><label>身高（cm）<input type="number" min="0" max="300" value={form.heightCm} onChange={(event) => updateForm("heightCm", event.target.value)} /></label><label>罩杯<input value={form.cupSize} onChange={(event) => updateForm("cupSize", event.target.value)} /></label><label>胸围<input value={form.bust} onChange={(event) => updateForm("bust", event.target.value)} /></label><label>腰围<input value={form.waist} onChange={(event) => updateForm("waist", event.target.value)} /></label><label>臀围<input value={form.hip} onChange={(event) => updateForm("hip", event.target.value)} /></label><label>来源链接<input type="url" value={form.sourceUrl} onChange={(event) => updateForm("sourceUrl", event.target.value)} /></label><label>简介<textarea value={form.biography} onChange={(event) => updateForm("biography", event.target.value)} /></label></>}{kind === "tags" && <><label>标签类型<select value={form.tagKind} onChange={(event) => updateForm("tagKind", event.target.value)}><option value="genre">类别</option><option value="theme">主题</option><option value="source">来源</option><option value="custom">自定义</option></select></label><label>父级标签 ID<input value={form.parentId} onChange={(event) => updateForm("parentId", event.target.value)} /></label></>}<label className="inline-check"><input type="checkbox" checked={form.isExcluded} onChange={(event) => updateForm("isExcluded", event.target.checked)} />排除此实体</label><button className="primary-button" disabled={busy}>{busy ? "保存中…" : "保存"}</button></form>}</section>;
}

function SearchPage() {
  useScrollRestoration("search");
  const navigate = useNavigate();
  const initial = readSearchStateFromURL() ?? readSearchState();
  const [query, setQuery] = useState(initial.query); const [items, setItems] = useState<CatalogItem[]>([]); const [error, setError] = useState(""); const [busy, setBusy] = useState(false); const [options, setOptions] = useState<Record<RelationKind, RelationEntity[]>>({ people: [], studios: [], series: [], tags: [] });
  const [actorIds, setActorIds] = useState<string[]>(initial.actorIds); const [directorIds, setDirectorIds] = useState<string[]>(initial.directorIds); const [studioIds, setStudioIds] = useState<string[]>(initial.studioIds); const [seriesIds, setSeriesIds] = useState<string[]>(initial.seriesIds); const [tagIds, setTagIds] = useState<string[]>(initial.tagIds); const [excludeTagIds, setExcludeTagIds] = useState<string[]>(initial.excludeTagIds); const [years, setYears] = useState(initial.years); const [minRating, setMinRating] = useState(initial.minRating); const [favoriteFilter, setFavoriteFilter] = useState(initial.favoriteFilter); const [watchedFilter, setWatchedFilter] = useState(initial.watchedFilter); const [playableFilter, setPlayableFilter] = useState(initial.playableFilter);
  const [submittedQuery, setSubmittedQuery] = useState(initial.submittedQuery);
  const [hints, setHints] = useState<SearchHint[]>([]); const [showHints, setShowHints] = useState(false); const [history, setHistory] = useState<string[]>(readSearchHistory); const [resultCount, setResultCount] = useState<number | null>(null); const [shareStatus, setShareStatus] = useState("");
  useEffect(() => { sessionStorage.setItem("jmlab.search-state", JSON.stringify({ query, submittedQuery, actorIds, directorIds, studioIds, seriesIds, tagIds, excludeTagIds, years, minRating, favoriteFilter, watchedFilter, playableFilter } satisfies SearchState)); }, [query, submittedQuery, actorIds, directorIds, studioIds, seriesIds, tagIds, excludeTagIds, years, minRating, favoriteFilter, watchedFilter, playableFilter]);
  useEffect(() => { Promise.all((Object.keys(relationLabels) as RelationKind[]).map(async (kind) => [kind, (await apiFetch<{ items: RelationEntity[] }>(`/api/v1/${kind}?limit=100`)).items] as const)).then((values) => setOptions(Object.fromEntries(values) as Record<RelationKind, RelationEntity[]>)).catch(() => undefined); }, []);
  useEffect(() => {
    const value = query.trim();
    if (value.length < 2) { setHints([]); return; }
    const timer = window.setTimeout(() => { apiFetch<{ items: SearchHint[] }>(`/api/v1/search/hints?q=${encodeURIComponent(value)}&limit=8`).then((result) => setHints(result.items)).catch(() => setHints([])); }, 180);
    return () => window.clearTimeout(timer);
  }, [query]);
  useEffect(() => {
    if (!initial.submittedQuery) return;
    const params = new URLSearchParams({ q: initial.query, limit: "48" });
    const filters: Array<[string, string]> = [["actorIds", initial.actorIds.join(",")], ["directorIds", initial.directorIds.join(",")], ["studioIds", initial.studioIds.join(",")], ["seriesIds", initial.seriesIds.join(",")], ["tagIds", initial.tagIds.join(",")], ["excludeTagIds", initial.excludeTagIds.join(",")], ["years", initial.years.replace(/[，\s]+/g, ",")], ["minRating", initial.minRating], ["isFavorite", initial.favoriteFilter], ["isWatched", initial.watchedFilter], ["hasPlayableSource", initial.playableFilter]];
    filters.forEach(([key, value]) => { if (value) params.set(key, value); });
    apiFetch<{ items: CatalogItem[] }>(`/api/v1/items?${params.toString()}`).then((result) => { setItems(result.items); setResultCount(result.items.length); }).catch((requestError) => setError(requestError instanceof Error ? requestError.message : "恢复搜索结果失败"));
  }, []);
  async function search(event: FormEvent) { event.preventDefault(); const trimmed = query.trim(); const hasCriteria = Boolean(trimmed || actorIds.length || directorIds.length || studioIds.length || seriesIds.length || tagIds.length || excludeTagIds.length || years.trim() || minRating || favoriteFilter || watchedFilter || playableFilter); const nextSubmittedQuery = hasCriteria ? (trimmed || "__filters__") : ""; const nextState: SearchState = { query: trimmed, submittedQuery: nextSubmittedQuery, actorIds, directorIds, studioIds, seriesIds, tagIds, excludeTagIds, years, minRating, favoriteFilter, watchedFilter, playableFilter }; setSubmittedQuery(nextSubmittedQuery); navigate(`/search${buildSearchParams(nextState).toString() ? `?${buildSearchParams(nextState).toString()}` : ""}`, { replace: true }); if (trimmed) { const nextHistory = [trimmed, ...history.filter((value) => value !== trimmed)].slice(0, 8); setHistory(nextHistory); sessionStorage.setItem("jmlab.search-history", JSON.stringify(nextHistory)); } setShowHints(false); setBusy(true); setError(""); try { const params = new URLSearchParams({ q: trimmed, limit: "48" }); const filters: Array<[string, string]> = [["actorIds", actorIds.join(",")], ["directorIds", directorIds.join(",")], ["studioIds", studioIds.join(",")], ["seriesIds", seriesIds.join(",")], ["tagIds", tagIds.join(",")], ["excludeTagIds", excludeTagIds.join(",")], ["years", years.replace(/[，\s]+/g, ",")], ["minRating", minRating], ["isFavorite", favoriteFilter], ["isWatched", watchedFilter], ["hasPlayableSource", playableFilter]]; filters.forEach(([key, value]) => { if (value) params.set(key, value); }); const result = await apiFetch<{ items: CatalogItem[] }>(`/api/v1/items?${params.toString()}`); setItems(result.items); setResultCount(result.items.length); } catch (requestError) { setError(requestError instanceof Error ? requestError.message : "搜索失败"); } finally { setBusy(false); } }
  function resetSearch() { setQuery(""); setSubmittedQuery(""); setActorIds([]); setDirectorIds([]); setStudioIds([]); setSeriesIds([]); setTagIds([]); setExcludeTagIds([]); setYears(""); setMinRating(""); setFavoriteFilter(""); setWatchedFilter(""); setPlayableFilter(""); setItems([]); setResultCount(null); setHints([]); setError(""); setShareStatus(""); navigate("/search", { replace: true }); }
  async function shareSearch() { const params = buildSearchParams({ query, submittedQuery, actorIds, directorIds, studioIds, seriesIds, tagIds, excludeTagIds, years, minRating, favoriteFilter, watchedFilter, playableFilter }); const url = `${window.location.origin}/search${params.toString() ? `?${params.toString()}` : ""}`; try { if (navigator.clipboard) { await navigator.clipboard.writeText(url); setShareStatus("筛选链接已复制"); } else { setShareStatus("请复制浏览器地址栏中的链接"); } } catch { setShareStatus("请复制浏览器地址栏中的链接"); } }
  function chooseHint(hint: SearchHint) { setQuery(hint.code || hint.label); setShowHints(false); }
  const relationNames = (kind: RelationKind, ids: string[]) => ids.map((id) => options[kind].find((entity) => entity.id === id)?.name || id);
  const activeFilters: Array<{ key: string; label: string; remove: () => void }> = [];
  if (query.trim()) activeFilters.push({ key: "query", label: `关键词：${query.trim()}`, remove: () => { setQuery(""); setSubmittedQuery(""); setItems([]); setResultCount(null); } });
  relationNames("people", actorIds).forEach((label, index) => activeFilters.push({ key: `actor-${actorIds[index]}`, label: `演员：${label}`, remove: () => setActorIds(actorIds.filter((_, itemIndex) => itemIndex !== index)) }));
  relationNames("people", directorIds).forEach((label, index) => activeFilters.push({ key: `director-${directorIds[index]}`, label: `导演：${label}`, remove: () => setDirectorIds(directorIds.filter((_, itemIndex) => itemIndex !== index)) }));
  relationNames("studios", studioIds).forEach((label, index) => activeFilters.push({ key: `studio-${studioIds[index]}`, label: `片商：${label}`, remove: () => setStudioIds(studioIds.filter((_, itemIndex) => itemIndex !== index)) }));
  relationNames("series", seriesIds).forEach((label, index) => activeFilters.push({ key: `series-${seriesIds[index]}`, label: `系列：${label}`, remove: () => setSeriesIds(seriesIds.filter((_, itemIndex) => itemIndex !== index)) }));
  relationNames("tags", tagIds).forEach((label, index) => activeFilters.push({ key: `tag-${tagIds[index]}`, label: `标签：${label}`, remove: () => setTagIds(tagIds.filter((_, itemIndex) => itemIndex !== index)) }));
  relationNames("tags", excludeTagIds).forEach((label, index) => activeFilters.push({ key: `exclude-tag-${excludeTagIds[index]}`, label: `排除标签：${label}`, remove: () => setExcludeTagIds(excludeTagIds.filter((_, itemIndex) => itemIndex !== index)) }));
  if (years.trim()) activeFilters.push({ key: "years", label: `年份：${years.trim()}`, remove: () => setYears("") });
  if (minRating) activeFilters.push({ key: "rating", label: `评分 ≥ ${minRating}`, remove: () => setMinRating("") });
  if (favoriteFilter) activeFilters.push({ key: "favorite", label: favoriteFilter === "true" ? "已收藏" : "未收藏", remove: () => setFavoriteFilter("") });
  if (watchedFilter) activeFilters.push({ key: "watched", label: watchedFilter === "true" ? "已看" : "未看", remove: () => setWatchedFilter("") });
  if (playableFilter) activeFilters.push({ key: "playable", label: playableFilter === "true" ? "有可播放源" : "无可播放源", remove: () => setPlayableFilter("") });
  const relationSelect = (label: string, kind: RelationKind, value: string[], setter: (next: string[]) => void) => <label className="filter-field">{label}<select multiple size={4} value={value} onChange={(event) => setter(Array.from(event.target.selectedOptions, (option) => option.value))}>{options[kind].map((entity) => <option value={entity.id} key={entity.id}>{entity.name}</option>)}</select></label>;
  const groupedHints = (Object.keys(searchHintLabels) as SearchHint["kind"][]).map((kind) => ({ kind, items: hints.filter((hint) => hint.kind === kind) })).filter((group) => group.items.length > 0);
  return <section className="content-card search-page"><div className="search-heading"><div><p className="eyebrow">目录搜索</p><h1>搜索作品、演员和片商</h1><p className="muted">支持内容标识、标题、关系实体和组合筛选；结果只来自当前账号可见媒体库。</p></div><Link to="/rankings" className="ghost-button">打开排行榜</Link></div><form className="search-row search-form" onSubmit={search}><div className="search-input-wrap"><input aria-label="搜索关键词" placeholder="输入内容标识或标题" value={query} onChange={(event) => { setQuery(event.target.value); setShowHints(true); }} onFocus={() => setShowHints(true)} onBlur={() => window.setTimeout(() => setShowHints(false), 150)} />{showHints && (groupedHints.length > 0 || (!query.trim() && history.length > 0)) && <div className="search-suggest" role="listbox">{groupedHints.map((group) => <div className="search-suggest-group" key={group.kind}><span className="search-suggest-label">{searchHintLabels[group.kind]}</span>{group.items.map((hint) => <button type="button" role="option" className="search-suggest-item" key={`${hint.kind}-${hint.id}`} onMouseDown={(event) => event.preventDefault()} onClick={() => chooseHint(hint)}><span>{hint.code || hint.label}</span><small>{hint.title || hint.subtitle || hint.label}</small></button>)}</div>)}{!query.trim() && history.length > 0 && <div className="search-suggest-group"><span className="search-suggest-label">搜索历史</span>{history.map((value) => <button type="button" className="search-suggest-item" key={value} onMouseDown={(event) => event.preventDefault()} onClick={() => { setQuery(value); setShowHints(false); }}><span>{value}</span><small>再次搜索</small></button>)}<button type="button" className="search-history-clear" onMouseDown={(event) => event.preventDefault()} onClick={() => { setHistory([]); sessionStorage.removeItem("jmlab.search-history"); }}>清除历史</button></div>}</div>}</div><button className="primary-button" disabled={busy}>{busy ? "搜索中…" : "搜索"}</button></form><div className="filter-panel"><div className="filter-panel-heading"><div><h2>筛选条件</h2><p className="muted">可以组合多个关系、年份、评分和个人状态。</p></div><div className="filter-panel-actions"><button type="button" className="ghost-button compact-button" onClick={shareSearch}>分享筛选</button><button type="button" className="ghost-button compact-button" onClick={resetSearch} disabled={busy}>重置条件</button></div></div><div className="filter-grid common-filters"><label className="filter-field">年份（逗号分隔）<input placeholder="2024,2025" value={years} onChange={(event) => setYears(event.target.value)} /></label><label className="filter-field">最低评分<input type="number" min="0" max="10" step="0.1" placeholder="0" value={minRating} onChange={(event) => setMinRating(event.target.value)} /></label><label className="filter-field">收藏状态<select value={favoriteFilter} onChange={(event) => setFavoriteFilter(event.target.value)}><option value="">不限</option><option value="true">已收藏</option><option value="false">未收藏</option></select></label><label className="filter-field">观看状态<select value={watchedFilter} onChange={(event) => setWatchedFilter(event.target.value)}><option value="">不限</option><option value="true">已看</option><option value="false">未看</option></select></label><label className="filter-field">播放源<select value={playableFilter} onChange={(event) => setPlayableFilter(event.target.value)}><option value="">不限</option><option value="true">有可播放源</option><option value="false">无可播放源</option></select></label></div><details className="advanced-search"><summary>人物、片商与标签{actorIds.length + directorIds.length + studioIds.length + seriesIds.length + tagIds.length + excludeTagIds.length > 0 && <span className="filter-count">已选择 {actorIds.length + directorIds.length + studioIds.length + seriesIds.length + tagIds.length + excludeTagIds.length} 项</span>}</summary><p className="muted">展开后可组合选择；电脑多选请按住 Ctrl 或 ⌘。</p><div className="filter-grid">{relationSelect("演员（可多选）", "people", actorIds, setActorIds)}{relationSelect("导演（可多选）", "people", directorIds, setDirectorIds)}{relationSelect("片商（可多选）", "studios", studioIds, setStudioIds)}{relationSelect("系列（可多选）", "series", seriesIds, setSeriesIds)}{relationSelect("包含标签", "tags", tagIds, setTagIds)}{relationSelect("排除标签", "tags", excludeTagIds, setExcludeTagIds)}</div></details><div className="filter-apply-row"><span className="muted">选好条件后，点击应用筛选。</span><button type="button" className="primary-button" disabled={busy} onClick={(event) => void search(event)}>{busy ? "搜索中…" : "应用筛选"}</button></div></div>{activeFilters.length > 0 && <div className="active-filters"><span className="active-filters-label">待搜索条件</span>{activeFilters.map((filter) => <button type="button" className="filter-chip" key={filter.key} onClick={filter.remove}>{filter.label}<span aria-hidden="true">×</span></button>)}<span className="condition-summary">{activeFilters.map((filter) => filter.label).join(" · ")}</span></div>}{shareStatus && <p className="form-success" role="status">{shareStatus}</p>}{error && <p className="form-error" role="alert">{error}</p>}{resultCount !== null && <p className="search-result-summary">找到 {resultCount} 部作品 · 修改条件后请重新搜索</p>}{items.length > 0 ? <ItemGrid items={items} /> : resultCount !== null ? <div className="empty-card search-empty"><h3>没有找到匹配作品</h3><p className="muted">可以换一个内容标识或移除部分筛选条件，再从当前授权媒体库搜索。</p><button type="button" className="ghost-button" onClick={resetSearch}>清空条件</button></div> : <p className="muted">输入关键词或选择筛选条件后，从当前授权媒体库搜索。</p>}</section>;
}

function ItemPoster({ item, className = "", alt }: { item: { id: string; code: string; title: string; itemType?: string }; className?: string; alt?: string }) {
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

function ItemCard({ item }: { item: CatalogItem }) {
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

function ItemGrid({ items, compact = false }: { items: CatalogItem[]; compact?: boolean }) {
  return (
    <div className={`item-grid${compact ? " item-grid-compact" : ""}`}>
      {items.map((item) => (
        <ItemCard item={item} key={item.id} />
      ))}
    </div>
  );
}
function RelationBlock({ kind, label, values }: { kind: RelationKind; label: string; values?: Relation[] }) { if (!values || values.length === 0) return null; return <div className="relation-block"><span className="eyebrow">{label}</span><div className="relation-list">{values.map((value) => <Link className="relation-chip" to={`/${kind}/${value.id}`} key={`${value.id}-${value.role ?? ""}`}>{value.name}{value.role === "director" ? " · 导演" : value.role === "writer" ? " · 编剧" : ""}</Link>)}</div></div>; }

function UserAdminPage() {
  const session = readSession();
  const [users, setUsers] = useState<User[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");
  const [busyUserId, setBusyUserId] = useState<string | null>(null);

  // New user form state
  const [showCreateForm, setShowCreateForm] = useState(false);
  const [username, setUsername] = useState("");
  const [displayName, setDisplayName] = useState("");
  const [password, setPassword] = useState("");
  const [role, setRole] = useState<"Viewer" | "Editor" | "Admin">("Viewer");
  const [creating, setCreating] = useState(false);
  const [createError, setCreateError] = useState("");

  // Reset password modal state
  const [resetUser, setResetUser] = useState<User | null>(null);
  const [newPassword, setNewPassword] = useState("");
  const [resetting, setResetting] = useState(false);
  const [resetError, setResetError] = useState("");

  // Delete user modal state
  const [deleteCandidate, setDeleteCandidate] = useState<User | null>(null);
  const [deleting, setDeleting] = useState(false);
  const [deleteError, setDeleteError] = useState("");

  const loadUsers = useCallback(() => {
    setLoading(true);
    apiFetch<{ items: User[] }>("/api/v1/admin/users")
      .then((result) => {
        setUsers(result.items);
        setError("");
      })
      .catch((requestError) => setError(requestError instanceof Error ? requestError.message : "读取用户失败"))
      .finally(() => setLoading(false));
  }, []);

  useEffect(() => {
    loadUsers();
  }, [loadUsers]);

  async function handleCreateUser(e: FormEvent) {
    e.preventDefault();
    setCreateError("");
    if (!username.trim()) {
      setCreateError("用户名不能为空");
      return;
    }
    if (password.length < 6) {
      setCreateError("密码长度至少需要 6 个字符");
      return;
    }
    setCreating(true);
    try {
      await apiFetch("/api/v1/admin/users", {
        method: "POST",
        body: JSON.stringify({
          username: username.trim(),
          displayName: displayName.trim() || username.trim(),
          password,
          role,
        }),
      });
      setMessage(`用户 ${username} 创建成功`);
      setUsername("");
      setDisplayName("");
      setPassword("");
      setRole("Viewer");
      setShowCreateForm(false);
      loadUsers();
    } catch (requestError) {
      setCreateError(requestError instanceof Error ? requestError.message : "创建用户失败");
    } finally {
      setCreating(false);
    }
  }

  async function toggleStatus(u: User) {
    setBusyUserId(u.id);
    setMessage("");
    setError("");
    const nextStatus = u.status === "active" ? "disabled" : "active";
    try {
      await apiFetch(`/api/v1/admin/users/${encodeURIComponent(u.id)}`, {
        method: "PATCH",
        body: JSON.stringify({ status: nextStatus }),
      });
      setMessage(`用户 ${u.username} 已${nextStatus === "active" ? "启用" : "禁用"}`);
      loadUsers();
    } catch (requestError) {
      setError(requestError instanceof Error ? requestError.message : "更新状态失败");
    } finally {
      setBusyUserId(null);
    }
  }

  async function changeRole(u: User, nextRole: string) {
    setBusyUserId(u.id);
    setMessage("");
    setError("");
    try {
      await apiFetch(`/api/v1/admin/users/${encodeURIComponent(u.id)}`, {
        method: "PATCH",
        body: JSON.stringify({ role: nextRole }),
      });
      setMessage(`用户 ${u.username} 角色已修改为 ${nextRole}`);
      loadUsers();
    } catch (requestError) {
      setError(requestError instanceof Error ? requestError.message : "更新角色失败");
    } finally {
      setBusyUserId(null);
    }
  }

  async function handleResetPassword(e: FormEvent) {
    e.preventDefault();
    if (!resetUser) return;
    setResetError("");
    if (newPassword.length < 6) {
      setResetError("新密码至少需要 6 个字符");
      return;
    }
    setResetting(true);
    try {
      await apiFetch(`/api/v1/admin/users/${encodeURIComponent(resetUser.id)}/reset-password`, {
        method: "POST",
        body: JSON.stringify({ password: newPassword }),
      });
      setMessage(`用户 ${resetUser.username} 的密码重置成功`);
      setResetUser(null);
      setNewPassword("");
    } catch (requestError) {
      setResetError(requestError instanceof Error ? requestError.message : "重置密码失败");
    } finally {
      setResetting(false);
    }
  }

  async function revokeSessions(u: User) {
    setBusyUserId(u.id);
    setMessage("");
    setError("");
    try {
      await apiFetch(`/api/v1/admin/users/${encodeURIComponent(u.id)}/revoke-sessions`, {
        method: "POST",
      });
      setMessage(`用户 ${u.username} 的所有会话已撤销`);
    } catch (requestError) {
      setError(requestError instanceof Error ? requestError.message : "撤销会话失败");
    } finally {
      setBusyUserId(null);
    }
  }

  async function handleDeleteUser() {
    if (!deleteCandidate) return;
    setDeleting(true);
    setDeleteError("");
    try {
      await apiFetch(`/api/v1/admin/users/${encodeURIComponent(deleteCandidate.id)}`, {
        method: "DELETE",
      });
      setMessage(`用户 ${deleteCandidate.username} 已成功删除`);
      setDeleteCandidate(null);
      loadUsers();
    } catch (requestError) {
      setDeleteError(requestError instanceof Error ? requestError.message : "删除用户失败");
    } finally {
      setDeleting(false);
    }
  }

  return (
    <section className="content-card">
      <div className="user-admin-header">
        <div>
          <p className="eyebrow">用户与权限</p>
          <h1>用户管理</h1>
          <p className="muted">管理本地账号、角色授权与会话状态。</p>
        </div>
        <div style={{ display: "flex", gap: "0.6rem" }}>
          <button
            type="button"
            className="primary-button"
            onClick={() => { setShowCreateForm(!showCreateForm); setCreateError(""); }}
          >
            {showCreateForm ? "收起表单" : "＋ 新建用户"}
          </button>
          <Link to="/settings" className="ghost-button">返回设置</Link>
        </div>
      </div>

      {message && <p className="form-success" role="status">{message}</p>}
      {error && <p className="form-error" role="alert">{error}</p>}

      {showCreateForm && (
        <div className="user-create-card">
          <h2>新建用户</h2>
          <p className="muted">创建新用户后可在权限和媒体库管理中分配访问范围。</p>
          {createError && <p className="form-error" role="alert">{createError}</p>}
          <form onSubmit={handleCreateUser}>
            <div className="user-create-grid">
              <label>
                用户名（必填）
                <input
                  required
                  placeholder="例如: viewer01"
                  value={username}
                  onChange={(e) => setUsername(e.target.value)}
                />
              </label>
              <label>
                显示名称
                <input
                  placeholder="留空默认同用户名"
                  value={displayName}
                  onChange={(e) => setDisplayName(e.target.value)}
                />
              </label>
              <label>
                初始密码（至少 6 位）
                <input
                  type="password"
                  required
                  placeholder="最少 6 个字符"
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                />
              </label>
              <label>
                角色
                <select
                  value={role}
                  onChange={(e) => setRole(e.target.value as "Viewer" | "Editor" | "Admin")}
                >
                  <option value="Viewer">Viewer (访客)</option>
                  <option value="Editor">Editor (编辑者)</option>
                  <option value="Admin">Admin (管理员)</option>
                </select>
              </label>
              <div style={{ display: "flex", gap: "0.6rem" }}>
                <button className="primary-button" disabled={creating} type="submit">
                  {creating ? "创建中…" : "确认创建"}
                </button>
                <button
                  type="button"
                  className="ghost-button"
                  onClick={() => setShowCreateForm(false)}
                >
                  取消
                </button>
              </div>
            </div>
          </form>
        </div>
      )}

      {loading ? (
        <p className="muted">正在加载用户列表…</p>
      ) : (
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>用户名</th>
                <th>显示名称</th>
                <th>角色</th>
                <th>状态</th>
                <th>操作</th>
              </tr>
            </thead>
            <tbody>
              {users.map((user) => {
                const currentRole = (user.roles && user.roles[0]) || (user.isAdmin ? "Admin" : "Viewer");
                const isBusy = busyUserId === user.id;

                return (
                  <tr key={user.id}>
                    <td><strong>{user.username}</strong></td>
                    <td>{user.displayName}</td>
                    <td>
                      <select
                        value={currentRole}
                        disabled={isBusy}
                        style={{ padding: "0.3rem 0.5rem", borderRadius: "6px", background: "var(--input)", color: "var(--text)", border: "1px solid var(--line)" }}
                        onChange={(e) => void changeRole(user, e.target.value)}
                      >
                        <option value="Viewer">Viewer</option>
                        <option value="Editor">Editor</option>
                        <option value="Admin">Admin</option>
                      </select>
                    </td>
                    <td>
                      <span className={user.status === "active" ? "status-active" : "status-disabled"}>
                        {user.status === "active" ? "启用" : "已禁用"}
                      </span>
                    </td>
                    <td>
                      <div className="user-action-buttons">
                        <button
                          type="button"
                          className="ghost-button compact-button"
                          disabled={isBusy}
                          onClick={() => void toggleStatus(user)}
                        >
                          {user.status === "active" ? "禁用" : "启用"}
                        </button>
                        <button
                          type="button"
                          className="ghost-button compact-button"
                          disabled={isBusy}
                          onClick={() => { setResetUser(user); setNewPassword(""); setResetError(""); }}
                        >
                          重置密码
                        </button>
                        <button
                          type="button"
                          className="ghost-button compact-button"
                          disabled={isBusy}
                          onClick={() => void revokeSessions(user)}
                        >
                          下线会话
                        </button>
                        <button
                          type="button"
                          className="ghost-button compact-button"
                          style={{ color: "#ff6b6b", borderColor: "rgba(255, 107, 107, 0.4)" }}
                          disabled={isBusy || user.id === session?.user.id}
                          onClick={() => { setDeleteCandidate(user); setDeleteError(""); }}
                          title={user.id === session?.user.id ? "不能删除当前登录账号" : "删除用户"}
                        >
                          删除
                        </button>
                      </div>
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}

      {resetUser && (
        <div className="user-modal-overlay" onClick={() => setResetUser(null)}>
          <div className="user-modal" onClick={(e) => e.stopPropagation()}>
            <h2>重置密码</h2>
            <p className="muted">正在为用户 <strong>{resetUser.username}</strong> 设置新密码，密码至少需要 12 个字符。</p>
            {resetError && <p className="form-error" role="alert">{resetError}</p>}
            <form onSubmit={handleResetPassword}>
              <label>
                新密码
                <input
                  type="password"
                  required
                  placeholder="至少 12 个字符"
                  value={newPassword}
                  onChange={(e) => setNewPassword(e.target.value)}
                  autoFocus
                />
              </label>
              <div className="user-modal-actions">
                <button type="button" className="ghost-button" onClick={() => setResetUser(null)}>
                  取消
                </button>
                <button type="submit" className="primary-button" disabled={resetting}>
                  {resetting ? "保存中…" : "确认重置"}
                </button>
              </div>
            </form>
          </div>
        </div>
      )}

      {deleteCandidate && (
        <div className="user-modal-overlay" onClick={() => setDeleteCandidate(null)}>
          <div className="user-modal" onClick={(e) => e.stopPropagation()}>
            <h2 style={{ color: "#ff6b6b" }}>确认删除用户</h2>
            <p className="muted">
              确定要删除用户 <strong>{deleteCandidate.username}</strong> ({deleteCandidate.displayName}) 吗？
            </p>
            <p style={{ fontSize: "0.88rem", color: "var(--muted)", margin: "0.5rem 0 1rem" }}>
              此操作将清除其所有播放记录、收藏、自定义偏好以及媒体库访问授权，且无法恢复。
            </p>
            {deleteError && <p className="form-error" role="alert">{deleteError}</p>}
            <div className="user-modal-actions">
              <button
                type="button"
                className="ghost-button"
                disabled={deleting}
                onClick={() => setDeleteCandidate(null)}
              >
                取消
              </button>
              <button
                type="button"
                className="primary-button"
                style={{ backgroundColor: "#ff5252", borderColor: "transparent" }}
                disabled={deleting}
                onClick={() => void handleDeleteUser()}
              >
                {deleting ? "删除中…" : "确认删除"}
              </button>
            </div>
          </div>
        </div>
      )}
    </section>
  );
}

function NotFoundPage() { return <section className="content-card"><p className="eyebrow">404</p><h1>页面不存在</h1><Link to="/" className="ghost-button">回到首页</Link></section>; }

class ErrorBoundary extends Component<{ children: ReactNode }, { hasError: boolean; error: Error | null }> {
  constructor(props: { children: ReactNode }) {
    super(props);
    this.state = { hasError: false, error: null };
  }
  static getDerivedStateFromError(error: Error) {
    return { hasError: true, error };
  }
  componentDidCatch(error: Error, errorInfo: ErrorInfo) {
    console.error("ErrorBoundary caught error:", error, errorInfo);
  }
  render() {
    if (this.state.hasError) {
      return (
        <div style={{ padding: "60px 20px", maxWidth: 600, margin: "60px auto", textAlign: "center", color: "#f3f4f6" }}>
          <h2>页面发生意外错误</h2>
          <p style={{ color: "#9ca3af", margin: "16px 0" }}>{this.state.error?.message || "请尝试清除本地会话缓存并重新加载"}</p>
          <button
            type="button"
            className="primary-button"
            onClick={() => {
              sessionStorage.clear();
              localStorage.clear();
              window.location.reload();
            }}
          >
            清除本地会话并刷新
          </button>
        </div>
      );
    }
    return this.props.children;
  }
}

export default function App() {
  return (
    <ErrorBoundary>
      <Layout />
    </ErrorBoundary>
  );
}
