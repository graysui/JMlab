import { apiFetch, readSession } from "../lib/api";
import { FormEvent, useEffect, useState } from "react";
import { Link, useNavigate } from "react-router-dom";
import { ThemePreference, defaultThemePreference, defaultModuleFlags, PreferencesData, normalizeTheme, preferenceModuleKeys, preferenceModuleLabels } from "../lib/appShared";

export function ProfileSettingsPage() {
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
  return <form className="content-card settings-page" onSubmit={savePreferences}><div className="settings-heading"><div><p className="eyebrow">个人偏好</p><h1>主题与首页</h1><p className="muted">偏好只影响当前账号；媒体库权限仍由管理员控制。</p></div><Link to="/browse" className="ghost-button">返回浏览</Link></div>{loading ? <div className="settings-loading"><div className="skeleton-line" /><div className="skeleton-line short" /><div className="skeleton-grid"><div className="skeleton-card" /><div className="skeleton-card" /></div></div> : <><section className="settings-section"><div><p className="eyebrow">外观</p><h2>选择显示风格</h2></div><div className="settings-options"><label className={`settings-option ${theme.mode === "dark" ? "selected" : ""}`}><input type="radio" name="theme-mode" checked={theme.mode === "dark"} onChange={() => applyTheme({ ...theme, mode: "dark" })} /><span><strong>深色</strong><small>适合夜间浏览</small></span></label><label className={`settings-option ${theme.mode === "light" ? "selected" : ""}`}><input type="radio" name="theme-mode" checked={theme.mode === "light"} onChange={() => applyTheme({ ...theme, mode: "light" })} /><span><strong>浅色</strong><small>更明亮的阅读界面</small></span></label></div><div className="accent-options"><span className="settings-label">强调色</span>{(["blue", "mint", "amber"] as const).map((accent) => <button type="button" className={`accent-option accent-${accent} ${theme.accent === accent ? "selected" : ""}`} aria-label={`选择${accent === "blue" ? "蓝色" : accent === "mint" ? "薄荷色" : "琥珀色"}强调色`} key={accent} onClick={() => applyTheme({ ...theme, accent })}><span /></button>)}</div></section><section className="settings-section"><div><p className="eyebrow">布局</p><h2>调整卡片密度</h2><p className="muted">这些设置会即时作用于首页和媒体库网格。</p></div><div className="layout-settings"><label><span className="settings-label">每行卡片数</span><select value={theme.columns} onChange={(event) => applyTheme({ ...theme, columns: event.target.value as ThemePreference["columns"] })}><option value="auto">自动</option><option value="2">2 列</option><option value="3">3 列</option><option value="4">4 列</option><option value="5">5 列</option></select></label><label><span className="settings-label">海报比例</span><select value={theme.posterRatio} onChange={(event) => applyTheme({ ...theme, posterRatio: event.target.value as ThemePreference["posterRatio"] })}><option value="portrait">竖向 2:3</option><option value="square">方形 1:1</option><option value="wide">横向 16:9</option></select></label></div><details className="appearance-advanced"><summary>更多视觉效果</summary><p className="muted">关闭动效可减少干扰，设备开启“减少动态效果”时也会自动简化动画。</p><div className="module-settings layout-toggles"><label className="module-setting"><span><strong>背景模糊</strong><small>为顶部栏和浮层保留玻璃质感</small></span><input type="checkbox" checked={theme.backgroundBlur} onChange={(event) => applyTheme({ ...theme, backgroundBlur: event.target.checked })} /></label><label className="module-setting"><span><strong>液态背景</strong><small>显示缓慢移动的渐变光斑</small></span><input type="checkbox" checked={theme.liquid} onChange={(event) => applyTheme({ ...theme, liquid: event.target.checked })} /></label><label className="module-setting"><span><strong>3D 卡片</strong><small>悬停时显示轻微透视效果</small></span><input type="checkbox" checked={theme.card3d} onChange={(event) => applyTheme({ ...theme, card3d: event.target.checked })} /></label></div></details><div className="module-settings"><label className="module-setting"><span><strong>原标题</strong><small>在作品卡片中显示来源原标题</small></span><input type="checkbox" checked={theme.showOriginalTitle} onChange={(event) => applyTheme({ ...theme, showOriginalTitle: event.target.checked })} /></label></div></section><section className="settings-section"><div><p className="eyebrow">首页内容</p><h2>显示哪些内容</h2><p className="muted">只调整自己的首页推荐，不影响服务模块的启停。</p></div><div className="module-settings">{preferenceModuleKeys.map((key) => <label className="module-setting" key={key}><span><strong>{preferenceModuleLabels[key]}</strong><small>{key === "ranking" ? "排行榜" : "首页推荐"}</small></span><input type="checkbox" checked={moduleFlags[key] !== false} onChange={() => toggleModule(key)} /></label>)}</div></section><section className="settings-section"><h2>成人内容</h2><label className="module-setting"><span><strong>我已成年，允许显示成人内容</strong><small>仍需管理员授予相应媒体库权限；取消后不再显示成人媒体。</small></span><input type="checkbox" checked={adultConfirmed} onChange={(event) => setAdultConfirmed(event.target.checked)} /></label></section><section className="settings-section settings-locale"><p className="muted">界面语言：简体中文</p></section>{error && <p className="form-error" role="alert">{error}</p>}<div className="settings-actions"><button className="primary-button" disabled={busy}>{busy ? "保存中…" : "保存偏好"}</button>{message && <span className="form-success" role="status">{message}</span>}</div></>}</form>;
}

