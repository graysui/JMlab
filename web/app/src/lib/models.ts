export type ItemType = "movie" | "series" | "season" | "episode" | "video";
export const mediaTypeLabel = (type?: string) => (({ movie: "电影", series: "剧集", season: "季", episode: "单集", video: "视频" } as Record<string, string>)[type || "movie"] || "作品");
export type Relation = { id: string; name: string; role?: string };
export type MediaChild = { sourceId: string; title: string; itemType: ItemType; indexNumber: number };
export type CatalogItem = {
  id: string; code: string; title: string; originalTitle?: string; overview?: string;
  year?: number; releaseDate?: string; runtimeSeconds?: number; sourceRating?: number;
  sourceUrl: string; source?: string; metadataStatus: string; itemType?: ItemType;
  parentId?: string; indexNumber?: number; metadataModuleId?: string;
  providerIds?: Record<string, string>; extraAttributes?: { children?: MediaChild[]; [key: string]: unknown };
  people?: Relation[]; studios?: Relation[]; series?: Relation[]; tags?: Relation[];
};
export const visibleCode = (item: { code?: string }) => item.code && !item.code.startsWith("media:") ? item.code : "";

export type PlaybackSourceRecord = { id: string; itemId: string; moduleId: string; moduleName?: string; providerId: string; providerType?: string; providerName?: string; providerGroup?: string; label: string; protocol: "hls" | "http" | "redirect"; quality?: string; language?: string; isPreview: boolean; status: "active" | "unavailable" | "expired"; enabled: boolean; expiresAt?: string; lastResolvedAt?: string; lastError?: string; lastProbeStatus?: string; lastProbeAt?: string; lastProbeDurationMs?: number; lastProbeError?: string; probeCount: number; probeSuccessCount: number; averageProbeDurationMs: number; sortOrder: number; createdAt: string; updatedAt: string };
