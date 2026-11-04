import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { apiFetch, readSession } from "../lib/api";

type ConnectionInfo = { serverUrl: string };

export function ClientConnectionPage() {
  const [connection, setConnection] = useState<ConnectionInfo | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [copyMessage, setCopyMessage] = useState("");
  const username = readSession()?.user.username ?? "";

  async function load() {
    setLoading(true);
    setError("");
    try {
      const result = await apiFetch<ConnectionInfo>("/api/v1/me/connection");
      setConnection(result);
    } catch (requestError) {
      setConnection(null);
      setError(requestError instanceof Error ? requestError.message : "无法读取连接信息");
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => { void load(); }, []);

  async function copyAddress() {
    if (!connection?.serverUrl) return;
    setCopyMessage("");
    try {
      if (!navigator.clipboard?.writeText) throw new Error("浏览器未开放剪贴板权限，请手动选择并复制地址。");
      await navigator.clipboard.writeText(connection.serverUrl);
      setCopyMessage("服务器地址已复制。");
    } catch (copyError) {
      setCopyMessage(copyError instanceof Error ? copyError.message : "复制失败，请手动选择并复制地址。");
    }
  }

  return <section className="client-connection-page">
    <div className="page-heading">
      <div><p className="eyebrow">观看入口</p><h1>连接 Jellyfin 客户端</h1><p className="muted">使用 JMlab 中的账号和媒体库权限登录兼容客户端。</p></div>
      <Link to="/browse" className="ghost-button">返回浏览</Link>
    </div>
    {loading ? <p className="muted" role="status">正在读取连接信息…</p> : error ? <div className="connection-error"><p className="form-error" role="alert">{error}</p><button className="ghost-button" type="button" onClick={() => void load()}>重新读取</button></div> : connection && <>
      <section className="connection-address" aria-labelledby="connection-address-title">
        <div><p className="eyebrow">Jellyfin 服务端地址</p><h2 id="connection-address-title">服务器地址</h2></div>
        <div className="connection-address-control">
          <label className="sr-only" htmlFor="client-server-url">Jellyfin 服务器地址</label>
          <input id="client-server-url" type="url" readOnly value={connection.serverUrl} onFocus={(event) => event.currentTarget.select()} />
          <button className="primary-button" type="button" onClick={() => void copyAddress()}>复制地址</button>
        </div>
        {copyMessage && <p className={copyMessage === "服务器地址已复制。" ? "form-success" : "form-error"} role={copyMessage === "服务器地址已复制。" ? "status" : "alert"}>{copyMessage}</p>}
      </section>
      <section className="connection-steps" aria-labelledby="connection-steps-title">
        <h2 id="connection-steps-title">登录信息</h2>
        <ol>
          <li>在客户端新增服务器，连接方式选择 <strong>Jellyfin</strong>。</li>
          <li>粘贴上方地址，并使用 JMlab 用户名 <strong>{username}</strong> 和当前账号密码登录。</li>
          <li>客户端只会显示你有权查看的媒体库；是否能播放还受媒体库播放权限和客户端格式支持影响。</li>
        </ol>
      </section>
      <p className="connection-limit">JMlab 不提供实时转码。地址可访问不代表客户端已连接成功或媒体能够解码；请在实际设备中验证。Web 端仍可用于浏览、选源和续播。</p>
    </>}
  </section>;
}
