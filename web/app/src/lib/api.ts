export type User = { id: string; username: string; displayName: string; status: string; isAdmin: boolean; roles?: string[] | null };
export type SessionData = { accessToken: string; csrfToken?: string; user: User };

export function readSession(): SessionData | null {
  const raw = sessionStorage.getItem("jmlab.session");
  if (!raw) return null;
  try {
    const parsed = JSON.parse(raw) as SessionData;
    if (parsed && parsed.user) {
      if (!Array.isArray(parsed.user.roles)) {
        parsed.user.roles = parsed.user.isAdmin ? ["Admin"] : [];
      }
    }
    return parsed;
  } catch {
    sessionStorage.removeItem("jmlab.session");
    return null;
  }
}

export function userHasRole(user?: User | null, ...targetRoles: string[]): boolean {
  if (!user) return false;
  if (user.isAdmin && targetRoles.includes("Admin")) return true;
  const roles = user.roles || (user.isAdmin ? ["Admin"] : []);
  return roles.some((r) => targetRoles.includes(r));
}
export function saveSession(session: SessionData | null) {
  if (session) sessionStorage.setItem("jmlab.session", JSON.stringify(session));
  else sessionStorage.removeItem("jmlab.session");
  window.dispatchEvent(new Event("jmlab-session-change"));
}

export async function apiFetch<T>(path: string, init: RequestInit = {}): Promise<T> {
  const session = readSession();
  const headers = new Headers(init.headers);
  headers.set("Accept", "application/json");
  if (init.body && !headers.has("Content-Type")) headers.set("Content-Type", "application/json");
  if (session?.accessToken) headers.set("Authorization", `Bearer ${session.accessToken}`);
  if (session?.csrfToken && init.method && init.method !== "GET") headers.set("X-CSRF-Token", session.csrfToken);
  const response = await fetch(path, { ...init, headers, credentials: "include" });
  const payload = await response.json().catch(() => ({}));
  if (!response.ok) throw new Error(payload?.error?.message ?? "请求失败");
  return payload as T;
}
