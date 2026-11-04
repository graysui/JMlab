import { type User, userHasRole } from "../lib/api";

const inAppRoutes = [
  /^\/$/,
  /^\/(?:browse|search|rankings|libraries|playlists|clients)$/,
  /^\/(?:people|studios|series|tags)(?:\/[^/]+)?$/,
  /^\/items\/[^/]+(?:\/play)?$/,
  /^\/manage(?:\/(?:discover|libraries|collections|relations|sources|tasks|logs|statistics|users|backup|modules(?:\/[^/]+)?))?$/,
  /^\/settings(?:\/profile)?$/,
];

export function defaultRouteFor(user: User): string {
  return userHasRole(user, "Admin", "Editor") ? "/manage" : "/browse";
}

export function safeReturnTo(candidate: string | null): string | null {
  if (!candidate || !candidate.startsWith("/") || candidate.startsWith("//") || candidate.startsWith("/\\") || candidate.includes("\\")) return null;

  try {
    const target = new URL(candidate, "http://jmlab.invalid");
    if (target.origin !== "http://jmlab.invalid" || !inAppRoutes.some((route) => route.test(target.pathname))) return null;
    if (target.pathname === "/login" || target.pathname.startsWith("/api/")) return null;
    return `${target.pathname}${target.search}${target.hash}`;
  } catch {
    return null;
  }
}

export function hasAnyRole(user: User, roles: readonly string[]): boolean {
  return roles.length === 0 || userHasRole(user, ...roles);
}
