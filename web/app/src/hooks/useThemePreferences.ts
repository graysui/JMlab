import { useEffect } from "react";
import { apiFetch, type SessionData } from "../lib/api";
import { applyThemeToDocument, defaultThemePreference, normalizeTheme, type PreferencesData, type ThemePreference } from "../lib/appShared";

declare global {
  interface Window {
    jmlabTheme?: ThemePreference;
  }
}

export function useThemePreferences(session: SessionData | null) {
  useEffect(() => {
    const syncTheme = () => applyThemeToDocument(window.jmlabTheme ?? defaultThemePreference);
    window.addEventListener("jmlab-preferences-change", syncTheme);
    if (!session) {
      window.jmlabTheme = defaultThemePreference;
      applyThemeToDocument(defaultThemePreference);
      return () => window.removeEventListener("jmlab-preferences-change", syncTheme);
    }
    let active = true;
    void apiFetch<PreferencesData>("/api/v1/me/preferences").then((preferences) => {
      if (!active) return;
      const theme = normalizeTheme(preferences.theme);
      window.jmlabTheme = theme;
      applyThemeToDocument(theme);
    }).catch(() => {
      if (active) applyThemeToDocument(defaultThemePreference);
    });
    return () => {
      active = false;
      window.removeEventListener("jmlab-preferences-change", syncTheme);
    };
  }, [session?.accessToken]);
}
