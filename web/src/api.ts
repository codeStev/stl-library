// Typed client for the stlib JSON API.

export interface ModelSummary {
  id: number;
  creator: string;
  release?: string;
  category?: string;
  name: string;
  dir: string;
  variants: number;
  parts: number;
  bytes: number;
  cover?: number;
  preview: boolean;
}

export interface FileRef {
  id: number;
  name: string;
  size: number;
}

export type DimKey = "scale" | "supports" | "density" | "format" | "fill" | "split" | "tech" | "extra";

export interface Variant {
  id: number;
  label: string;
  dims: Partial<Record<DimKey, string>>;
  option?: string;
  parts: FileRef[];
}

// In the detail, "variants" is the list itself (the summary's count is
// replaced by it).
export interface ModelDetail extends Omit<ModelSummary, "variants"> {
  variants: Variant[];
  images: FileRef[];
}

export interface Creator {
  name: string;
  models: number;
}

export interface Issue {
  dir: string;
  reason: string;
}

async function get<T>(path: string): Promise<T> {
  const r = await fetch(path);
  if (!r.ok) throw new Error(`${r.status} ${await r.text()}`);
  return r.json() as Promise<T>;
}

export const api = {
  models: (q: string, creator: string, offset: number, limit = 60) =>
    get<ModelSummary[]>(
      `/api/models?${new URLSearchParams({ q, creator, offset: String(offset), limit: String(limit) })}`,
    ),
  model: (id: number) => get<ModelDetail>(`/api/models/${id}`),
  creators: () => get<Creator[]>("/api/creators"),
  issues: () => get<Issue[]>("/api/issues"),
  imageURL: (id: number) => `/api/images/${id}`,
  thumbURL: (id: number) => `/api/images/${id}/thumb`,
  previewURL: (modelId: number) => `/api/models/${modelId}/thumb`,
  partURL: (id: number) => `/api/parts/${id}`,
  zipURL: (variantId: number) => `/api/variants/${variantId}/zip`,
};

export function formatBytes(n: number): string {
  if (n < 1e6) return `${(n / 1e3).toFixed(0)} KB`;
  if (n < 1e9) return `${(n / 1e6).toFixed(1)} MB`;
  return `${(n / 1e9).toFixed(2)} GB`;
}
