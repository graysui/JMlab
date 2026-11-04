import { NavLink, Outlet } from "react-router-dom";
import { Icon } from "../components/Icon";

const browseNavigation = [
  { to: "/browse", label: "首页", icon: "home" as const, end: true },
  { to: "/search", label: "搜索", icon: "search" as const },
  { to: "/rankings", label: "排行", icon: "ranking" as const },
  { to: "/libraries", label: "媒体库", icon: "library" as const },
  { to: "/playlists", label: "我的清单", icon: "list" as const },
];

export function BrowseLayout() {
  return <div className="browse-layout">
    <nav className="browse-nav" aria-label="浏览导航">
      {browseNavigation.map((item) => <NavLink key={item.to} to={item.to} end={item.end} className={({ isActive }) => isActive ? "active" : ""}>
        <Icon name={item.icon} /><span>{item.label}</span>
      </NavLink>)}
    </nav>
    <main className="browse-content page-shell" id="main-content" tabIndex={-1}><Outlet /></main>
  </div>;
}
