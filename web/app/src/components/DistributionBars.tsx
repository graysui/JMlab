import { DistributionBucket } from "../lib/appShared";

export function DistributionBars({ title, buckets }: { title: string; buckets: DistributionBucket[] }) {
  const maximum = Math.max(1, ...buckets.map((bucket) => bucket.count));
  return <section className="distribution-card"><h2>{title}</h2>{buckets.length === 0 ? <p className="muted">暂无统计数据</p> : <div className="distribution-list">{buckets.map((bucket) => <div className="distribution-row" key={bucket.key}><span>{bucket.label}</span><div className="distribution-track"><span style={{ width: `${Math.round((bucket.count / maximum) * 100)}%` }} /></div><strong>{bucket.count}</strong></div>)}</div>}</section>;
}

