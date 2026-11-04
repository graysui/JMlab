import { LibraryTemplatePicker, type LibraryTemplateSelection, type ResolvedLibraryTemplate } from "../../components/LibraryTemplatePicker";
import { apiFetch, readSession, userHasRole, type User } from "../../lib/api";
import { FormEvent, useEffect, useRef, useState } from "react";
import { Link } from "react-router-dom";
import { VirtualLibraryRecord } from "../../lib/appShared";

export type LibraryAdminForm = { name: string; description: string; itemType: VirtualLibraryRecord["itemType"]; filterJSON: string; sortField: string; sortDirection: string; posterImageId: string; isPublic: boolean; isAdult: boolean; enabled: boolean };

export const emptyLibraryAdminForm: LibraryAdminForm = { name: "", description: "", itemType: "movie", filterJSON: "{}", sortField: "updatedAt", sortDirection: "desc", posterImageId: "", isPublic: true, isAdult: true, enabled: true };

export function libraryAdminForm(library: VirtualLibraryRecord): LibraryAdminForm { return { name: library.name, description: library.description ?? "", itemType: library.itemType, filterJSON: JSON.stringify(library.filter ?? {}, null, 2), sortField: library.sort?.field ?? "updatedAt", sortDirection: library.sort?.direction ?? "desc", posterImageId: library.posterImageId ?? "", isPublic: library.isPublic, isAdult: library.isAdult, enabled: library.enabled }; }

export type LibraryPermissionRecord = { subjectType: "user" | "role" | string; subjectId: string; name?: string; canView: boolean; canPlay: boolean };

export const libraryRoleOptions: Array<{ id: string; name: string }> = [{ id: "role-admin", name: "管理员（Admin）" }, { id: "role-editor", name: "编辑者（Editor）" }, { id: "role-viewer", name: "查看者（Viewer）" }];

