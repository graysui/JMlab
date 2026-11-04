import { type IconName } from "../components/Icon";

export type ManagementGroup = "workspace" | "content" | "maintenance" | "system";

export type ManagementNavigationItem = {
  id: string;
  label: string;
  description: string;
  to: string;
  icon: IconName;
  group: ManagementGroup;
  roles: readonly string[];
};

export const managementGroups: Array<{ id: ManagementGroup; label: string }> = [
  { id: "workspace", label: "工作区" },
  { id: "content", label: "内容与媒体库" },
  { id: "maintenance", label: "维护" },
  { id: "system", label: "系统" },
];

export const managementNavigation: ManagementNavigationItem[] = [
  { id: "overview", label: "管理概览", description: "配置待办和运行状态", to: "/manage", icon: "home", group: "workspace", roles: ["Admin", "Editor"] },
  { id: "modules", label: "模块", description: "安装、连接和维护内容与视频模块", to: "/manage/modules", icon: "module", group: "workspace", roles: ["Admin"] },
  { id: "discover", label: "添加内容", description: "搜索信息模块并导入作品", to: "/manage/discover", icon: "search", group: "content", roles: ["Admin", "Editor"] },
  { id: "libraries", label: "媒体库", description: "查看范围、筛选和用户授权", to: "/manage/libraries", icon: "library", group: "content", roles: ["Admin", "Editor"] },
  { id: "collections", label: "合集与清单", description: "整理作品并维护合集", to: "/manage/collections", icon: "list", group: "content", roles: ["Admin", "Editor"] },
  { id: "relations", label: "人物与分类", description: "维护人物、片商、系列和标签", to: "/manage/relations", icon: "settings", group: "content", roles: ["Admin", "Editor"] },
  { id: "sources", label: "作品播放源", description: "检查视频来源和可用状态", to: "/manage/sources", icon: "ranking", group: "content", roles: ["Admin", "Editor"] },
  { id: "tasks", label: "任务中心", description: "查看进度、取消和重试任务", to: "/manage/tasks", icon: "list", group: "maintenance", roles: ["Admin", "Editor"] },
  { id: "logs", label: "操作日志", description: "查看当前账号可访问的操作记录", to: "/manage/logs", icon: "settings", group: "maintenance", roles: ["Admin", "Editor"] },
  { id: "statistics", label: "媒体统计", description: "查看内容和播放源分布", to: "/manage/statistics", icon: "ranking", group: "maintenance", roles: ["Admin", "Editor"] },
  { id: "users", label: "用户与权限", description: "管理账号、角色和媒体库授权", to: "/manage/users", icon: "shield", group: "system", roles: ["Admin"] },
  { id: "backup", label: "备份与恢复", description: "设置备份并恢复数据", to: "/manage/backup", icon: "shield", group: "system", roles: ["Admin"] },
];
