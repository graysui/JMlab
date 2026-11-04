import { useEffect, useState } from "react";
import { Link, NavLink } from "react-router-dom";
import brandMark from "../assets/jmlab-mark.svg";
import { Icon } from "../components/Icon";
import { ThemeMenu } from "../components/ThemeMenu";
import { AppRoutes } from "../app/AppRoutes";
import { apiFetch, readSession, saveSession, userHasRole, type SessionData } from "../lib/api";
import { useThemePreferences } from "../hooks/useThemePreferences";

export function Layout() {
  const [session, setSession] = useState<SessionData | null>(() => readSession());
  const [logoutError, setLogoutError] = useState("");
  useThemePreferences(session);

  useEffect(() => {
    const sync = () => setSession(readSession());
    window.addEventListener("jmlab-session-change", sync);
    return () => window.removeEventListener("jmlab-session-change", sync);
  }, []);

  async function logout() {
    setLogoutError("");
    try {
      await apiFetch("/api/v1/auth/logout", { method: "POST" });
      saveSession(null);
    } catch (error) {
      setLogoutError(error instanceof Error ? error.message : "退出失败，请重试");
    }
  }

  return <div className="app-shell">
    <a className="skip-link" href="#app-content">跳到主要内容</a>
    <header className="topbar">
      <Link to="/" className="brand" aria-label="JMlab 工作区入口">
        <img className="brand-mark" src={brandMark} width={32} height={32} alt="" aria-hidden="true" />
        <span className="brand-name">JMlab</span>
      </Link>
      {session && <nav className="workspace-switch" aria-label="工作区切换">
        {userHasRole(session.user, "Admin", "Editor") && <NavLink to="/manage" end>管理</NavLink>}
        <NavLink to="/browse">浏览</NavLink>
      </nav>}
      <div className="topbar-actions">
        {session ? <>
          <span className="topbar-user">{session.user.displayName || session.user.username}</span>
          <ThemeMenu session={session} />
          <Link to="/settings/profile" className="ghost-button profile-mobile-entry" aria-label="个人偏好"><Icon name="settings" /><span>个人偏好</span></Link>
          <button className="ghost-button" type="button" aria-label="退出" onClick={() => void logout()}><Icon name="logout" /><span>退出</span></button>
        </> : <Link to="/login" className="primary-button">登录</Link>}
      </div>
    </header>
    {logoutError && <p className="form-error app-notice" role="alert">{logoutError}</p>}
    <div id="app-content" className="app-content" tabIndex={-1}><AppRoutes /></div>
  </div>;
}
