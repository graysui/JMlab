import type { ReactNode } from "react";

const shapes = {
  home: <><path d="m3 10 9-7 9 7v10a1 1 0 0 1-1 1h-5v-7H9v7H4a1 1 0 0 1-1-1Z" /></>,
  search: <><circle cx="10.5" cy="10.5" r="6.5" /><path d="m16 16 5 5" /></>,
  ranking: <><path d="M4 21V11h5v10m1 0V4h5v17m1 0v-7h5v7" /></>,
  library: <><rect x="3" y="4" width="5" height="16" rx="1" /><rect x="8" y="4" width="5" height="16" rx="1" /><path d="m15 5 4-1 3 15-4 1Z" /></>,
  list: <><path d="M9 6h12M9 12h12M9 18h12m-18-12 1 1 2-2m-3 7 1 1 2-2m-3 7 1 1 2-2" /></>,
  module: <><rect x="3" y="3" width="7" height="7" rx="2" /><rect x="14" y="3" width="7" height="7" rx="2" /><rect x="3" y="14" width="7" height="7" rx="2" /><path d="M14 17.5h7m-3.5-3.5v7" /></>,
  settings: <><path d="M4 7h16M4 17h16" /><circle cx="9" cy="7" r="3" /><circle cx="15" cy="17" r="3" /></>,
  theme: <><circle cx="12" cy="12" r="9" /><path d="M12 3v18" /><path d="M12 3a9 9 0 0 1 0 18Z" fill="currentColor" stroke="none" /></>,
  logout: <><path d="M9 4H4v16h5m5-12 4 4-4 4m-5-4h12" /></>,
  arrow: <><path d="m9 5 7 7-7 7" /></>,
  close: <><path d="m6 6 12 12M6 18 18 6" /></>,
  shield: <><path d="M12 3 3 7v5c0 5 9 9 9 9s9-4 9-9V7Z" /><path d="m8 12 3 3 5-6" /></>,
} satisfies Record<string, ReactNode>;

export type IconName = keyof typeof shapes;

export function Icon({ name, className = "" }: { name: IconName; className?: string }) {
  return <svg className={`ui-icon ${className}`} width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true" focusable="false">{shapes[name]}</svg>;
}
