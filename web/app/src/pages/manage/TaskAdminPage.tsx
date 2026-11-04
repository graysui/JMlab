import { apiFetch } from "../../lib/api";
import { FormEvent, useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { TaskRecord, BackupRunRecord, taskStatusLabels } from "../../lib/appShared";

export function TaskAdminPage() {
  const [tasks, setTasks] = useState<TaskRecord[]>([]);
  const [total, setTotal] = useState(0);
  const [status, setStatus] = useState("");
  const [type, setType] = useState("");
  const [limit, setLimit] = useState(20);
  const [offset, setOffset] = useState(0);
  const [selectedTask, setSelectedTask] = useState<TaskRecord | null>(null);
  const [backupRun, setBackupRun] = useState<BackupRunRecord | null>(null);
  const [backupRunError, setBackupRunError] = useState("");
  const [loading, setLoading] = useState(true);
  const [detailLoading, setDetailLoading] = useState(false);
  const [busyTask, setBusyTask] = useState("");
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");

  async function loadTasks() {
    setLoading(true);
    try {
      const params = new URLSearchParams({ limit: String(limit), offset: String(offset) });
      if (status) params.set("status", status);
      if (type.trim()) params.set("type", type.trim());
      const result = await apiFetch<{ items: TaskRecord[]; total: number }>(`/api/v1/tasks?${params.toString()}`);
      setTasks(result.items);
      setTotal(result.total);
      setError("");
    } catch (requestError) {
      setError(requestError instanceof Error ? requestError.message : "读取任务失败");
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    void loadTasks();
    const timer = window.setInterval(() => { void loadTasks(); }, 5000);
    return () => window.clearInterval(timer);
  }, [status, type, limit, offset]);

  async function cancelTask(taskID: string) {
    setBusyTask(taskID);
    setMessage("");
    try {
      await apiFetch<TaskRecord>(`/api/v1/tasks/${taskID}/cancel`, { method: "POST" });
      setMessage("任务已取消");
      await loadTasks();
    } catch (requestError) {
      setError(requestError instanceof Error ? requestError.message : "取消任务失败");
    } finally {
      setBusyTask("");
    }
  }

  async function openTask(taskID: string) {
    setDetailLoading(true); setError("");
    setBackupRun(null); setBackupRunError("");
    try {
      const task = await apiFetch<TaskRecord>(`/api/v1/tasks/${taskID}`);
      setSelectedTask(task);
      if (task.type === "backup") {
        try { setBackupRun(await apiFetch<BackupRunRecord>(`/api/v1/backup/runs/${encodeURIComponent(taskID)}`)); }
        catch (requestError) { setBackupRunError(requestError instanceof Error ? requestError.message : "读取备份目标结果失败"); }
      }
    }
    catch (requestError) { setError(requestError instanceof Error ? requestError.message : "读取任务详情失败"); }
    finally { setDetailLoading(false); }
  }

  async function retryTask(taskID: string) {
    setBusyTask(taskID); setMessage(""); setError("");
    try { const task = await apiFetch<TaskRecord>(`/api/v1/tasks/${taskID}/retry`, { method: "POST" }); setSelectedTask(task); setBackupRun(null); setBackupRunError(""); setMessage("任务已重新排队"); await loadTasks(); }
    catch (requestError) { setError(requestError instanceof Error ? requestError.message : "重试任务失败"); }
    finally { setBusyTask(""); }
  }

  function resetFilters(event: FormEvent) { event.preventDefault(); setStatus(""); setType(""); setOffset(0); }
  const page = Math.floor(offset / limit) + 1; const pages = Math.max(1, Math.ceil(total / limit));
  return <section className="content-card task-admin-page"><div className="relation-page-heading"><div><p className="eyebrow">后台任务</p><h1>同步任务中心</h1><p className="muted">任务会自动刷新；失败、部分成功和取消任务可以从详情页重新排队。</p></div><Link to="/manage" className="ghost-button">返回设置</Link></div><form className="admin-filter-bar" onSubmit={resetFilters}><label>状态<select value={status} onChange={(event) => { setStatus(event.target.value); setOffset(0); }}><option value="">全部状态</option>{Object.entries(taskStatusLabels).map(([key, label]) => <option key={key} value={key}>{label}</option>)}</select></label><label>任务类型<input placeholder="sync_item" value={type} onChange={(event) => setType(event.target.value)} /></label><label>每页<select value={limit} onChange={(event) => { setLimit(Number(event.target.value)); setOffset(0); }}><option value={20}>20</option><option value={50}>50</option><option value={100}>100</option></select></label><button className="ghost-button compact-button">应用筛选</button></form>{error && <p className="form-error" role="alert">{error}</p>}{message && <p className="form-success" role="status">{message}</p>}{loading ? <p className="muted">正在读取任务…</p> : tasks.length === 0 ? <div className="empty-card"><h3>暂无匹配任务</h3><p className="muted">调整筛选条件或从作品详情发起同步。</p></div> : <><div className="table-wrap"><table className="task-table"><thead><tr><th>任务</th><th>状态</th><th>进度</th><th>重试</th><th>错误</th><th>更新时间</th><th>操作</th></tr></thead><tbody>{tasks.map((task) => <tr key={task.id}><td><button type="button" className="table-link" onClick={() => void openTask(task.id)}><strong>{task.type === "backup" ? "备份" : task.type}</strong><small>{task.id}</small></button></td><td><span className={`task-status task-status-${task.status}`}>{taskStatusLabels[task.status] ?? task.status}</span></td><td><div className="task-progress"><span style={{ width: `${task.progress}%` }} /></div><small>{task.progress}%</small></td><td>{task.retryCount}/{task.maxRetries}</td><td>{task.errorMessage ? <span title={task.errorCode}>{task.errorMessage}</span> : <span className="muted">—</span>}</td><td>{new Date(task.updatedAt).toLocaleString()}</td><td><div className="table-actions">{task.canCancel && <button type="button" className="ghost-button compact-button" disabled={busyTask === task.id} onClick={() => void cancelTask(task.id)}>{busyTask === task.id ? "取消中…" : "取消"}</button>}{task.canRetry && <button type="button" className="ghost-button compact-button" disabled={busyTask === task.id} onClick={() => void retryTask(task.id)}>重试</button>}</div></td></tr>)}</tbody></table></div><div className="pagination-bar"><span className="muted">共 {total} 条 · 第 {page}/{pages} 页</span><div className="table-actions"><button type="button" className="ghost-button compact-button" disabled={offset === 0} onClick={() => setOffset(Math.max(0, offset - limit))}>上一页</button><button type="button" className="ghost-button compact-button" disabled={offset + limit >= total} onClick={() => setOffset(offset + limit)}>下一页</button></div></div></>}{selectedTask && <section className="dashboard-panel task-detail-panel"><div className="home-module-heading"><div><p className="eyebrow">任务详情</p><h2>{selectedTask.type === "backup" ? "备份" : selectedTask.type}</h2><p className="muted">{selectedTask.id} · {taskStatusLabels[selectedTask.status] ?? selectedTask.status}</p></div><button type="button" className="ghost-button compact-button" onClick={() => { setSelectedTask(null); setBackupRun(null); setBackupRunError(""); }}>关闭</button></div>{detailLoading ? <p className="muted">正在读取 attempt 历史…</p> : <><div className="dashboard-error-counts"><span>进度<strong>{selectedTask.progress}%</strong></span><span>重试<strong>{selectedTask.retryCount}/{selectedTask.maxRetries}</strong></span></div>{selectedTask.errorMessage && <p className="form-error">{selectedTask.errorCode ? `${selectedTask.errorCode}：` : ""}{selectedTask.errorMessage}</p>}{selectedTask.type === "backup" && <section className="dashboard-panel"><div className="home-module-heading"><div><p className="eyebrow">备份目标结果</p><h3>{backupRun?.artifactName || "备份归档"}</h3></div><span className={`task-status task-status-${backupRun?.taskStatus || selectedTask.status}`}>{taskStatusLabels[backupRun?.taskStatus || selectedTask.status] ?? backupRun?.taskStatus}</span></div>{backupRunError && <p className="form-error" role="alert">{backupRunError}</p>}{backupRun && <><p className="muted">归档 {backupRun.artifactSize ?? 0} 字节 · SHA256 {backupRun.artifactSha256 || "生成中"}{backupRun.includeUserState ? " · 含用户状态" : ""}{backupRun.includeEncryptedConfig ? " · 含加密目标配置" : ""}</p><div className="table-wrap"><table className="management-table"><thead><tr><th>目标</th><th>类型</th><th>状态</th><th>归档路径</th><th>写入大小</th><th>结果</th></tr></thead><tbody>{backupRun.targets.map((target) => <tr key={target.targetId}><td>{target.displayName}<small>{target.targetId}</small></td><td>{target.targetType === "local" ? "本地" : target.targetType === "webdav" ? "WebDAV" : "Git"}</td><td><span className={`task-status task-status-${target.status}`}>{taskStatusLabels[target.status] ?? target.status}</span></td><td><code>{target.artifactPath || "—"}</code></td><td>{target.bytesWritten} 字节</td><td>{target.errorMessage ? <span className="form-error">{target.errorMessage}</span> : target.checksum ? <small>SHA256 {target.checksum}</small> : <span className="muted">—</span>}</td></tr>)}</tbody></table></div></>}</section>}<div className="attempt-list">{selectedTask.attempts?.length ? selectedTask.attempts.map((attempt) => <article className="attempt-card" key={attempt.id}><div><strong>Attempt #{attempt.attemptNumber}</strong><small>{taskStatusLabels[attempt.status] ?? attempt.status} · {new Date(attempt.startedAt).toLocaleString()}</small></div><span>{attempt.errorCode || "—"}</span>{attempt.errorMessage && <p className="muted">{attempt.errorMessage}</p>}</article>) : <p className="muted">暂无 attempt 历史</p>}</div>{selectedTask.canRetry && <button type="button" className="primary-button" disabled={busyTask === selectedTask.id} onClick={() => void retryTask(selectedTask.id)}>重新排队</button>}</>}</section>}</section>;
}

