import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { apiFetch } from "../lib/api";

type TemplateModule = { id: string; name: string; status: string; templateCount: number };
export type LibraryTemplateSelection = { moduleId: string; templateId: string; revision: string };
export type ResolvedLibraryTemplate = {
  id: string; name: string; description: string; itemType: "movie" | "mixed"; isAdult: boolean;
  filter: Record<string, unknown>; sort: { field: string; direction: string };
  rankingScopes: Array<{ id: string; name: string; rankingType: string; rankingPeriod: string }>;
};
type Props = { onApply: (template: ResolvedLibraryTemplate, selection: LibraryTemplateSelection) => void; onReset: () => void };

export function LibraryTemplatePicker({ onApply, onReset }: Props) {
  const [modules, setModules] = useState<TemplateModule[]>([]);
  const [moduleId, setModuleId] = useState("");
  const [templates, setTemplates] = useState<ResolvedLibraryTemplate[]>([]);
  const [templateId, setTemplateId] = useState("");
  const [scopeId, setScopeId] = useState("");
  const [revision, setRevision] = useState("");
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [reload, setReload] = useState(0);
  const selected = templates.find((template) => template.id === templateId);

  useEffect(() => {
    let active = true;
    setLoading(true); setError("");
    void (async () => {
      try {
        if (!moduleId) {
          const result = await apiFetch<{ items: TemplateModule[] }>("/api/v1/admin/modules/library-templates");
          if (active) setModules(result.items);
        } else {
          const result = await apiFetch<{ items: ResolvedLibraryTemplate[]; revision: string }>(`/api/v1/admin/modules/${encodeURIComponent(moduleId)}/library-templates`);
          if (active) { setTemplates(result.items); setRevision(result.revision); }
        }
      } catch (e) { if (active) setError(e instanceof Error ? e.message : "读取模块模板失败"); }
      finally { if (active) setLoading(false); }
    })();
    return () => { active = false; };
  }, [moduleId, reload]);

  function apply(template: ResolvedLibraryTemplate, nextScopeId: string) {
    const scope = template.rankingScopes.find((entry) => entry.id === nextScopeId);
    onApply({ ...template,
      name: scope ? `${template.name} · ${scope.name}` : template.name,
      filter: { ...template.filter, ...(scope ? { rankingType: scope.rankingType, rankingPeriod: scope.rankingPeriod } : {}) },
    }, { moduleId, templateId: template.id, revision });
  }
  function reset() { setTemplateId(""); setScopeId(""); setTemplates([]); setRevision(""); onReset(); }

  return <section className="wide-field filter-builder-container library-template-picker" aria-label="模块虚拟库模板">
    <div className="filter-presets-header"><strong>1. 选择模块</strong><button type="button" className="ghost-button compact-button" disabled={loading} onClick={() => { reset(); setModuleId(""); setReload((value) => value + 1); }}>刷新模块列表</button></div>
    <p className="muted">模板由模块提供，只展示已添加到本地的内容。创建后可独立调整规则。</p>
    <label>来源模块<select value={moduleId} disabled={loading && !moduleId} onChange={(event) => { reset(); setModuleId(event.target.value); }}>
      <option value="">请选择模块</option>
      {modules.map((entry) => <option key={entry.id} value={entry.id} disabled={entry.status !== "active" || entry.templateCount === 0}>{entry.name}{entry.status !== "active" ? "（已停用或不可用）" : entry.templateCount === 0 ? "（暂无模板）" : ""}</option>)}
    </select></label>
    {loading && <p className="muted" role="status">正在读取{moduleId ? "模块模板" : "模块列表"}…</p>}
    {error && <div role="alert"><p className="form-error">{error}</p><button type="button" className="ghost-button compact-button" onClick={() => { reset(); setReload((value) => value + 1); }}>重新读取</button></div>}
    {!loading && !error && !moduleId && modules.every((entry) => entry.status !== "active" || entry.templateCount === 0) && <p className="muted">暂无可用的虚拟库模板，请到<Link to="/admin/modules">模块管理</Link>启用或更新模块。</p>}
    {!loading && !error && moduleId && (templates.length ? <>
      <label>2. 预设模板<select value={templateId} onChange={(event) => {
        const template = templates.find((entry) => entry.id === event.target.value);
        setTemplateId(event.target.value);
        const nextScope = template?.rankingScopes[0]?.id || "";
        setScopeId(nextScope);
        if (template) apply(template, nextScope); else onReset();
      }}><option value="">请选择模板</option>{templates.map((template) => <option key={template.id} value={template.id}>{template.name}</option>)}</select></label>
      {selected && <><p className="muted">{selected.description}</p>{selected.rankingScopes.length ? <label>3. 榜单范围<select value={scopeId} onChange={(event) => { setScopeId(event.target.value); apply(selected, event.target.value); }}>{selected.rankingScopes.map((scope) => <option key={scope.id} value={scope.id}>{scope.name}</option>)}</select></label> : <p className="muted">此模板不限榜单。</p>}</>}
    </> : <p className="muted">此模块尚未提供虚拟库模板，可在模块管理中更新清单。</p>)}
  </section>;
}
