import { apiFetch, readSession, type User } from "../../lib/api";
import { FormEvent, useCallback, useEffect, useState } from "react";
import { Link } from "react-router-dom";

export function UserAdminPage() {
  const session = readSession();
  const [users, setUsers] = useState<User[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");
  const [busyUserId, setBusyUserId] = useState<string | null>(null);

  // New user form state
  const [showCreateForm, setShowCreateForm] = useState(false);
  const [username, setUsername] = useState("");
  const [displayName, setDisplayName] = useState("");
  const [password, setPassword] = useState("");
  const [role, setRole] = useState<"Viewer" | "Editor" | "Admin">("Viewer");
  const [creating, setCreating] = useState(false);
  const [createError, setCreateError] = useState("");

  // Reset password modal state
  const [resetUser, setResetUser] = useState<User | null>(null);
  const [newPassword, setNewPassword] = useState("");
  const [resetting, setResetting] = useState(false);
  const [resetError, setResetError] = useState("");

  // Delete user modal state
  const [deleteCandidate, setDeleteCandidate] = useState<User | null>(null);
  const [deleting, setDeleting] = useState(false);
  const [deleteError, setDeleteError] = useState("");

  const loadUsers = useCallback(() => {
    setLoading(true);
    apiFetch<{ items: User[] }>("/api/v1/admin/users")
      .then((result) => {
        setUsers(result.items);
        setError("");
      })
      .catch((requestError) => setError(requestError instanceof Error ? requestError.message : "读取用户失败"))
      .finally(() => setLoading(false));
  }, []);

  useEffect(() => {
    loadUsers();
  }, [loadUsers]);

  async function handleCreateUser(e: FormEvent) {
    e.preventDefault();
    setCreateError("");
    if (!username.trim()) {
      setCreateError("用户名不能为空");
      return;
    }
    if (password.length < 6) {
      setCreateError("密码长度至少需要 6 个字符");
      return;
    }
    setCreating(true);
    try {
      await apiFetch("/api/v1/admin/users", {
        method: "POST",
        body: JSON.stringify({
          username: username.trim(),
          displayName: displayName.trim() || username.trim(),
          password,
          role,
        }),
      });
      setMessage(`用户 ${username} 创建成功`);
      setUsername("");
      setDisplayName("");
      setPassword("");
      setRole("Viewer");
      setShowCreateForm(false);
      loadUsers();
    } catch (requestError) {
      setCreateError(requestError instanceof Error ? requestError.message : "创建用户失败");
    } finally {
      setCreating(false);
    }
  }

  async function toggleStatus(u: User) {
    setBusyUserId(u.id);
    setMessage("");
    setError("");
    const nextStatus = u.status === "active" ? "disabled" : "active";
    try {
      await apiFetch(`/api/v1/admin/users/${encodeURIComponent(u.id)}`, {
        method: "PATCH",
        body: JSON.stringify({ status: nextStatus }),
      });
      setMessage(`用户 ${u.username} 已${nextStatus === "active" ? "启用" : "禁用"}`);
      loadUsers();
    } catch (requestError) {
      setError(requestError instanceof Error ? requestError.message : "更新状态失败");
    } finally {
      setBusyUserId(null);
    }
  }

  async function changeRole(u: User, nextRole: string) {
    setBusyUserId(u.id);
    setMessage("");
    setError("");
    try {
      await apiFetch(`/api/v1/admin/users/${encodeURIComponent(u.id)}`, {
        method: "PATCH",
        body: JSON.stringify({ role: nextRole }),
      });
      setMessage(`用户 ${u.username} 角色已修改为 ${nextRole}`);
      loadUsers();
    } catch (requestError) {
      setError(requestError instanceof Error ? requestError.message : "更新角色失败");
    } finally {
      setBusyUserId(null);
    }
  }

  async function handleResetPassword(e: FormEvent) {
    e.preventDefault();
    if (!resetUser) return;
    setResetError("");
    if (newPassword.length < 6) {
      setResetError("新密码至少需要 6 个字符");
      return;
    }
    setResetting(true);
    try {
      await apiFetch(`/api/v1/admin/users/${encodeURIComponent(resetUser.id)}/reset-password`, {
        method: "POST",
        body: JSON.stringify({ password: newPassword }),
      });
      setMessage(`用户 ${resetUser.username} 的密码重置成功`);
      setResetUser(null);
      setNewPassword("");
    } catch (requestError) {
      setResetError(requestError instanceof Error ? requestError.message : "重置密码失败");
    } finally {
      setResetting(false);
    }
  }

  async function revokeSessions(u: User) {
    setBusyUserId(u.id);
    setMessage("");
    setError("");
    try {
      await apiFetch(`/api/v1/admin/users/${encodeURIComponent(u.id)}/revoke-sessions`, {
        method: "POST",
      });
      setMessage(`用户 ${u.username} 的所有会话已撤销`);
    } catch (requestError) {
      setError(requestError instanceof Error ? requestError.message : "撤销会话失败");
    } finally {
      setBusyUserId(null);
    }
  }

  async function handleDeleteUser() {
    if (!deleteCandidate) return;
    setDeleting(true);
    setDeleteError("");
    try {
      await apiFetch(`/api/v1/admin/users/${encodeURIComponent(deleteCandidate.id)}`, {
        method: "DELETE",
      });
      setMessage(`用户 ${deleteCandidate.username} 已成功删除`);
      setDeleteCandidate(null);
      loadUsers();
    } catch (requestError) {
      setDeleteError(requestError instanceof Error ? requestError.message : "删除用户失败");
    } finally {
      setDeleting(false);
    }
  }

  return (
    <section className="content-card">
      <div className="user-admin-header">
        <div>
          <p className="eyebrow">用户与权限</p>
          <h1>用户管理</h1>
          <p className="muted">管理本地账号、角色授权与会话状态。</p>
        </div>
        <div style={{ display: "flex", gap: "0.6rem" }}>
          <button
            type="button"
            className="primary-button"
            onClick={() => { setShowCreateForm(!showCreateForm); setCreateError(""); }}
          >
            {showCreateForm ? "收起表单" : "＋ 新建用户"}
          </button>
          <Link to="/manage" className="ghost-button">返回设置</Link>
        </div>
      </div>

      {message && <p className="form-success" role="status">{message}</p>}
      {error && <p className="form-error" role="alert">{error}</p>}

      {showCreateForm && (
        <div className="user-create-card">
          <h2>新建用户</h2>
          <p className="muted">创建新用户后可在权限和媒体库管理中分配访问范围。</p>
          {createError && <p className="form-error" role="alert">{createError}</p>}
          <form onSubmit={handleCreateUser}>
            <div className="user-create-grid">
              <label>
                用户名（必填）
                <input
                  required
                  placeholder="例如: viewer01"
                  value={username}
                  onChange={(e) => setUsername(e.target.value)}
                />
              </label>
              <label>
                显示名称
                <input
                  placeholder="留空默认同用户名"
                  value={displayName}
                  onChange={(e) => setDisplayName(e.target.value)}
                />
              </label>
              <label>
                初始密码（至少 6 位）
                <input
                  type="password"
                  required
                  placeholder="最少 6 个字符"
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                />
              </label>
              <label>
                角色
                <select
                  value={role}
                  onChange={(e) => setRole(e.target.value as "Viewer" | "Editor" | "Admin")}
                >
                  <option value="Viewer">Viewer (访客)</option>
                  <option value="Editor">Editor (编辑者)</option>
                  <option value="Admin">Admin (管理员)</option>
                </select>
              </label>
              <div style={{ display: "flex", gap: "0.6rem" }}>
                <button className="primary-button" disabled={creating} type="submit">
                  {creating ? "创建中…" : "确认创建"}
                </button>
                <button
                  type="button"
                  className="ghost-button"
                  onClick={() => setShowCreateForm(false)}
                >
                  取消
                </button>
              </div>
            </div>
          </form>
        </div>
      )}

      {loading ? (
        <p className="muted">正在加载用户列表…</p>
      ) : (
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>用户名</th>
                <th>显示名称</th>
                <th>角色</th>
                <th>状态</th>
                <th>操作</th>
              </tr>
            </thead>
            <tbody>
              {users.map((user) => {
                const currentRole = (user.roles && user.roles[0]) || (user.isAdmin ? "Admin" : "Viewer");
                const isBusy = busyUserId === user.id;

                return (
                  <tr key={user.id}>
                    <td><strong>{user.username}</strong></td>
                    <td>{user.displayName}</td>
                    <td>
                      <select
                        value={currentRole}
                        disabled={isBusy}
                        style={{ padding: "0.3rem 0.5rem", borderRadius: "6px", background: "var(--input)", color: "var(--text)", border: "1px solid var(--line)" }}
                        onChange={(e) => void changeRole(user, e.target.value)}
                      >
                        <option value="Viewer">Viewer</option>
                        <option value="Editor">Editor</option>
                        <option value="Admin">Admin</option>
                      </select>
                    </td>
                    <td>
                      <span className={user.status === "active" ? "status-active" : "status-disabled"}>
                        {user.status === "active" ? "启用" : "已禁用"}
                      </span>
                    </td>
                    <td>
                      <div className="user-action-buttons">
                        <button
                          type="button"
                          className="ghost-button compact-button"
                          disabled={isBusy}
                          onClick={() => void toggleStatus(user)}
                        >
                          {user.status === "active" ? "禁用" : "启用"}
                        </button>
                        <button
                          type="button"
                          className="ghost-button compact-button"
                          disabled={isBusy}
                          onClick={() => { setResetUser(user); setNewPassword(""); setResetError(""); }}
                        >
                          重置密码
                        </button>
                        <button
                          type="button"
                          className="ghost-button compact-button"
                          disabled={isBusy}
                          onClick={() => void revokeSessions(user)}
                        >
                          下线会话
                        </button>
                        <button
                          type="button"
                          className="ghost-button compact-button"
                          style={{ color: "#ff6b6b", borderColor: "rgba(255, 107, 107, 0.4)" }}
                          disabled={isBusy || user.id === session?.user.id}
                          onClick={() => { setDeleteCandidate(user); setDeleteError(""); }}
                          title={user.id === session?.user.id ? "不能删除当前登录账号" : "删除用户"}
                        >
                          删除
                        </button>
                      </div>
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}

      {resetUser && (
        <div className="user-modal-overlay" onClick={() => setResetUser(null)}>
          <div className="user-modal" onClick={(e) => e.stopPropagation()}>
            <h2>重置密码</h2>
            <p className="muted">正在为用户 <strong>{resetUser.username}</strong> 设置新密码，密码至少需要 12 个字符。</p>
            {resetError && <p className="form-error" role="alert">{resetError}</p>}
            <form onSubmit={handleResetPassword}>
              <label>
                新密码
                <input
                  type="password"
                  required
                  placeholder="至少 12 个字符"
                  value={newPassword}
                  onChange={(e) => setNewPassword(e.target.value)}
                  autoFocus
                />
              </label>
              <div className="user-modal-actions">
                <button type="button" className="ghost-button" onClick={() => setResetUser(null)}>
                  取消
                </button>
                <button type="submit" className="primary-button" disabled={resetting}>
                  {resetting ? "保存中…" : "确认重置"}
                </button>
              </div>
            </form>
          </div>
        </div>
      )}

      {deleteCandidate && (
        <div className="user-modal-overlay" onClick={() => setDeleteCandidate(null)}>
          <div className="user-modal" onClick={(e) => e.stopPropagation()}>
            <h2 style={{ color: "#ff6b6b" }}>确认删除用户</h2>
            <p className="muted">
              确定要删除用户 <strong>{deleteCandidate.username}</strong> ({deleteCandidate.displayName}) 吗？
            </p>
            <p style={{ fontSize: "0.88rem", color: "var(--muted)", margin: "0.5rem 0 1rem" }}>
              此操作将清除其所有播放记录、收藏、自定义偏好以及媒体库访问授权，且无法恢复。
            </p>
            {deleteError && <p className="form-error" role="alert">{deleteError}</p>}
            <div className="user-modal-actions">
              <button
                type="button"
                className="ghost-button"
                disabled={deleting}
                onClick={() => setDeleteCandidate(null)}
              >
                取消
              </button>
              <button
                type="button"
                className="primary-button"
                style={{ backgroundColor: "#ff5252", borderColor: "transparent" }}
                disabled={deleting}
                onClick={() => void handleDeleteUser()}
              >
                {deleting ? "删除中…" : "确认删除"}
              </button>
            </div>
          </div>
        </div>
      )}
    </section>
  );
}

