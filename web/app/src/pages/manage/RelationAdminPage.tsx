import { apiFetch } from "../../lib/api";
import { FormEvent, useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { RelationEntity, RelationKind, relationLabels } from "../../lib/appShared";

export type RelationAdminForm = { name: string; aliases: string; sourceName: string; sourceUrl: string; gender: string; status: string; birthday: string; heightCm: string; cupSize: string; bust: string; waist: string; hip: string; biography: string; parentId: string; tagKind: string; isExcluded: boolean };

export const emptyRelationAdminForm: RelationAdminForm = { name: "", aliases: "", sourceName: "", sourceUrl: "", gender: "", status: "unknown", birthday: "", heightCm: "", cupSize: "", bust: "", waist: "", hip: "", biography: "", parentId: "", tagKind: "custom", isExcluded: false };

export function relationAdminForm(entity: RelationEntity): RelationAdminForm {
  return { name: entity.name, aliases: entity.aliases?.join(", ") ?? "", sourceName: entity.sourceName ?? "", sourceUrl: entity.sourceUrl ?? "", gender: entity.gender ?? "", status: entity.status ?? "unknown", birthday: entity.birthday ?? "", heightCm: entity.heightCm?.toString() ?? "", cupSize: entity.cupSize ?? "", bust: entity.measurements?.bust ?? "", waist: entity.measurements?.waist ?? "", hip: entity.measurements?.hip ?? "", biography: entity.biography ?? "", parentId: entity.parentId ?? "", tagKind: entity.tagKind ?? "custom", isExcluded: entity.isExcluded };
}

export function RelationAdminPage() {
  const [kind, setKind] = useState<RelationKind>("people");
  const [query, setQuery] = useState("");
  const [submittedQuery, setSubmittedQuery] = useState("");
  const [includeExcluded, setIncludeExcluded] = useState(true);
  const [entities, setEntities] = useState<RelationEntity[]>([]);
  const [selected, setSelected] = useState<string[]>([]);
  const [mergeTargetId, setMergeTargetId] = useState("");
  const [editing, setEditing] = useState<RelationEntity | null>(null);
  const [editorOpen, setEditorOpen] = useState(false);
  const [form, setForm] = useState<RelationAdminForm>(emptyRelationAdminForm);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const label = relationLabels[kind];

  async function loadEntities() {
    setLoading(true);
    setError("");
    try {
      const suffix = `?limit=200&includeExcluded=${includeExcluded ? "1" : "0"}${submittedQuery ? `&q=${encodeURIComponent(submittedQuery)}` : ""}`;
      const result = await apiFetch<{ items: RelationEntity[] }>(`/api/v1/${kind}${suffix}`);
      setEntities(result.items);
      setSelected([]);
      setMergeTargetId("");
    } catch (requestError) {
      setError(requestError instanceof Error ? requestError.message : `读取${label}失败`);
    } finally { setLoading(false); }
  }
  useEffect(() => { void loadEntities(); }, [kind, includeExcluded, submittedQuery]);
  function chooseKind(next: RelationKind) { setKind(next); setEditing(null); setEditorOpen(false); setForm(emptyRelationAdminForm); }
  function toggleSelected(id: string) { setSelected((current) => current.includes(id) ? current.filter((value) => value !== id) : [...current, id]); }
  function toggleAll() { setSelected(selected.length === entities.length ? [] : entities.map((entity) => entity.id)); }
  function startEdit(entity: RelationEntity) { setEditing(entity); setEditorOpen(true); setForm(relationAdminForm(entity)); }
  function startCreate() { setEditing(null); setEditorOpen(true); setForm(emptyRelationAdminForm); }
  function updateForm<K extends keyof RelationAdminForm>(key: K, value: RelationAdminForm[K]) { setForm((current) => ({ ...current, [key]: value })); }
  async function saveEntity(event: FormEvent) {
    event.preventDefault();
    if (!form.name.trim()) { setError("名称不能为空"); return; }
    setBusy(true); setError("");
    const payload: Record<string, unknown> = { name: form.name.trim(), aliases: form.aliases.split(",").map((value) => value.trim()).filter(Boolean), isExcluded: form.isExcluded };
    if (kind === "people") Object.assign(payload, { gender: form.gender, status: form.status, birthday: form.birthday, heightCm: form.heightCm ? Number(form.heightCm) : null, cupSize: form.cupSize, measurements: Object.fromEntries([["bust", form.bust], ["waist", form.waist], ["hip", form.hip]].filter(([, value]) => value)), biography: form.biography, sourceUrl: form.sourceUrl });
    if (kind === "tags") Object.assign(payload, { sourceName: form.sourceName, tagKind: form.tagKind, parentId: form.parentId, clearParent: !form.parentId });
    if (kind === "studios" || kind === "series") Object.assign(payload, { sourceName: form.sourceName });
    try {
      await apiFetch<RelationEntity>(editing ? `/api/v1/${kind}/${editing.id}` : `/api/v1/${kind}`, { method: editing ? "PATCH" : "POST", body: JSON.stringify(payload) });
      setEditing(null); setEditorOpen(false); setForm(emptyRelationAdminForm); await loadEntities();
    } catch (requestError) { setError(requestError instanceof Error ? requestError.message : "保存关系实体失败"); } finally { setBusy(false); }
  }
  async function batch(action: "exclude" | "include") {
    if (selected.length === 0) return;
    setBusy(true); setError("");
    try { await apiFetch(`/api/v1/${kind}/batch`, { method: "POST", body: JSON.stringify({ ids: selected, action }) }); await loadEntities(); } catch (requestError) { setError(requestError instanceof Error ? requestError.message : "批量更新失败"); } finally { setBusy(false); }
  }
  async function mergeEntity() {
    if (selected.length !== 1 || !mergeTargetId) return;
    setBusy(true); setError("");
    try { await apiFetch(`/api/v1/${kind}/${selected[0]}/merge`, { method: "POST", body: JSON.stringify({ targetId: mergeTargetId }) }); await loadEntities(); } catch (requestError) { setError(requestError instanceof Error ? requestError.message : "合并关系实体失败"); } finally { setBusy(false); }
  }
  const canMerge = selected.length === 1 && Boolean(mergeTargetId);
  return <section className="content-card relation-admin-page"><div className="relation-page-heading"><div><p className="eyebrow">人物与分类</p><h1>编辑、排除与合并</h1></div><Link to="/manage" className="ghost-button">返回设置</Link></div><div className="relation-admin-tabs">{(Object.keys(relationLabels) as RelationKind[]).map((value) => <button type="button" className={kind === value ? "active" : ""} onClick={() => chooseKind(value)} key={value}>{relationLabels[value]}</button>)}</div><div className="admin-toolbar"><form className="search-row" onSubmit={(event) => { event.preventDefault(); setSubmittedQuery(query.trim()); }}><input aria-label={`搜索${label}`} placeholder={`搜索${label}`} value={query} onChange={(event) => setQuery(event.target.value)} /><button className="primary-button" type="submit">搜索</button></form><label className="inline-check"><input type="checkbox" checked={includeExcluded} onChange={(event) => setIncludeExcluded(event.target.checked)} />显示已排除</label><button type="button" className="primary-button" onClick={startCreate}>新建{label}</button></div>{error && <p className="form-error" role="alert">{error}</p>}<div className="batch-toolbar"><span className="muted">已选 {selected.length} 项</span><button type="button" className="ghost-button" disabled={busy || selected.length === 0} onClick={() => batch("exclude")}>批量排除</button><button type="button" className="ghost-button" disabled={busy || selected.length === 0} onClick={() => batch("include")}>批量启用</button>{selected.length === 1 && <><select aria-label="合并目标" value={mergeTargetId} onChange={(event) => setMergeTargetId(event.target.value)}><option value="">选择合并目标</option>{entities.filter((entity) => entity.id !== selected[0]).map((entity) => <option value={entity.id} key={entity.id}>{entity.name}</option>)}</select><button type="button" className="ghost-button" disabled={busy || !canMerge} onClick={mergeEntity}>合并</button></>}</div>{loading ? <p className="muted">正在读取{label}…</p> : <div className="table-wrap"><table className="relation-admin-table"><thead><tr><th><input type="checkbox" aria-label="全选" checked={entities.length > 0 && selected.length === entities.length} onChange={toggleAll} /></th><th>名称</th><th>来源</th><th>状态</th><th>操作</th></tr></thead><tbody>{entities.map((entity) => <tr key={entity.id} className={entity.isExcluded ? "is-excluded" : ""}><td><input type="checkbox" checked={selected.includes(entity.id)} onChange={() => toggleSelected(entity.id)} /></td><td><strong>{entity.name}</strong>{entity.aliases && entity.aliases.length > 0 && <small>{entity.aliases.join(" · ")}</small>}</td><td>{entity.sourceName || entity.sourceUrl || "手工"}</td><td>{entity.isExcluded ? <span className="status-disabled">已排除</span> : <span className="status-active">启用</span>}</td><td><button type="button" className="ghost-button compact-button" onClick={() => startEdit(entity)}>编辑</button></td></tr>)}</tbody></table></div>}{editorOpen && <form className="relation-editor" onSubmit={saveEntity}><div className="relation-editor-heading"><div><p className="eyebrow">{editing ? "编辑关系实体" : `新建${label}`}</p><h2>{editing ? editing.name : `补录${label}`}</h2></div><button type="button" className="ghost-button" onClick={() => { setEditing(null); setEditorOpen(false); setForm(emptyRelationAdminForm); }}>关闭</button></div><label>名称<input required value={form.name} onChange={(event) => updateForm("name", event.target.value)} /></label><label>别名（逗号分隔）<input value={form.aliases} onChange={(event) => updateForm("aliases", event.target.value)} /></label>{(kind === "studios" || kind === "series" || kind === "tags") && <label>来源名称<input value={form.sourceName} onChange={(event) => updateForm("sourceName", event.target.value)} /></label>}{kind === "people" && <><label>性别<input value={form.gender} onChange={(event) => updateForm("gender", event.target.value)} /></label><label>状态<select value={form.status} onChange={(event) => updateForm("status", event.target.value)}><option value="active">活跃</option><option value="retired">隐退</option><option value="unknown">未知</option></select></label><label>生日<input type="date" value={form.birthday} onChange={(event) => updateForm("birthday", event.target.value)} /></label><label>身高（cm）<input type="number" min="0" max="300" value={form.heightCm} onChange={(event) => updateForm("heightCm", event.target.value)} /></label><label>罩杯<input value={form.cupSize} onChange={(event) => updateForm("cupSize", event.target.value)} /></label><label>胸围<input value={form.bust} onChange={(event) => updateForm("bust", event.target.value)} /></label><label>腰围<input value={form.waist} onChange={(event) => updateForm("waist", event.target.value)} /></label><label>臀围<input value={form.hip} onChange={(event) => updateForm("hip", event.target.value)} /></label><label>来源链接<input type="url" value={form.sourceUrl} onChange={(event) => updateForm("sourceUrl", event.target.value)} /></label><label>简介<textarea value={form.biography} onChange={(event) => updateForm("biography", event.target.value)} /></label></>}{kind === "tags" && <><label>标签类型<select value={form.tagKind} onChange={(event) => updateForm("tagKind", event.target.value)}><option value="genre">类别</option><option value="theme">主题</option><option value="source">来源</option><option value="custom">自定义</option></select></label><label>父级标签 ID<input value={form.parentId} onChange={(event) => updateForm("parentId", event.target.value)} /></label></>}<label className="inline-check"><input type="checkbox" checked={form.isExcluded} onChange={(event) => updateForm("isExcluded", event.target.checked)} />排除此实体</label><button className="primary-button" disabled={busy}>{busy ? "保存中…" : "保存"}</button></form>}</section>;
}

