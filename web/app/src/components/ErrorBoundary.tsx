import { Component, type ErrorInfo, type ReactNode } from "react";

export class ErrorBoundary extends Component<{ children: ReactNode }, { hasError: boolean; error: Error | null }> {
  constructor(props: { children: ReactNode }) {
    super(props);
    this.state = { hasError: false, error: null };
  }
  static getDerivedStateFromError(error: Error) {
    return { hasError: true, error };
  }
  componentDidCatch(error: Error, errorInfo: ErrorInfo) {
    console.error("ErrorBoundary caught error:", error, errorInfo);
  }
  render() {
    if (this.state.hasError) {
      return (
        <div style={{ padding: "60px 20px", maxWidth: 600, margin: "60px auto", textAlign: "center", color: "#f3f4f6" }}>
          <h2>页面发生意外错误</h2>
          <p style={{ color: "#9ca3af", margin: "16px 0" }}>{this.state.error?.message || "请尝试清除本地会话缓存并重新加载"}</p>
          <button
            type="button"
            className="primary-button"
            onClick={() => {
              sessionStorage.clear();
              localStorage.clear();
              window.location.reload();
            }}
          >
            清除本地会话并刷新
          </button>
        </div>
      );
    }
    return this.props.children;
  }
}

