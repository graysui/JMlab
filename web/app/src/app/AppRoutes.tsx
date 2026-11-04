import { Navigate, Route, Routes, useLocation } from "react-router-dom";
import { useState } from "react";
import { readSession } from "../lib/api";
import { BackupAdminPage } from "../pages/BackupAdminPage";
import { DiscoverPage } from "../pages/DiscoverPage";
import { LibraryPage } from "../pages/browse/LibraryPage";
import { HomePage } from "../pages/browse/HomePage";
import { ItemPage } from "../pages/browse/ItemPage";
import { PlayPage } from "../pages/browse/PlayPage";
import { RankingPage } from "../pages/browse/RankingPage";
import { RelationDetailPage, RelationListPage } from "../pages/browse/Relations";
import { SearchPage } from "../pages/browse/SearchPage";
import { ClientConnectionPage } from "../pages/ClientConnectionPage";
import { LoginPage } from "../pages/LoginPage";
import { NotFoundPage } from "../pages/NotFoundPage";
import { ProfileSettingsPage } from "../pages/ProfileSettingsPage";
import { PlaylistsPage } from "../pages/PlaylistsPage";
import { SourceAdminPage } from "../pages/SourceAdminPage";
import { ModuleCenterPage } from "../pages/ModuleCenterPage";
import { CollectionAdminPage } from "../pages/manage/CollectionAdminPage";
import { OverviewPage } from "../pages/manage/OverviewPage";
import { LibraryAdminPage } from "../pages/manage/LibraryAdminPage";
import { RelationAdminPage } from "../pages/manage/RelationAdminPage";
import { StatisticsAdminPage } from "../pages/manage/StatisticsAdminPage";
import { TaskAdminPage } from "../pages/manage/TaskAdminPage";
import { AuditLogsPage } from "../pages/manage/AuditLogsPage";
import { UserAdminPage } from "../pages/manage/UserAdminPage";
import { ManagementLayout } from "../layouts/ManagementLayout";
import { BrowseLayout } from "../layouts/BrowseLayout";
import { RouteAccess } from "./RouteAccess";
import { defaultRouteFor } from "./routeRules";

const contentManagers = ["Admin", "Editor"];

function EntryRoute() {
  const session = readSession();
  return <Navigate to={session ? defaultRouteFor(session.user) : "/login"} replace />;
}

function LoginRoute() {
  const [initialTarget] = useState(() => {
    const session = readSession();
    return session ? defaultRouteFor(session.user) : null;
  });
  return initialTarget ? <Navigate to={initialTarget} replace /> : <main className="page-shell login-shell"><LoginPage /></main>;
}

function LegacyRouteRedirect() {
  const location = useLocation();
  const path = location.pathname.replace(/\/+$/, "") || "/";
  let target: string | null = null;

  if (path === "/settings") target = "/settings/profile";
  else if (path.startsWith("/settings/")) {
    const id = path.slice("/settings/".length);
    const legacySettings: Record<string, string> = {
      profile: "/settings/profile",
      discover: "/manage/discover",
      dashboard: "/manage",
      libraries: "/manage/libraries",
      collections: "/manage/collections",
      relations: "/manage/relations",
      sources: "/manage/sources",
      tasks: "/manage/tasks",
      logs: "/manage/logs",
      statistics: "/manage/statistics",
      users: "/manage/users",
      backup: "/manage/backup",
    };
    target = legacySettings[id] ?? null;
  } else if (path === "/admin") target = "/manage";
  else if (path === "/admin/modules" || path.startsWith("/admin/modules/")) target = path.replace(/^\/admin\/modules/, "/manage/modules");
  else {
    const legacyAdmin: Record<string, string> = {
      dashboard: "/manage",
      discover: "/manage/discover",
      libraries: "/manage/libraries",
      collections: "/manage/collections",
      relations: "/manage/relations",
      sources: "/manage/sources",
      tasks: "/manage/tasks",
      logs: "/manage/logs",
      statistics: "/manage/statistics",
      users: "/manage/users",
      backup: "/manage/backup",
      data: "/manage/backup",
      providers: "/manage/modules",
      items: "/manage/modules",
    };
    target = legacyAdmin[path.slice("/admin/".length)] ?? null;
  }

  return target
    ? <Navigate to={{ pathname: target, search: location.search, hash: location.hash }} replace />
    : <NotFoundPage />;
}

export function AppRoutes() {
  return <Routes>
    <Route path="/" element={<EntryRoute />} />
    <Route path="/login" element={<LoginRoute />} />
    <Route path="/settings/profile" element={<RouteAccess><ProfileSettingsPage /></RouteAccess>} />
    <Route path="/settings/*" element={<LegacyRouteRedirect />} />
    <Route path="/admin/*" element={<LegacyRouteRedirect />} />
    <Route path="/clients" element={<RouteAccess><ClientConnectionPage /></RouteAccess>} />

    <Route element={<RouteAccess roles={contentManagers}><ManagementLayout /></RouteAccess>}>
      <Route path="/manage" element={<RouteAccess roles={contentManagers}><OverviewPage /></RouteAccess>} />
      <Route path="/manage/modules/*" element={<RouteAccess roles={["Admin"]}><ModuleCenterPage /></RouteAccess>} />
      <Route path="/manage/discover" element={<RouteAccess roles={contentManagers}><DiscoverPage /></RouteAccess>} />
      <Route path="/manage/libraries" element={<RouteAccess roles={contentManagers}><LibraryAdminPage /></RouteAccess>} />
      <Route path="/manage/collections" element={<RouteAccess roles={contentManagers}><CollectionAdminPage /></RouteAccess>} />
      <Route path="/manage/relations" element={<RouteAccess roles={contentManagers}><RelationAdminPage /></RouteAccess>} />
      <Route path="/manage/sources" element={<RouteAccess roles={contentManagers}><SourceAdminPage /></RouteAccess>} />
      <Route path="/manage/tasks" element={<RouteAccess roles={contentManagers}><TaskAdminPage /></RouteAccess>} />
      <Route path="/manage/logs" element={<RouteAccess roles={contentManagers}><AuditLogsPage /></RouteAccess>} />
      <Route path="/manage/statistics" element={<RouteAccess roles={contentManagers}><StatisticsAdminPage /></RouteAccess>} />
      <Route path="/manage/users" element={<RouteAccess roles={["Admin"]}><UserAdminPage /></RouteAccess>} />
      <Route path="/manage/backup" element={<RouteAccess roles={["Admin"]}><BackupAdminPage /></RouteAccess>} />
    </Route>

    <Route element={<RouteAccess><BrowseLayout /></RouteAccess>}>
      <Route path="/browse" element={<HomePage />} />
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
    </Route>
    <Route path="*" element={<NotFoundPage />} />
  </Routes>;
}