export function LibraryAdminPage() {
  const session = readSession();
  const canManage = userHasRole(session?.user, "Admin");
  const [libraries, setLibraries] = useState<VirtualLibraryRecord[]>([]);
  const [editing, setEditing] = useState<VirtualLibraryRecord | null>(null);
  const [form, setForm] = useState<LibraryAdminForm>(emptyLibraryAdminForm);
  const [editorOpen, setEditorOpen] = useState(false);
  const [previewCounts, setPreviewCounts] = useState<Record<string, number>>({});
  const [permissionLibrary, setPermissionLibrary] = useState<VirtualLibraryRecord | null>(null);
  const [permissionUsers, setPermissionUsers] = useState<User[]>([]);
  const [permissionDraft, setPermissionDraft] = useState<Record<string, LibraryPermissionRecord>>({});
  const [loading, setLoading] = useState(true); const [busy, setBusy] = useState(false); const [error, setError] = useState(""); const [message, setMessage] = useState("");

  const [showAdvancedJson, setShowAdvancedJson] = useState(false);
  const [templateSelection, setTemplateSelection] = useState<LibraryTemplateSelection | null>(null);
  const [editorKey, setEditorKey] = useState(0);
  const [yearsDraft, setYearsDraft] = useState("");
  const suggestedName = useRef("");
  const suggestedDescription = useRef("");
  let visualFilter: Record<string, unknown> = {};
  let visualAvailable = true;
  try {
    const parsed: unknown = JSON.parse(form.filterJSON);
    if (!parsed || typeof parsed !== "object" || Array.isArray(parsed) || ["all", "any", "not", "field"].some((key) => key in parsed)) visualAvailable = false;
    else visualFilter = parsed as Record<string, unknown>;
  } catch { visualAvailable = false; }
  const minRating = visualFilter.minRating == null ? "" : String(visualFilter.minRating);
  const years = Array.isArray(visualFilter.years) ? visualFilter.years.join(",") : "";
  const hasPlayableSource = visualFilter.hasPlayableSource === true;
  const isFavorite = visualFilter.isFavorite === true;
  useEffect(() => { setYearsDraft(years); }, [years, editorKey]);

  async function loadLibraries() { setLoading(true); try { const result = await apiFetch<{ items: VirtualLibraryRecord[] }>(`/api/v1/libraries${canManage ? "?includeDisabled=1" : ""}`); setLibraries(result.items); setError(""); } catch (requestError) { setError(requestError instanceof Error ? requestError.message : "读取虚拟媒体库失败"); } finally { setLoading(false); } }
  useEffect(() => { void loadLibraries(); }, [canManage]);
  useEffect(() => { if (!canManage) return; apiFetch<{ items: User[] }>("/api/v1/admin/users").then((result) => setPermissionUsers(result.items)).catch(() => setPermissionUsers([])); }, [canManage]);
  function updateForm<K extends keyof LibraryAdminForm>(key: K, value: LibraryAdminForm[K]) { setForm((current) => ({ ...current, [key]: value })); }

  function syncVisualToFilter(overrides: Record<string, unknown>) {
    if (!visualAvailable) return;
    const next = { ...visualFilter };
    for (const [key, value] of Object.entries(overrides)) {
      if (value == null || value === "" || value === false) delete next[key];
      else next[key] = value;
    }
    updateForm("filterJSON", JSON.stringify(next, null, 2));
  }
  function applyTemplate(template: ResolvedLibraryTemplate, selection: LibraryTemplateSelection) {
    setTemplateSelection(selection);
    const previousName = suggestedName.current;
    const previousDescription = suggestedDescription.current;
    setForm((current) => ({ ...current,
      name: !current.name || current.name === previousName ? template.name : current.name,
      description: !current.description || current.description === previousDescription ? template.description : current.description,
      itemType: template.itemType, isAdult: template.isAdult, filterJSON: JSON.stringify(template.filter, null, 2),
      sortField: template.sort.field, sortDirection: template.sort.direction,
    }));
    // Keep names entered by the user when a template or range changes.
    suggestedName.current = template.name; suggestedDescription.current = template.description;
    setShowAdvancedJson(false);
  }
  function resetTemplate() {
    setTemplateSelection(null);
    if (!editing) { setForm(emptyLibraryAdminForm); suggestedName.current = ""; suggestedDescription.current = ""; }
  }
  function startCreate() {
    setEditing(null); setForm(emptyLibraryAdminForm); setTemplateSelection(null);
    suggestedName.current = ""; suggestedDescription.current = "";
    setEditorKey((key) => key + 1); setShowAdvancedJson(false); setEditorOpen(true); setMessage(""); setError("");
  }
  function startEdit(library: VirtualLibraryRecord) {
    setEditing(library); setForm(libraryAdminForm(library)); setTemplateSelection(null);
    suggestedName.current = ""; suggestedDescription.current = "";
    setEditorKey((key) => key + 1); setShowAdvancedJson(true); setEditorOpen(true); setMessage(""); setError("");
    window.scrollTo({ top: 0, behavior: "smooth" });
  }

  async function saveLibrary(event: FormEvent) {
    event.preventDefault();
    if (!editing && !templateSelection) { setError("请先选择模块和预设模板"); return; }
    setBusy(true);
    setError("");
    setMessage("");
    let filter: unknown;
    try {
      filter = JSON.parse(form.filterJSON || "{}");
    } catch {
      setError("过滤器必须是合法 JSON");
      setBusy(false);
      return;
    }
    if (visualAvailable) {
      const values = yearsDraft.trim() ? yearsDraft.trim().split(/[\s,，]+/).map(Number) : [];
      if (values.some((year) => !Number.isInteger(year) || year < 1900 || year > 2100)) {
        setError("请填写 1900 到 2100 之间的年份，用逗号分隔"); setBusy(false); return;
      }
      const next = { ...visualFilter };
      if (values.length) next.years = values; else delete next.years;
      filter = next;
    }
    const payload = {
      ...(!editing && templateSelection ? { template: templateSelection } : {}),
      name: form.name.trim(),
      description: form.description.trim(),
      itemType: form.itemType,
      filter,
      sort: { field: form.sortField, direction: form.sortDirection },
      posterImageId: form.posterImageId.trim(),
      isPublic: form.isPublic,
      isAdult: form.isAdult,
      enabled: form.enabled,
    };
    try {
      await apiFetch<VirtualLibraryRecord>(editing ? `/api/v1/libraries/${editing.id}` : "/api/v1/libraries", { method: editing ? "PATCH" : "POST", body: JSON.stringify(payload) });
      setMessage(editing ? "媒体库已更新" : "媒体库已创建");
      setEditorOpen(false);
      setEditing(null);
      setForm(emptyLibraryAdminForm);
      await loadLibraries();
    } catch (requestError) {
      setError(requestError instanceof Error ? requestError.message : "保存媒体库失败");
    } finally {
      setBusy(false);
    }
  }

  async function removeLibrary(library: VirtualLibraryRecord) { if (!window.confirm(`确定删除「${library.name}」吗？`)) return; setBusy(true); setError(""); try { await apiFetch(`/api/v1/libraries/${library.id}`, { method: "DELETE" }); if (permissionLibrary?.id === library.id) setPermissionLibrary(null); await loadLibraries(); setMessage("媒体库已删除"); } catch (requestError) { setError(requestError instanceof Error ? requestError.message : "删除媒体库失败"); } finally { setBusy(false); } }
  async function previewLibrary(library: VirtualLibraryRecord) { try { const result = await apiFetch<{ itemCount: number }>(`/api/v1/libraries/${library.id}/preview`); setPreviewCounts((current) => ({ ...current, [library.id]: result.itemCount })); } catch (requestError) { setError(requestError instanceof Error ? requestError.message : "预览媒体库失败"); } }
  async function openPermissions(library: VirtualLibraryRecord) { setPermissionLibrary(library); setError(""); try { const result = await apiFetch<{ permissions: LibraryPermissionRecord[] }>(`/api/v1/libraries/${library.id}/permissions`); const next: Record<string, LibraryPermissionRecord> = {}; result.permissions.forEach((permission) => { next[`${permission.subjectType}:${permission.subjectId}`] = permission; }); setPermissionDraft(next); } catch (requestError) { setPermissionLibrary(null); setError(requestError instanceof Error ? requestError.message : "读取媒体库授权失败"); } }
  function updatePermission(subjectType: "user" | "role", subjectId: string, field: "canView" | "canPlay", value: boolean) { const key = `${subjectType}:${subjectId}`; setPermissionDraft((current) => ({ ...current, [key]: { ...(current[key] ?? {}), subjectType, subjectId, [field]: value } })); }
  async function savePermissions() { if (!permissionLibrary) return; setBusy(true); setError(""); try { const permissions = Object.values(permissionDraft).filter((permission) => permission.canView || permission.canPlay); await apiFetch(`/api/v1/libraries/${permissionLibrary.id}/permissions`, { method: "PUT", body: JSON.stringify({ permissions }) }); setMessage("媒体库授权已保存"); await openPermissions(permissionLibrary); } catch (requestError) { setError(requestError instanceof Error ? requestError.message : "保存媒体库授权失败"); } finally { setBusy(false); } }
  const permissionSubjects = [...libraryRoleOptions.map((role) => ({ subjectType: "role" as const, subjectId: role.id, name: role.name, detail: "角色" })), ...permissionUsers.map((user) => ({ subjectType: "user" as const, subjectId: user.id, name: user.displayName || user.username, detail: `用户 · ${user.username}` }))];

  return (
    <section className="content-card management-page">
      <div className="relation-page-heading">
        <div>
          <p className="eyebrow">虚拟媒体库</p>
          <h1>媒体库配置</h1>
          <p className="muted">先选择模块，再从模块提供的模板和榜单范围创建虚拟库。</p>
        </div>
        <Link to="/manage" className="ghost-button">返回设置</Link>
      </div>
      <div className="admin-toolbar">
        {canManage && <button type="button" className="primary-button" onClick={startCreate}>新建媒体库</button>}
        <Link to="/manage/collections" className="ghost-button">管理合集</Link>
        <Link to="/manage" className="ghost-button">查看运行概况</Link>
      </div>
      {error && <p className="form-error" role="alert">{error}</p>}
      {message && <p className="form-success" role="status">{message}</p>}
      {canManage && editorOpen && (
        <form className="relation-editor" onSubmit={saveLibrary}>
          <div className="relation-editor-heading">
            <div>
              <p className="eyebrow">{editing ? "编辑媒体库" : "新建媒体库"}</p>
              <h2>{editing ? editing.name : "定义一个虚拟库"}</h2>
            </div>
            <button type="button" className="ghost-button" onClick={() => setEditorOpen(false)}>关闭</button>
          </div>
          {editing ? <details className="wide-field"><summary>使用模块模板替换筛选规则</summary><p className="muted">选择模板会替换当前范围和排序；直接保存会保留原规则。</p><LibraryTemplatePicker key={editorKey} onApply={applyTemplate} onReset={resetTemplate} /></details> : <LibraryTemplatePicker key={editorKey} onApply={applyTemplate} onReset={resetTemplate} />}
          <fieldset className="library-fields wide-field" disabled={!editing && !templateSelection}>
          <label>
            名称
            <input required maxLength={200} value={form.name} onChange={(event) => updateForm("name", event.target.value)} />
          </label>
          <label>
            类型
            <select value={form.itemType} onChange={(event) => updateForm("itemType", event.target.value as LibraryAdminForm["itemType"])}>
              <option value="movie">作品</option>
              <option value="collection">合集</option>
              <option value="people">演员</option>
              <option value="mixed">混合</option>
            </select>
          </label>
          <label className="wide-field">
            说明
            <textarea maxLength={2000} value={form.description} onChange={(event) => updateForm("description", event.target.value)} />
          </label>

          <div className="wide-field filter-builder-container">
            <div className="filter-presets-header">
              <strong>调整筛选条件</strong>
              <button type="button" className="ghost-button compact-button" onClick={() => setShowAdvancedJson(!showAdvancedJson)}>{showAdvancedJson ? "收起 JSON 模式" : "高级 JSON 模式"}</button>
            </div>
            {(visualFilter.metadataModuleId != null || visualFilter.streamModuleId != null || visualFilter.rankingType != null) && <p className="muted">当前范围：{visualFilter.metadataModuleId != null ? "所选模块的已入库内容" : visualFilter.streamModuleId != null ? "所选模块的可播放内容" : "已有榜单"}{visualFilter.rankingType != null ? ` · ${{ ranking: "热门榜", playback: "热播榜", top250: "TOP 250", hot: "热播榜" }[String(visualFilter.rankingType)] ?? "榜单"} · ${{ daily: "日榜", weekly: "周榜", monthly: "月榜", all: "历史总榜" }[String(visualFilter.rankingPeriod)] ?? `${visualFilter.rankingPeriod ?? "全部"} 年`}` : ""}</p>}
            {!visualAvailable && <p className="muted">当前为自定义规则，请在高级 JSON 模式中编辑；原有条件会完整保留。</p>}
            <fieldset className="filter-visual-grid library-visual-fields" disabled={!visualAvailable}>
              <label>
                <span>最低评分要求</span>
                <select value={minRating} onChange={(e) => { const v = e.target.value; syncVisualToFilter({ minRating: v ? Number(v) : null }); }}>
                  <option value="">不限评分</option>
                  {minRating && !["6", "7", "7.5", "8", "8.5"].includes(minRating) && <option value={minRating}>{minRating} 分及以上</option>}
                  <option value="6">6.0 分及以上</option>
                  <option value="7">7.0 分及以上</option>
                  <option value="7.5">7.5 分及以上（推荐）</option>
                  <option value="8">8.0 分及以上（优秀）</option>
                  <option value="8.5">8.5 分及以上（神作）</option>
                </select>
              </label>
              <label>
                <span>发行年份</span>
                <input placeholder="如 2025 或 2024,2025" value={yearsDraft} onChange={(e) => { e.target.setCustomValidity(""); setYearsDraft(e.target.value); }} onBlur={(e) => {
                  const values = yearsDraft.trim() ? yearsDraft.trim().split(/[\s,，]+/).map(Number) : [];
                  if (values.some((year) => !Number.isInteger(year) || year < 1900 || year > 2100)) { e.target.setCustomValidity("请填写 1900 到 2100 之间的年份，用逗号分隔"); e.target.reportValidity(); return; }
                  syncVisualToFilter({ years: values.length ? values : null });
                }} />
              </label>
              <label className="inline-check">
                <input type="checkbox" checked={hasPlayableSource} onChange={(e) => { const v = e.target.checked; syncVisualToFilter({ hasPlayableSource: v }); }} />
                仅包含可播放作品
              </label>
              <label className="inline-check">
                <input type="checkbox" checked={isFavorite} onChange={(e) => { const v = e.target.checked; syncVisualToFilter({ isFavorite: v }); }} />
                仅包含我的收藏
              </label>
            </fieldset>
          </div>

          {showAdvancedJson && (
            <label className="wide-field">
              过滤器 JSON（高级自定义规则）
              <textarea className="code-input" value={form.filterJSON} onChange={(event) => updateForm("filterJSON", event.target.value)} spellCheck={false} rows={4} />
            </label>
          )}

          <label>
            排序字段
            <select value={form.sortField} onChange={(event) => updateForm("sortField", event.target.value)}>
              {Object.entries({ title: "标题", code: "编号", year: "年份", rating: "评分", releaseDate: "发行日期", updatedAt: "最近更新", createdAt: "添加时间" }).map(([value, label]) => <option value={value} key={value}>{label}</option>)}
            </select>
          </label>
          <label>
            排序方向
            <select value={form.sortDirection} onChange={(event) => updateForm("sortDirection", event.target.value)}>
              <option value="desc">降序</option>
              <option value="asc">升序</option>
            </select>
          </label>
          <label>
            海报图片 ID
            <input value={form.posterImageId} onChange={(event) => updateForm("posterImageId", event.target.value)} />
          </label>
          <label className="inline-check">
            <input type="checkbox" checked={form.isPublic} onChange={(event) => updateForm("isPublic", event.target.checked)} />
            公开媒体库（所有用户默认可查看；播放仍受播放权限限制）
          </label>
          <label className="inline-check">
            <input type="checkbox" checked={form.isAdult} onChange={(event) => updateForm("isAdult", event.target.checked)} />
            成人内容
          </label>
          <label className="inline-check">
            <input type="checkbox" checked={form.enabled} onChange={(event) => updateForm("enabled", event.target.checked)} />
            启用
          </label>
          <button className="primary-button" disabled={busy}>{busy ? "保存中…" : editing ? "保存修改" : "创建媒体库"}</button>
          </fieldset>
        </form>
      )}
      {loading ? <p className="muted">正在读取媒体库…</p> : libraries.length === 0 ? <div className="empty-card"><h3>还没有可见媒体库</h3><p className="muted">管理员可以新建媒体库或检查授权。</p></div> : <div className="management-card-grid">{libraries.map((library) => <article className="management-card" key={library.id}><div className="management-card-heading"><div><p className="eyebrow">{library.itemType}</p><h2>{library.name}</h2></div><span className={library.enabled ? "status-active" : "status-disabled"}>{library.enabled ? "启用" : "停用"}</span></div><p className="muted">{library.description || "暂无说明"}</p><div className="management-card-facts"><span>{library.isPublic ? "公开" : "需授权"}</span><span>{library.isAdult ? "成人" : "普通"}</span><span>{library.sort?.field} · {library.sort?.direction}</span>{previewCounts[library.id] != null && <strong>{previewCounts[library.id]} 部匹配作品</strong>}</div><div className="table-actions"><button type="button" className="ghost-button compact-button" onClick={() => void previewLibrary(library)}>预览数量</button>{canManage && <><button type="button" className="ghost-button compact-button" onClick={() => startEdit(library)}>编辑</button><button type="button" className="ghost-button compact-button" onClick={() => void openPermissions(library)}>授权</button><button type="button" className="ghost-button compact-button" disabled={busy} onClick={() => void removeLibrary(library)}>删除</button></>}</div></article>)}</div>}
      {permissionLibrary && <section className="dashboard-panel permission-panel"><div className="home-module-heading"><div><p className="eyebrow">{permissionLibrary.name}</p><h2>可见用户与角色</h2></div><button type="button" className="ghost-button compact-button" onClick={() => setPermissionLibrary(null)}>关闭</button></div><p className="muted">勾选查看或播放权限；公开媒体库仍会受成人内容确认和启用状态限制。</p><div className="permission-list">{permissionSubjects.map((subject) => { const key = `${subject.subjectType}:${subject.subjectId}`; const permission = permissionDraft[key] ?? { subjectType: subject.subjectType, subjectId: subject.subjectId, canView: false, canPlay: false }; return <div className="permission-row" key={key}><span><strong>{subject.name}</strong><small>{subject.detail}</small></span><label className="inline-check"><input type="checkbox" checked={permission.canView} onChange={(event) => updatePermission(subject.subjectType, subject.subjectId, "canView", event.target.checked)} />可查看</label><label className="inline-check"><input type="checkbox" checked={permission.canPlay} onChange={(event) => updatePermission(subject.subjectType, subject.subjectId, "canPlay", event.target.checked)} />可播放</label></div>; })}</div><button type="button" className="primary-button" disabled={busy} onClick={() => void savePermissions()}>{busy ? "保存中…" : "保存授权"}</button></section>}
    </section>
  );
}

