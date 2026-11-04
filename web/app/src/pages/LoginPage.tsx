import { apiFetch, saveSession, type SessionData } from "../lib/api";
import { FormEvent, useState } from "react";
import { useLocation, useNavigate, useSearchParams } from "react-router-dom";
import { defaultRouteFor, safeReturnTo } from "../app/routeRules";

export function LoginPage() {
  const navigate = useNavigate();
  const location = useLocation();
  const [searchParams] = useSearchParams();
  const [username, setUsername] = useState(""); const [password, setPassword] = useState(""); const [error, setError] = useState(""); const [busy, setBusy] = useState(false);
  async function submit(event: FormEvent) { event.preventDefault(); setBusy(true); setError(""); try { const session = await apiFetch<SessionData>("/api/v1/auth/login", { method: "POST", body: JSON.stringify({ username, password, clientName: "JMlab Web", deviceId: "web" }) }); saveSession(session); const stateReturnTo = (location.state as { returnTo?: unknown } | null)?.returnTo; const returnTo = typeof stateReturnTo === "string" ? stateReturnTo : searchParams.get("returnTo"); navigate(safeReturnTo(returnTo) ?? defaultRouteFor(session.user), { replace: true }); } catch (requestError) { setError(requestError instanceof Error ? requestError.message : "登录失败"); } finally { setBusy(false); } }
  return <form className="auth-card" onSubmit={submit}><p className="eyebrow">JMlab 账号</p><h1>登录媒体库</h1><label>用户名<input required autoComplete="username" value={username} onChange={(event) => setUsername(event.target.value)} /></label><label>密码<input required type="password" autoComplete="current-password" value={password} onChange={(event) => setPassword(event.target.value)} /></label>{error && <p className="form-error" role="alert">{error}</p>}<button className="primary-button" disabled={busy}>{busy ? "登录中…" : "登录"}</button><p className="muted">登录后，Web 与 Jellyfin 客户端会共享同一份用户状态。</p></form>;
}

