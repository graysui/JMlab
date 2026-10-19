export type SettingsEntry = {
  id: string;
  title: string;
  description: string;
  group: "personal" | "content" | "system";
  roles: string[];
};

export const settingsEntries: SettingsEntry[] = [
  { id: "profile", title: "主题与首页", description: "外观、首页内容和成人内容偏好", group: "personal", roles: [] },
  { id: "discover", title: "添加内容", description: "从已连接的模块搜索电影、剧集并加入媒体库", group: "content", roles: ["Admin", "Editor"] },
  { id: "libraries", title: "媒体库", description: "查看媒体库；管理员可调整筛选、排序和访问权限", group: "content", roles: ["Admin", "Editor"] },
  { id: "collections", title: "合集与清单", description: "整理作品、维护合集和导入清单", group: "content", roles: ["Admin", "Editor"] },
  { id: "relations", title: "人物与分类", description: "编辑人物、片商、系列和标签", group: "content", roles: ["Admin", "Editor"] },
  { id: "sources", title: "作品播放源", description: "按片名检查视频、调整顺序和启停来源", group: "content", roles: ["Admin", "Editor"] },
  { id: "dashboard", title: "运行概况", description: "查看服务健康、在线状态和最近任务", group: "system", roles: ["Admin", "Editor"] },
  { id: "statistics", title: "媒体统计", description: "查看作品分布、观看情况和播放源健康", group: "system", roles: ["Admin", "Editor"] },
  { id: "tasks", title: "任务中心", description: "查看进度、取消任务和重试失败任务", group: "system", roles: ["Admin", "Editor"] },
  { id: "logs", title: "操作日志", description: "查看当前账号有权访问的操作记录", group: "system", roles: ["Admin", "Editor"] },
  { id: "backup", title: "备份与恢复", description: "设置备份位置、保存数据和恢复归档", group: "system", roles: ["Admin"] },
  { id: "users", title: "用户与权限", description: "管理账号、角色和媒体库授权", group: "system", roles: ["Admin"] },
];
