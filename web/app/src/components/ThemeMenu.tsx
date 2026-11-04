import { useEffect, useRef, useState, type KeyboardEvent } from "react";
import { apiFetch, type SessionData } from "../lib/api";
import { applyThemeToDocument, getFocusableElements, normalizeTheme, type PreferencesData, type ThemePreference } from "../lib/appShared";
import { Icon } from "./Icon";

export function ThemeMenu({ session }: { session: SessionData | null }) {
  const [position, setPosition] = useState<{ x: number; y: number } | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const menuRef = useRef<HTMLDivElement>(null);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const restoreFocus = useRef(false);

  function open(x: number, y: number) {
    const width = 220;
    const height = 188;
    restoreFocus.current = false;
    setPosition({ x: Math.min(Math.max(8, x), Math.max(8, window.innerWidth - width - 8)), y: Math.min(Math.max(8, y), Math.max(8, window.innerHeight - height - 8)) });
    setError("");
  }

  function close(restore: boolean) {
    restoreFocus.current = restore;
    setPosition(null);
  }

  useEffect(() => {
    const closeMenu = (event: PointerEvent) => {
      if (menuRef.current?.contains(event.target as Node)) return;
      close(false);
    };
    const openContextMenu = (event: MouseEvent) => {
      if (!(event.target instanceof HTMLElement) || !event.target.closest(".app-shell")) return;
      if (event.target.isContentEditable || event.target.closest("input, textarea, select, [contenteditable='true'], button, a")) return;
      event.preventDefault();
      open(event.clientX, event.clientY);
    };
    window.addEventListener("pointerdown", closeMenu);
    window.addEventListener("contextmenu", openContextMenu);
    return () => {
      window.removeEventListener("pointerdown", closeMenu);
      window.removeEventListener("contextmenu", openContextMenu);
    };
  }, []);

  useEffect(() => {
    if (position) {
      window.requestAnimationFrame(() => menuRef.current?.querySelector<HTMLElement>("[role='menuitem']")?.focus());
      return;
    }
    if (restoreFocus.current) {
      triggerRef.current?.focus();
      restoreFocus.current = false;
    }
  }, [position]);

  async function saveQuickTheme(patch: Partial<ThemePreference>) {
    if (!session || busy) return;
    setBusy(true);
    setError("");
    try {
      const preferences = await apiFetch<PreferencesData>("/api/v1/me/preferences");
      const theme = normalizeTheme({ ...preferences.theme, ...patch });
      await apiFetch<PreferencesData>("/api/v1/me/preferences", { method: "PATCH", body: JSON.stringify({ theme, moduleFlags: preferences.moduleFlags, locale: preferences.locale }) });
      window.jmlabTheme = theme;
      applyThemeToDocument(theme);
      window.dispatchEvent(new Event("jmlab-preferences-change"));
      close(true);
    } catch (requestError) {
      setError(requestError instanceof Error ? requestError.message : "主题保存失败");
    } finally {
      setBusy(false);
    }
  }

  function onMenuKeyDown(event: KeyboardEvent<HTMLDivElement>) {
    const items = getFocusableElements(event.currentTarget).filter((element) => element.getAttribute("role") === "menuitem");
    const currentIndex = items.indexOf(document.activeElement as HTMLElement);
    if (event.key === "Escape") { event.preventDefault(); close(true); return; }
    if (!items.length || !["ArrowDown", "ArrowUp", "Home", "End"].includes(event.key)) return;
    event.preventDefault();
    const nextIndex = event.key === "Home" ? 0 : event.key === "End" ? items.length - 1 : (currentIndex + (event.key === "ArrowDown" ? 1 : -1) + items.length) % items.length;
    items[nextIndex]?.focus();
  }

  if (!session) return null;
  return <>
    <button ref={triggerRef} id="theme-menu-trigger" type="button" className="ghost-button theme-menu-trigger" aria-label="主题" aria-haspopup="menu" aria-controls="theme-context-menu" aria-expanded={Boolean(position)} onClick={(event) => { event.stopPropagation(); const rect = event.currentTarget.getBoundingClientRect(); open(rect.left, rect.bottom + 6); }}>
      <Icon name="theme" /><span>主题</span>
    </button>
    {position && <div ref={menuRef} id="theme-context-menu" className="theme-context-menu" role="menu" aria-labelledby="theme-menu-trigger" style={{ left: position.x, top: position.y }} onKeyDown={onMenuKeyDown} onContextMenu={(event) => { event.preventDefault(); event.stopPropagation(); }}>
      <strong>快速主题</strong>
      <div className="theme-menu-row"><button type="button" role="menuitem" disabled={busy} onClick={() => void saveQuickTheme({ mode: "dark" })}>深色</button><button type="button" role="menuitem" disabled={busy} onClick={() => void saveQuickTheme({ mode: "light" })}>浅色</button></div>
      <div className="theme-menu-row"><button type="button" role="menuitem" disabled={busy} onClick={() => void saveQuickTheme({ accent: "blue" })}>蓝色</button><button type="button" role="menuitem" disabled={busy} onClick={() => void saveQuickTheme({ accent: "mint" })}>薄荷</button><button type="button" role="menuitem" disabled={busy} onClick={() => void saveQuickTheme({ accent: "amber" })}>琥珀</button></div>
      {error && <span className="theme-menu-error" role="alert">{error}</span>}
    </div>}
  </>;
}
