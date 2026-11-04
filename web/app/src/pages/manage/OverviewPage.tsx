import { useEffect, useState, type ReactNode } from "react";
import { Link } from "react-router-dom";
import { apiFetch, readSession, userHasRole } from "../../lib/api";
import { type AdminDashboardData, type VirtualLibraryRecord } from "../../lib/appShared";

type Loadable<T> = { status: "loading" } | { status: "ready"; data: T } | { status: "error"; message: string };
type ManagedModule = {
  manifest: { id: string; name: string; type: string; supportedItemTypes?: string[] };
  status: string;
  enabled: boolean;
  libraryScope?: { mode: "all" | "selected"; libraryIds: string[] } | null;
};
type MetadataSource = { id: string; name: string; supportedItemTypes: string[] };
type ScopeStatus = { ready: boolean; module?: ManagedModule; reason: string };

function errorMessage(error: unknown): string { return error instanceof Error ? error.message : "读取失败"; }

function StateCard({ title, state, children, action }: { title: string; state: "ready" | "attention" | "unknown" | "neutral"; children: ReactNode; action?: ReactNode }) {
  const labels = { ready: "已具备", attention: "需要处理", unknown: "状态未知", neutral: "待核对" };
  return <article className={`setup-step setup-step-${state}`}>
    <div className="setup-step-heading"><h3>{title}</h3><span>{labels[state]}</span></div>
    <div className="setup-step-body">{children}</div>
    {action && <div className="setup-step-actions">{action}</div>}
  </article>;
}

function ErrorState({ message }: { message: string }) {
  return <p className="form-error" role="alert">无法确认当前状态：{message}</p>;
}

function findPlaybackScope(modules: ManagedModule[], libraries: VirtualLibraryRecord[]): ScopeStatus {
  const running = modules.filter((module) => module.enabled && module.status === "active" && module.manifest.type !== "metadata");
  if (!running.length) return { ready: false, reason: "没有正在运行的视频能力模块。" };
  const availableLibraryIds = new Set(libraries.filter((library) => library.enabled).map((library) => library.id));
  const matching = running.find((module) => {
    const scope = module.libraryScope;
    return !scope || scope.mode === "all" || scope.libraryIds.some((id) => availableLibraryIds.has(id));
  });
  if (matching) return { ready: true, module: matching, reason: `“${matching.manifest.name}”已配置适用范围。` };
  const selected = running[0];
  return { ready: false, module: selected, reason: `“${selected.manifest.name}”没有匹配当前已启用媒体库的范围。` };
}

