import { useState } from "react";
import { Link } from "react-router-dom";
import { Icon, type IconName } from "../components/Icon";
import { userHasRole, type User } from "../lib/api";
import { settingsEntries } from "../lib/settings";

const groups: Array<{ id: string; title: string; description: string; icon: IconName }> = [
  { id: "personal", title: "个人偏好", description: "调整自己的外观与首页", icon: "theme" },
  { id: "content", title: "内容与媒体库", description: "添加、整理和维护媒体内容", icon: "library" },
  { id: "system", title: "系统与维护", description: "运行状态、任务和数据安全", icon: "shield" },
  { id: "modules", title: "模块设置", description: "来源账号、榜单更新和播放站点", icon: "module" },
];

export function SettingsCenterPage({ user }: { user: User }) {
  const [query, setQuery] = useState("");
  const entries: Array<{ id: string; title: string; description: string; group: string; to: string }> = settingsEntries.filter((entry) => entry.roles.length === 0 || userHasRole(user, ...entry.roles)).map((entry) => ({ ...entry, to: `/settings/${entry.id}` }));
  if (userHasRole(user, "Admin")) entries.push({ id: "modules", title: "管理模块", description: "安装、配置、启停和更新模块；设置来源账号、榜单和播放站点", group: "modules", to: "/admin/modules" });
  const keyword = query.trim().toLocaleLowerCase();
  const visible = entries.filter((entry) => `${entry.title} ${entry.description} ${groups.find((group) => group.id === entry.group)?.title}`.toLocaleLowerCase().includes(keyword));
  return <section className="content-card settings-center">
    <div className="settings-heading"><div><p className="eyebrow">让媒体库更顺手</p><h1>设置</h1><p className="muted">个人偏好、内容管理和服务维护，都能在这里找到。</p></div><Link to="/" className="ghost-button">返回首页</Link></div>
    <div className="settings-search"><Icon name="search" /><label className="sr-only" htmlFor="settings-search">查找设置</label><input id="settings-search" type="search" placeholder="查找设置，例如：备份、账号、播放源" value={query} onChange={(event) => setQuery(event.target.value)} />{query && <button type="button" className="icon-button" aria-label="清空设置搜索" onClick={() => setQuery("")}><Icon name="close" /></button>}</div>
    <nav className="settings-shortcuts" aria-label="设置分组">{groups.filter((group) => visible.some((entry) => entry.group === group.id)).map((group) => <a href={`#settings-${group.id}`} key={group.id}>{group.title}<span>{visible.filter((entry) => entry.group === group.id).length}</span></a>)}</nav>
    {keyword && <p className="muted" role="status">找到 {visible.length} 项设置</p>}
    <div className="settings-groups">{groups.map((group) => {
      const items = visible.filter((entry) => entry.group === group.id);
      if (!items.length) return null;
      return <section className="settings-category" aria-labelledby={`settings-${group.id}`} key={group.id}>
        <div className="settings-category-heading"><span className="section-icon"><Icon name={group.icon} /></span><div><h2 id={`settings-${group.id}`}>{group.title}</h2><p className="muted">{group.description}</p></div></div>
        <div className="settings-card-grid">{items.map((entry) => <Link className="settings-link-card" to={entry.to} key={entry.id}><div><strong>{entry.title}</strong><small>{entry.description}</small></div><Icon name="arrow" /></Link>)}</div>
      </section>;
    })}</div>
    {!visible.length && <div className="empty-card"><Icon name="search" /><h2>没有找到相关设置</h2><p className="muted">试试更短的关键词，或查看全部设置。</p><button className="ghost-button" onClick={() => setQuery("")}>查看全部设置</button></div>}
  </section>;
}
