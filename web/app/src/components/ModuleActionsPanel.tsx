import { useState, type FormEvent } from "react";
import { apiFetch } from "../lib/api";
import { ModuleFieldInput, fieldInitial, fieldValue, type ModuleField } from "./ModuleFields";

export type ModuleAction = { id: string; name: string; description?: string; readOnly?: boolean; confirmation?: string; fields?: Record<string, ModuleField>; columns?: Record<string, string> };
type Result = { message?: string; values?: Record<string, unknown>; items?: Record<string, unknown>[] };
const display = (value: unknown) => value === undefined || value === null ? "—" : typeof value === "boolean" ? value ? "是" : "否" : typeof value === "object" ? JSON.stringify(value) : String(value);

export function ModuleActionsPanel({ moduleId, revision, actions, enabled, onUpdated }: { moduleId: string; revision: string; actions: ModuleAction[]; enabled: boolean; onUpdated: () => Promise<void> }) {
  const [selected, setSelected] = useState(actions[0]?.id || "");
  const [inputs, setInputs] = useState<Record<string, Record<string, unknown>>>({});
  const [results, setResults] = useState<Record<string, Result>>({});
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [confirm, setConfirm] = useState(false);
  const action = actions.find((value) => value.id === selected) || actions[0];
  if (!action) return null;
  const fields = Object.entries(action.fields || {});
  const valueOf = (key: string, field: ModuleField) => inputs[action.id]?.[key] ?? fieldInitial(field);
  const columns = Object.entries(action.columns || {}).filter(([key]) => !action.fields?.[key]?.secret && action.fields?.[key]?.type !== "password");
  const rows = results[action.id]?.items || (results[action.id]?.values ? [results[action.id].values!] : []);
  function chooseRow(row: Record<string, unknown>) {
    setInputs((current) => {
      const next = { ...current };
      for (const target of actions) {
        next[target.id] = { ...next[target.id] };
        for (const [key, field] of Object.entries(target.fields || {})) if (row[key] !== undefined && !field.secret && field.type !== "password") next[target.id][key] = fieldInitial(field, row[key]);
      }
      return next;
    });
    setError(""); setConfirm(false);
  }
  async function run(event: FormEvent) {
    event.preventDefault(); setError("");
    if (action.confirmation && !confirm) { setConfirm(true); return; }
    setBusy(true);
    try {
      const input = Object.fromEntries(fields.map(([key, field]) => [key, fieldValue(field, valueOf(key, field))]));
      const result = await apiFetch<Result>(`/api/v1/admin/modules/${encodeURIComponent(moduleId)}/actions/${encodeURIComponent(action.id)}`, { method: "POST", body: JSON.stringify({ revision, input }) });
      setResults((current) => ({ ...current, [action.id]: result })); await onUpdated();
    } catch (e) { setError(e instanceof SyntaxError ? "请检查 JSON 格式，列表需要使用方括号。" : e instanceof Error ? e.message : "操作失败，请刷新后重试"); }
    finally {
      setInputs((current) => ({ ...current, [action.id]: Object.fromEntries(fields.map(([key, field]) => [key, field.secret || field.type === "password" ? "" : current[action.id]?.[key] ?? fieldInitial(field)])) }));
      setBusy(false); setConfirm(false);
    }
  }
  return <section className="dashboard-panel"><h2>模块功能</h2><nav className="module-tabs" aria-label="可用模块操作">{actions.map((item) => <button type="button" className="ghost-button" aria-pressed={item.id === action.id} disabled={busy} key={item.id} onClick={() => { setSelected(item.id); setError(""); setConfirm(false); }}>{item.name}</button>)}</nav><form className="module-config-form" onSubmit={run}><h3>{action.name}</h3>{action.description && <p className="muted">{action.description}</p>}<fieldset disabled={busy || !enabled}>{fields.map(([key, field]) => <label key={key}>{field.label}<ModuleFieldInput field={field} value={valueOf(key, field)} required={field.required} onChange={(value) => { setConfirm(false); setInputs((current) => ({ ...current, [action.id]: { ...current[action.id], [key]: value } })); }} />{field.description && <small>{field.description}</small>}</label>)}{confirm && <p role="alert">{action.confirmation} 推荐先核对上方信息，再确认执行。</p>}<div className="table-actions"><button className="primary-button">{busy ? "正在处理…" : confirm ? "确认执行" : action.name}</button><button type="button" className="ghost-button" onClick={() => { setInputs((current) => ({ ...current, [action.id]: {} })); setConfirm(false); }}>清空填写</button></div></fieldset></form>{error && <p className="form-error" role="alert">{error}</p>}{results[action.id]?.message && <p className="form-success" role="status">{results[action.id].message}</p>}{rows.length > 0 && columns.length > 0 && <div className="table-wrap"><table className="management-table"><thead><tr>{columns.map(([key, label]) => <th key={key}>{label}</th>)}<th>填写操作</th></tr></thead><tbody>{rows.map((row, index) => <tr key={index}>{columns.map(([key]) => <td key={key}>{display(row[key])}</td>)}<td><button type="button" className="ghost-button" disabled={busy} onClick={() => chooseRow(row)}>选择此项</button></td></tr>)}</tbody></table><p className="muted">选择后，在上方切换到需要的操作，核对后执行。</p></div>}</section>;
}
