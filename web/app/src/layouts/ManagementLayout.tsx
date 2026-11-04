import { useEffect, useMemo, useRef, useState } from "react";
import { Link, NavLink, Outlet } from "react-router-dom";
import { Icon } from "../components/Icon";
import { readSession, userHasRole } from "../lib/api";
import { managementGroups, managementNavigation } from "../lib/navigation";

export function ManagementLayout() {
  const user = readSession()?.user;
  const [query, setQuery] = useState("");
  const [drawerOpen, setDrawerOpen] = useState(false);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const drawerRef = useRef<HTMLElement>(null);
  const visibleItems = useMemo(() => managementNavigation.filter((item) => {
    const roleAllowed = item.roles.length === 0 || userHasRole(user, ...item.roles);
    const searchText = `${item.label} ${item.description}`.toLocaleLowerCase();
    return roleAllowed && searchText.includes(query.trim().toLocaleLowerCase());
  }), [query, user]);

  useEffect(() => {
    if (!drawerOpen) return;
    const previousOverflow = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    drawerRef.current?.querySelector<HTMLElement>("a[href]")?.focus();
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        event.preventDefault();
        setDrawerOpen(false);
        return;
      }
      if (event.key !== "Tab" || !drawerRef.current) return;
      const focusable = Array.from(drawerRef.current.querySelectorAll<HTMLElement>("a[href], button:not([disabled]), input:not([disabled])"));
      const first = focusable[0];
      const last = focusable[focusable.length - 1];
      if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last?.focus(); }
      else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first?.focus(); }
    };
    window.addEventListener("keydown", onKeyDown);
    return () => {
      document.body.style.overflow = previousOverflow;
      window.removeEventListener("keydown", onKeyDown);
      triggerRef.current?.focus();
    };
  }, [drawerOpen]);

  function navigationContent() {
    return <>
      <label className="management-search">
        <Icon name="search" />
        <span className="sr-only">查找管理入口</span>
        <input type="search" value={query} onChange={(event) => setQuery(event.target.value)} placeholder="查找管理入口" />
        {query && <button type="button" className="icon-button" aria-label="清除搜索" onClick={() => setQuery("")}><Icon name="close" /></button>}
      </label>
      <nav aria-label="管理导航" className="management-nav">
        {managementGroups.map((group) => {
          const items = visibleItems.filter((item) => item.group === group.id);
          if (!items.length) return null;
          return <section className="management-nav-group" key={group.id}>
            <h2>{group.label}</h2>
            {items.map((item) => <NavLink key={item.id} to={item.to} end={item.to === "/manage"} onClick={() => setDrawerOpen(false)} className={({ isActive }) => isActive ? "active" : ""}>
              <Icon name={item.icon} /><span>{item.label}</span>
            </NavLink>)}
          </section>;
        })}
        {query && visibleItems.length === 0 && <p className="muted management-search-empty" role="status">没有匹配的管理入口。</p>}
      </nav>
      <Link className="management-client-link" to="/clients"><Icon name="arrow" />客户端连接</Link>
    </>;
  }

  return <div className="management-layout">
    <button ref={triggerRef} className="management-mobile-menu" type="button" aria-haspopup="dialog" aria-expanded={drawerOpen} onClick={() => setDrawerOpen(true)}>
      <Icon name="menu" /><span>管理导航</span>
    </button>
    <aside className="management-sidebar">{navigationContent()}</aside>
    {drawerOpen && <>
      <button className="management-drawer-backdrop" type="button" aria-label="关闭管理导航" onClick={() => setDrawerOpen(false)} />
      <aside ref={drawerRef} className="management-sidebar management-sidebar-drawer" role="dialog" aria-modal="true" aria-label="管理导航">
        <div className="management-drawer-heading"><strong>管理导航</strong><button type="button" className="icon-button" aria-label="关闭管理导航" onClick={() => setDrawerOpen(false)}><Icon name="close" /></button></div>
        {navigationContent()}
      </aside>
    </>}
    <main className="management-content" id="management-content"><Outlet /></main>
  </div>;
}
