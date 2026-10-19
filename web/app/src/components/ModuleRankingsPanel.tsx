import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { apiFetch } from "../lib/api";
export type RankingDefinition = { id: string; name: string; periods: Array<{ value: string; label: string }> };
type Target = { rankingType: string; period: string };
type Schedule = { enabled: boolean; intervalHours: number; updateTime: string; targets: Target[]; lastRunAt?: string };
export function ModuleRankingsPanel({ moduleId, enabled }: { moduleId: string; enabled: boolean }) {
  const path = `/api/v1/admin/modules/${encodeURIComponent(moduleId)}/rankings`;
  const [definitions, setDefinitions] = useState<RankingDefinition[]>([]);
  const [revision, setRevision] = useState("");
  const [schedule, setSchedule] = useState<Schedule>({ enabled: false, intervalHours: 24, updateTime: "", targets: [] });
  const [kind, setKind] = useState(""); const [period, setPeriod] = useState("");
  const [busy, setBusy] = useState(false); const [loading, setLoading] = useState(true);
  const [error, setError] = useState(""); const [message, setMessage] = useState(""); const [taskId, setTaskId] = useState("");
  async function load() {
    const result = await apiFetch<{ items: RankingDefinition[]; revision: string; schedule: Schedule }>(path);
    setDefinitions(result.items); setRevision(result.revision); setSchedule({ ...result.schedule, updateTime: result.schedule.updateTime || "" });
    setKind((current) => result.items.some((d) => d.id === current) ? current : result.items[0]?.id || "");
    setPeriod((current) => current || result.items[0]?.periods[0]?.value || "");
  }
  useEffect(() => { void load().catch((e) => setError(String(e.message || e))).finally(() => setLoading(false)); }, [moduleId]);
  const definition = definitions.find((d) => d.id === kind);
  async function execute(save: boolean) {
    setBusy(true); setError(""); setMessage("");
    try {
      if (save) { const result = await apiFetch<Schedule>(`${path}/schedule`, { method: "PUT", body: JSON.stringify(schedule) }); setSchedule(result); setMessage("自动更新计划已保存。停用模块时不会执行。"); }
      else { const result = await apiFetch<{ taskId: string }>(path, { method: "POST", body: JSON.stringify({ revision, rankingType: kind, period }) }); setTaskId(result.taskId); setMessage("任务已加入队列，完成后榜单会显示已入库内容。"); }
    } catch (e) { setError(e instanceof Error ? e.message : "操作失败"); } finally { setBusy(false); }
  }
  if (loading) return <p role="status">正在读取榜单设置…</p>;
  return <section className="dashboard-panel"><h2>榜单与自动更新</h2><p className="muted">榜单和范围由模块提供，内容经任务保存到媒体库。新计划默认关闭。</p>{error && <p className="form-error" role="alert">{error}<button className="ghost-button" onClick={() => void load().then(() => setError("")).catch((e) => setError(e.message))}>刷新设置</button></p>}{message && <p className="form-success" role="status">{message}</p>}{taskId && <Link to={`/settings/tasks?taskId=${encodeURIComponent(taskId)}`} className="ghost-button">查看任务进度</Link>}<form className="module-config-form" onSubmit={(e) => { e.preventDefault(); void execute(false); }}><fieldset disabled={busy || !enabled}><label>榜单<select required value={kind} onChange={(e) => { setKind(e.target.value); setPeriod(definitions.find((d) => d.id === e.target.value)?.periods[0]?.value || ""); }}>{definitions.map((d) => <option key={d.id} value={d.id}>{d.name}</option>)}</select></label><label>范围<select required value={period} onChange={(e) => setPeriod(e.target.value)}>{definition?.periods.map((p) => <option key={p.value} value={p.value}>{p.label}</option>)}</select></label><button className="primary-button" disabled={!kind || !period}>立即同步所选榜单</button></fieldset></form><form className="module-config-form" onSubmit={(e) => { e.preventDefault(); void execute(true); }}><h3>自动更新</h3><fieldset disabled={busy}><label className="inline-check"><input type="checkbox" checked={schedule.enabled} onChange={(e) => setSchedule({ ...schedule, enabled: e.target.checked })} />开启自动更新</label><label>执行方式<select value={schedule.updateTime ? "daily" : "interval"} onChange={(e) => setSchedule({ ...schedule, updateTime: e.target.value === "daily" ? "04:00" : "" })}><option value="interval">按时间间隔</option><option value="daily">每天固定时间</option></select></label>{schedule.updateTime ? <label>每天执行时间（服务器时区）<input type="time" required value={schedule.updateTime} onChange={(e) => setSchedule({ ...schedule, updateTime: e.target.value })} /></label> : <label>间隔小时<input type="number" min={1} max={720} required value={schedule.intervalHours} onChange={(e) => setSchedule({ ...schedule, intervalHours: Number(e.target.value) })} /></label>}<h4>需要更新的榜单范围</h4>{definitions.map((d) => <fieldset key={d.id}><legend>{d.name}</legend>{d.periods.map((p) => { const checked = schedule.targets.some((t) => t.rankingType === d.id && t.period === p.value); return <label className="inline-check" key={p.value}><input type="checkbox" checked={checked} onChange={(e) => setSchedule({ ...schedule, targets: e.target.checked ? [...schedule.targets, { rankingType: d.id, period: p.value }] : schedule.targets.filter((t) => !(t.rankingType === d.id && t.period === p.value)) })} />{p.label}</label>; })}</fieldset>)}{schedule.lastRunAt && <p className="muted">上次提交：{new Date(schedule.lastRunAt).toLocaleString()}</p>}<button className="primary-button">保存自动更新</button></fieldset></form></section>;
}