export function OverviewPage() {
  const user = readSession()?.user;
  const isAdmin = userHasRole(user, "Admin");
  const [dashboard, setDashboard] = useState<Loadable<AdminDashboardData>>({ status: "loading" });
  const [libraries, setLibraries] = useState<Loadable<VirtualLibraryRecord[]>>({ status: "loading" });
  const [modules, setModules] = useState<Loadable<ManagedModule[] | MetadataSource[]>>({ status: "loading" });
  const [refreshing, setRefreshing] = useState(false);

  async function load() {
    setRefreshing(true);
    setDashboard({ status: "loading" });
    setLibraries({ status: "loading" });
    setModules({ status: "loading" });
    const requests = await Promise.allSettled([
      apiFetch<AdminDashboardData>("/api/v1/dashboard"),
      apiFetch<{ items: VirtualLibraryRecord[] }>("/api/v1/libraries"),
      isAdmin
        ? apiFetch<{ items: ManagedModule[] }>("/api/v1/admin/modules")
        : apiFetch<{ items: MetadataSource[] }>("/api/v1/metadata/modules"),
    ]);
    const [dashboardResult, libraryResult, moduleResult] = requests;
    setDashboard(dashboardResult.status === "fulfilled" ? { status: "ready", data: dashboardResult.value } : { status: "error", message: errorMessage(dashboardResult.reason) });
    setLibraries(libraryResult.status === "fulfilled" ? { status: "ready", data: libraryResult.value.items } : { status: "error", message: errorMessage(libraryResult.reason) });
    setModules(moduleResult.status === "fulfilled" ? { status: "ready", data: moduleResult.value.items } : { status: "error", message: errorMessage(moduleResult.reason) });
    setRefreshing(false);
  }

  useEffect(() => { void load(); }, [isAdmin]);

  const readyLibraries = libraries.status === "ready" ? libraries.data : [];
  const managedModules = modules.status === "ready" && isAdmin ? modules.data as ManagedModule[] : [];
  const metadataSources = modules.status === "ready" && !isAdmin ? modules.data as MetadataSource[] : [];
  const activeMetadata = isAdmin
    ? managedModules.filter((module) => module.enabled && module.status === "active" && module.manifest.type !== "stream")
    : metadataSources;
  const activeStreams = isAdmin
    ? managedModules.filter((module) => module.enabled && module.status === "active" && module.manifest.type !== "metadata")
    : [];
  const playbackScope = isAdmin && libraries.status === "ready" ? findPlaybackScope(managedModules, readyLibraries) : null;
  const noLibraries = libraries.status === "ready" && readyLibraries.length === 0;
  const noItems = dashboard.status === "ready" && dashboard.data.resources.items === 0;

  return <section className="management-page overview-page">
    <div className="page-heading">
      <div><p className="eyebrow">JMlab 管理</p><h1>{isAdmin ? "从配置到观看" : "内容工作区"}</h1><p className="muted">{isAdmin ? "根据宿主当前状态查看下一步。状态读取失败时会明确标记为未知。" : "从可用的信息来源添加内容，检查当前账号可访问的媒体库。"}</p></div>
      <button type="button" className="ghost-button" disabled={refreshing} onClick={() => void load()}>{refreshing ? "正在刷新…" : "刷新状态"}</button>
    </div>

    {isAdmin ? <>
      <section className="setup-overview" aria-labelledby="setup-overview-title">
        <div className="section-heading"><div><p className="eyebrow">首次配置</p><h2 id="setup-overview-title">配置检查</h2></div><Link to="/clients" className="ghost-button">客户端连接信息</Link></div>
        <div className="setup-step-grid">
          <StateCard title="安装并运行模块" state={modules.status === "error" ? "unknown" : modules.status === "loading" ? "unknown" : activeMetadata.length ? "ready" : "attention"} action={<Link to="/manage/modules" className="ghost-button">管理模块</Link>}>
            {modules.status === "loading" ? <p role="status">正在读取模块状态…</p> : modules.status === "error" ? <ErrorState message={modules.message} /> : activeMetadata.length ? <p>有 {activeMetadata.length} 个内容信息能力正在运行。</p> : <p>没有正在运行的内容信息能力；添加内容前需要安装并启用信息模块。</p>}
          </StateCard>
          <StateCard title="搜索并添加内容" state={dashboard.status === "error" ? "unknown" : dashboard.status === "loading" ? "unknown" : noItems ? "attention" : "ready"} action={<Link to="/manage/discover" className="ghost-button">添加内容</Link>}>
            {dashboard.status === "loading" ? <p role="status">正在读取内容状态…</p> : dashboard.status === "error" ? <ErrorState message={dashboard.message} /> : noItems ? <p>{activeMetadata.length ? "还没有入库作品。" : "先完成内容信息模块配置，再搜索并导入作品。"}</p> : <p>当前可见范围内有 {dashboard.data.resources.items} 部作品。入库不代表已匹配视频来源。</p>}
          </StateCard>
          <StateCard title="创建媒体库" state={libraries.status === "error" ? "unknown" : libraries.status === "loading" ? "unknown" : noLibraries ? "attention" : "ready"} action={<Link to="/manage/libraries" className="ghost-button">媒体库配置</Link>}>
            {libraries.status === "loading" ? <p role="status">正在读取媒体库…</p> : libraries.status === "error" ? <ErrorState message={libraries.message} /> : noLibraries ? <p>还没有可用媒体库。媒体库模板只整理已入库内容，不会下载或导入视频。</p> : <p>当前可访问 {readyLibraries.length} 个媒体库；筛选范围决定哪些已入库作品会显示。</p>}
          </StateCard>
          <StateCard title="设置视频模块范围" state={modules.status === "error" || libraries.status === "error" ? "unknown" : modules.status === "loading" || libraries.status === "loading" ? "unknown" : noLibraries || activeStreams.length === 0 || !playbackScope?.ready ? "attention" : "ready"} action={playbackScope?.module ? <Link to={`/manage/modules/${encodeURIComponent(playbackScope.module.manifest.id)}`} className="ghost-button">配置适用媒体库</Link> : <Link to="/manage/modules" className="ghost-button">管理视频模块</Link>}>
            {modules.status === "error" ? <ErrorState message={modules.message} /> : libraries.status === "error" ? <ErrorState message={libraries.message} /> : modules.status === "loading" || libraries.status === "loading" ? <p role="status">正在核对视频模块和媒体库…</p> : noLibraries ? <p>先创建媒体库，再设置视频模块适用范围。</p> : !playbackScope?.ready ? <p>{playbackScope?.reason ?? "没有正在运行的视频能力模块。"}</p> : <p>{playbackScope.reason} 模块运行状态不代表每部作品都有可播放来源。</p>}
          </StateCard>
          <StateCard title="核对用户授权" state="neutral" action={<><Link to="/manage/libraries" className="ghost-button">检查查看权限</Link><Link to="/manage/users" className="ghost-button">用户与权限</Link></>}>
            <p>媒体库存在不代表目标用户可查看或播放。分别检查每个用户或角色的查看与播放权限。</p>
          </StateCard>
          <StateCard title="客户端连接" state="neutral" action={<Link to="/clients" className="ghost-button">查看连接地址</Link>}>
            <p>页面提供服务端配置地址和登录说明，不代表任意客户端已连接或真实媒体已解码。</p>
          </StateCard>
        </div>
      </section>
      <section className="overview-summary" aria-labelledby="overview-summary-title">
        <div className="section-heading"><div><p className="eyebrow">维护状态</p><h2 id="overview-summary-title">当前摘要</h2></div></div>
        {dashboard.status === "loading" ? <p className="muted" role="status">正在读取运行概况…</p> : dashboard.status === "error" ? <ErrorState message={dashboard.message} /> : <>
          <div className="overview-metrics"><div><span>可见作品</span><strong>{dashboard.data.resources.items}</strong></div><div><span>有可用来源的作品</span><strong>{dashboard.data.resources.playableItems}</strong></div><div><span>当前可访问媒体库</span><strong>{libraries.status === "ready" ? readyLibraries.length : "—"}</strong></div><div><span>失败任务</span><strong>{dashboard.data.errors.taskFailures}</strong></div></div>
          <p className="muted overview-scope-note">作品和播放源统计按当前管理范围汇总；来源可用性不等同于客户端格式兼容或实际播放验证。</p>
          <div className="table-actions"><Link to="/manage/tasks" className="ghost-button">查看任务</Link><Link to="/manage/statistics" className="ghost-button">查看媒体统计</Link></div>
        </>}
      </section>
    </> : <>
      <section className="editor-workspace">
        <div className="section-heading"><div><p className="eyebrow">内容整理</p><h2>可以继续的工作</h2></div></div>
        <div className="setup-step-grid editor-step-grid">
          <StateCard title="搜索并添加内容" state={modules.status === "error" ? "unknown" : modules.status === "loading" ? "unknown" : metadataSources.length ? "ready" : "attention"} action={<Link to="/manage/discover" className="ghost-button">添加内容</Link>}>
            {modules.status === "loading" ? <p role="status">正在读取内容来源…</p> : modules.status === "error" ? <ErrorState message={modules.message} /> : metadataSources.length ? <p>有 {metadataSources.length} 个当前可用的信息来源。</p> : <p>暂时没有可用的信息来源，请联系管理员启用模块。</p>}
          </StateCard>
          <StateCard title="查看媒体库" state={libraries.status === "error" ? "unknown" : libraries.status === "loading" ? "unknown" : noLibraries ? "attention" : "ready"} action={<Link to="/manage/libraries" className="ghost-button">媒体库</Link>}>
            {libraries.status === "loading" ? <p role="status">正在读取媒体库…</p> : libraries.status === "error" ? <ErrorState message={libraries.message} /> : noLibraries ? <p>当前账号没有可用媒体库。请联系管理员检查查看权限。</p> : <p>当前账号可查看 {readyLibraries.length} 个媒体库。Editor 只能查看，不能修改库配置或用户授权。</p>}
          </StateCard>
        </div>
        <div className="table-actions"><Link to="/clients" className="ghost-button">客户端连接信息</Link><Link to="/browse" className="ghost-button">浏览媒体</Link></div>
      </section>
      <section className="overview-summary" aria-labelledby="editor-summary-title">
        <div className="section-heading"><div><p className="eyebrow">当前范围</p><h2 id="editor-summary-title">内容状态</h2></div></div>
        {dashboard.status === "loading" ? <p className="muted" role="status">正在读取内容状态…</p> : dashboard.status === "error" ? <ErrorState message={dashboard.message} /> : <div className="overview-metrics"><div><span>当前范围内作品</span><strong>{dashboard.data.resources.items}</strong></div><div><span>有可用来源的作品</span><strong>{dashboard.data.resources.playableItems}</strong></div><div><span>可访问媒体库</span><strong>{libraries.status === "ready" ? readyLibraries.length : "—"}</strong></div></div>}
      </section>
    </>}
  </section>;
}
