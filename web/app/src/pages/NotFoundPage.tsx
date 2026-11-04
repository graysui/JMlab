import { Link } from "react-router-dom";

export function NotFoundPage() { return <section className="content-card"><p className="eyebrow">404</p><h1>页面不存在</h1><Link to="/" className="ghost-button">回到首页</Link></section>; }

