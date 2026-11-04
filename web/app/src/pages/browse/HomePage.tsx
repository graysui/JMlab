import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { ModuleRankingLinks } from "../../components/ModuleRankingLinks";
import { ItemGrid } from "../../components/media/MediaCards";
import { apiFetch, readSession, userHasRole } from "../../lib/api";
import { homeModuleLabels, homeModuleOrder, type HomeDashboard, type PreferencesData } from "../../lib/appShared";

export function HomePage() {
  const session = readSession();
  const [dashboard, setDashboard] = useState<HomeDashboard | null>(null);
  const [preferences, setPreferences] = useState<PreferencesData | null>(null);
  const [loading, setLoading] = useState(Boolean(session));
  const [error, setError] = useState("");

  useEffect(() => {
    if (!session) { setLoading(false); return; }
    let cancelled = false;
    setLoading(true);
    setError("");
    Promise.all([apiFetch<HomeDashboard>("/api/v1/home?limit=12"), apiFetch<PreferencesData>("/api/v1/me/preferences")])
      .then(([home, prefs]) => { if (!cancelled) { setDashboard(home); setPreferences(prefs); } })
      .catch((requestError) => { if (!cancelled) setError(requestError instanceof Error ? requestError.message : "读取首页失败"); })
      .finally(() => { if (!cancelled) setLoading(false); });
    return () => { cancelled = true; };
  }, [session?.accessToken]);

  if (!session) return <section className="hero-grid"><div className="hero-card"><p className="eyebrow">虚拟媒体服务器</p><h1>把电影、剧集和个人视频整理成自己的媒体库。</h1><p className="muted">登录后浏览有权访问的媒体库，也可以用自己的清单整理想看的内容。</p><div className="hero-actions"><Link to="/login" className="primary-button">登录开始</Link></div></div></section>;
  if (loading) return <section className="content-card home-dashboard"><p className="eyebrow">JMlab 首页</p><h1>正在加载你的媒体库…</h1><div className="skeleton-grid">{[1, 2, 3, 4].map((value) => <div className="skeleton-card" key={value} />)}</div></section>;
  if (error) return <section className="content-card"><p className="eyebrow">首页</p><h1>暂时无法读取首页</h1><p className="form-error" role="alert">{error}</p><div className="table-actions"><Link to="/libraries" className="ghost-button">打开媒体库</Link><Link to="/clients" className="ghost-button">客户端连接信息</Link></div></section>;
  if (!dashboard) return null;

  const enabled = (key: string) => preferences?.moduleFlags?.[key] !== false;
  const hasVisibleModule = homeModuleOrder.some((key) => enabled(key) && Boolean(dashboard.modules[key]?.items?.length));
  const canManageContent = userHasRole(session.user, "Admin", "Editor");

  return <section className="home-dashboard">
    <div className="home-hero"><div><p className="eyebrow">我的媒体</p><h1>欢迎回来，{session.user.displayName || session.user.username}</h1><p className="muted">从上次观看的地方继续，或发现下一部想看的作品。</p><div className="hero-actions"><Link to="/search" className="primary-button">搜索作品</Link><Link to="/rankings" className="ghost-button">查看排行</Link><Link to="/libraries" className="ghost-button">浏览媒体库</Link><Link to="/clients" className="ghost-button">客户端连接</Link><Link to="/settings/profile" className="ghost-button">个人偏好</Link></div></div><div className="home-stats"><div><strong>{dashboard.stats.visibleItems}</strong><span>可见作品</span></div><div><strong>{dashboard.stats.playableItems}</strong><span>有可用来源</span></div><div><strong>{dashboard.stats.people}</strong><span>人物</span></div></div></div>
    {homeModuleOrder.filter(enabled).map((key) => { const module = dashboard.modules[key]; if (!module || module.items.length === 0) return null; return <section className="home-module" key={key}><div className="home-module-heading"><div><p className="eyebrow">{module.title || homeModuleLabels[key]}</p><h2>{homeModuleLabels[key]}</h2></div><Link to="/search" className="ghost-button compact-button">查看全部</Link></div><ItemGrid items={module.items} compact /></section>; })}
    {enabled("ranking") && <ModuleRankingLinks />}
    {!hasVisibleModule && <div className="empty-card"><div className="empty-icon">＋</div><h2>当前账号没有可见作品</h2><p className="muted">已入库作品、媒体库可见权限和视频播放权限由管理员分别配置。连接地址可用于填写客户端；此页不会验证客户端连接或媒体解码。</p><div className="table-actions"><Link to="/clients" className="primary-button">查看客户端连接信息</Link><Link to="/libraries" className="ghost-button">检查可访问媒体库</Link>{canManageContent && <Link to="/manage/discover" className="ghost-button">添加内容</Link>}</div></div>}
  </section>;
}
