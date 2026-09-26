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
  displayName?: string;
  tags: string[];
  prints: number;
}

export interface FileRef {
  id: number;
  name: string;
  size: number;
}

export type DimKey = "scale" | "supports" | "density" | "format" | "fill" | "split" | "tech" | "extra";

export interface Print {
  id: number;
  at: number; // unix seconds
  note?: string;
}

export interface Variant {
  id: number;
  label: string;
  dims: Partial<Record<DimKey, string>>;
  option?: string;
  parts: FileRef[];
  prints: Print[];
  queued: boolean;
}

export interface Tag {
  tag: string;
  models: number;
}

export interface QueueItem {
  variantId: number;
  modelId: number;
  model: string;
  label: string;
  added: number;
  note?: string;
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

async function send<T = void>(method: string, path: string, body?: unknown): Promise<T> {
  const r = await fetch(path, {
    method,
    headers: body === undefined ? {} : { "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  if (!r.ok) throw new Error(`${r.status} ${await r.text()}`);
  return (r.status === 204 ? undefined : r.json()) as Promise<T>;
}

async function get<T>(path: string): Promise<T> {
  const r = await fetch(path);
  if (!r.ok) throw new Error(`${r.status} ${await r.text()}`);
  return r.json() as Promise<T>;
}

export interface Filters {
  q: string;
  creator: string;
  tag: string;
  printed: "" | "yes" | "no";
}

export const displayName = (m: { name: string; displayName?: string }) => m.displayName || m.name;

export const api = {
  models: (f: Filters, offset: number, limit = 60) =>
    get<ModelSummary[]>(`/api/models?${new URLSearchParams({ ...f, offset: String(offset), limit: String(limit) })}`),
  tags: () => get<Tag[]>("/api/tags"),
  setTags: (modelId: number, tags: string[]) => send<{ tags: string[] }>("PUT", `/api/models/${modelId}/tags`, { tags }),
  setName: (modelId: number, name: string) => send("PUT", `/api/models/${modelId}/name`, { name }),
  markPrinted: (variantId: number, note: string) => send<Print>("POST", `/api/variants/${variantId}/prints`, { note }),
  deletePrint: (id: number) => send("DELETE", `/api/prints/${id}`),
  queue: () => get<QueueItem[]>("/api/queue"),
  enqueue: (variantId: number, note = "") => send("PUT", `/api/variants/${variantId}/queue`, { note }),
  dequeue: (variantId: number) => send("DELETE", `/api/variants/${variantId}/queue`),
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

export const formatDate = (unix: number) => new Date(unix * 1000).toLocaleDateString();
