import { type ReactNode } from "react";
import { Link, Navigate, useLocation } from "react-router-dom";
import { readSession } from "../lib/api";
import { defaultRouteFor, hasAnyRole } from "./routeRules";

export function RouteAccess({ roles = [], children }: { roles?: readonly string[]; children: ReactNode }) {
  const session = readSession();
  const location = useLocation();

  if (!session) {
    return <Navigate to="/login" state={{ returnTo: `${location.pathname}${location.search}${location.hash}` }} replace />;
  }

  if (!hasAnyRole(session.user, roles)) {
    return <section className="content-card access-denied" role="status">
      <p className="eyebrow">账号权限</p>
      <h1>没有管理权限</h1>
      <p className="muted">当前账号不能使用这项功能，请联系管理员授权。</p>
      <Link to={defaultRouteFor(session.user)} className="ghost-button">返回可用工作区</Link>
    </section>;
  }

  return children;
}
