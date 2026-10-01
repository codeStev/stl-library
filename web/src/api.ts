// Typed client for the stlib JSON API.

import { signedOut } from "./auth";

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
  hidden?: boolean;
  addedUnix?: number;
  lastPrintedUnix?: number;
  previewVersion?: number; // when a chosen preview was made (part of the picture's URL)
}

export interface FileRef {
  id: number;
  name: string;
  size: number;
}

// A part file seen from a plate link (see the slice contents).
export interface PartLink {
  partId?: number;
  path: string;
  name: string;
  modelId?: number;
  modelName?: string;
  count: number;
  missing?: boolean;
}

export const isSliced = (name: string) => /\.(ctb|cbddlp|goo|chitubox|lys|lyt|photon|pws)$/i.test(name);

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
  relabeled?: boolean;
  // How many copies of each part (by id) were printed, from printed prints.
  printedParts: Record<string, number>;
}

// What a sliced file says about itself (read from its header).
export interface SliceMeta {
  format: string;
  version: number;
  layers: number;
  layerHeight: number;
  exposureS: number;
  bottomExposureS: number;
  bottomLayers: number;
  printSeconds?: number;
  volumeMl?: number;
  resX: number;
  resY: number;
  hasPreview: boolean;
}

// A "print": a named set of parts (with counts) and plates, possibly from several models.
export interface JobSummary {
  id: number;
  name: string;
  state: "planned" | "printed";
  items: number;
  copies: number;
  plates: number;
  createdUnix: number;
  printedUnix?: number;
}
export interface JobItem {
  partId?: number;
  path: string;
  name: string;
  modelId?: number;
  modelName?: string;
  count: number;
  missing?: boolean;
}
export interface JobPlate {
  partId?: number;
  uploadId?: string;
  name: string;
  size?: number;
  missing?: boolean;
}
export interface Job {
  id: number;
  name: string;
  note: string;
  state: "planned" | "printed";
  createdUnix: number;
  printedUnix?: number;
  items: JobItem[];
  plates: JobPlate[];
}

export interface PrinterJob {
  file: string;
  state: string;
  layer: number;
  layers: number;
  progress: number;
  elapsedMs: number;
  remainingMs: number;
  error?: string;
}

export interface PrinterState {
  enabled: boolean;
  status?: { name: string; firmware?: string; machine: string; uvTemp?: number; job?: PrinterJob };
  transfer?: { partId: number; file: string; size: number; sent: number; start: boolean; state: string; error?: string };
}

export interface PrinterFile {
  path: string;
  folder?: boolean;
  size?: number;
  used?: number;
  total?: number;
}

export interface PrinterSettings {
  host: string;
  controlPort?: number;
  discoveryPort?: number;
  source?: "saved" | "default" | "none";
}

export interface NotificationSettings {
  ntfyUrl: string;
  ntfyToken?: string;
  ntfyTokenSet?: boolean;
  clearNtfyToken?: boolean;
  smtpHost: string;
  smtpPort?: number;
  smtpSecurity?: "starttls" | "tls" | "none";
  smtpUsername?: string;
  smtpPassword?: string;
  smtpPasswordSet?: boolean;
  clearSmtpPassword?: boolean;
  emailFrom?: string;
  emailTo?: string;
  events: Record<string, boolean>;
  allEvents?: string[];
}

export const EVENT_LABELS: Record<string, string> = {
  "print.done": "A print finished",
  "print.stopped": "A print was stopped",
  "print.error": "The printer reported an error",
  "import.done": "New models were imported",
  "import.failed": "An import needs attention",
  "account.registered": "Someone created an account",
  "digest.weekly": "Weekly summary (new models, imports, damaged files, backup)",
};

export const printable = (name: string) => /\.(ctb|goo)$/i.test(name);

export function formatDuration(ms: number): string {
  const s = Math.round(ms / 1000);
  const h = Math.floor(s / 3600), m = Math.floor((s % 3600) / 60);
  if (h > 0) return `${h} h ${m} min`;
  if (m > 0) return `${m} min`;
  return `${s} s`;
}

export interface ScanState {
  running: boolean;
  started?: number;
  finished?: number;
  added: number;
  updated: number;
  removed: number;
  unchanged: number;
  issues: number;
  pruned: number;
  error?: string;
}

export interface ImportRecord {
  source: string;
  state: "existing" | "waiting" | "queued" | "imported" | "duplicate" | "failed";
  target?: string;
  files: number;
  message?: string;
  updated: number;
}

export interface BulkRow {
  id: number;
  name: string;
  from: string;
  to: string;
  problem?: string;
}

export interface BulkBody {
  ids: number[];
  creator?: string | null;
  release?: string | null;
  category?: string | null;
  find?: string;
  replace?: string;
}

export interface StorageReport {
  bytes: number;
  models: number;
  files: number;
  creators: { name: string; models: number; bytes: number; releases: { name: string; models: number; bytes: number }[] }[];
  largest: { id: number; name: string; creator: string; bytes: number }[];
  kinds: { ext: string; files: number; bytes: number }[];
  duplicateBytes: number;
  duplicateGroups: number;
  hashed: number;
}

export interface TidyItem {
  kind: "junk-file" | "junk-folder" | "empty-folder" | "copy";
  path: string;
  size?: number;
  original?: string;
}

export interface TidyState {
  enabled: boolean;
  running: boolean;
  folders: number;
  finished: number;
  error?: string;
  items: TidyItem[];
}

export interface ImportList {
  enabled: boolean;
  running: boolean;
  current?: string;
  since?: number;
  done: number;
  records: ImportRecord[];
}

export interface ImportPreview {
  target?: string;
  settled: boolean;
  why?: string;
  error?: string;
  total: number;
  placements: { from: string; to: string }[];
}

export interface Collection {
  id: number;
  name: string;
  note: string;
  models: number;
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

// A change that gets no answer within this time counts as failed, so the
// UI can say so instead of waiting forever.
const SAVE_TIMEOUT_MS = 15_000;

async function send<T = void>(method: string, path: string, body?: unknown): Promise<T> {
  let r: Response;
  try {
    r = await fetch(path, {
      method,
      headers: body === undefined ? {} : { "Content-Type": "application/json" },
      body: body === undefined ? undefined : JSON.stringify(body),
      signal: AbortSignal.timeout(SAVE_TIMEOUT_MS),
    });
  } catch (e) {
    if (e instanceof DOMException && e.name === "TimeoutError") {
      throw new Error(`no answer from the server within ${SAVE_TIMEOUT_MS / 1000} s`);
    }
    throw e;
  }
  if (r.status === 401) signedOut();
  if (!r.ok) throw new Error(`${r.status} ${await r.text()}`);
  return (r.status === 204 ? undefined : r.json()) as Promise<T>;
}

async function get<T>(path: string): Promise<T> {
  const r = await fetch(path);
  if (r.status === 401) signedOut();
  if (!r.ok) throw new Error(`${r.status} ${await r.text()}`);
  return r.json() as Promise<T>;
}

export interface Filters {
  q: string;
  creator: string;
  tag: string;
  collection: string; // a collection id, "" for all
  printed: "" | "yes" | "no";
  hidden: "" | "yes";
  // Variant filters: the model has a variant with this value.
  scale: string;
  supports: string;
  format: string;
  fill: string;
  plate: "" | "yes"; // has a sliced file (library or uploaded plate)
  added: string; // first seen within this many days ("" = any)
  sort: "" | "added" | "printed";
}

export const emptyFilters: Filters = {
  q: "", creator: "", tag: "", collection: "", printed: "", hidden: "",
  scale: "", supports: "", format: "", fill: "", plate: "", added: "", sort: "",
};

export interface Facet {
  value: string;
  models: number;
}

// Canonical values for relabeling (see the convention package).
export const DIM_VALUES: Record<DimKey, string[]> = {
  scale: ["28mm", "32mm", "35mm", "54mm", "75mm", "100mm", "120mm", "178mm", "1-6", "1-10", "1-12", "Bust", "Freescale", "Heroic"],
  supports: ["Supported", "No Supports"],
  density: ["Beefed", "Light"],
  format: ["Lychee", "Chitubox", "STL", "OBJ", "3MF"],
  fill: ["Hollow", "Solid"],
  split: ["Combined"],
  tech: ["FDM", "Resin"],
  extra: ["Repaired", "Original"],
};

export const displayName = (m: { name: string; displayName?: string }) => m.displayName || m.name;

export const api = {
  models: (f: Filters, offset: number, limit = 60) =>
    get<ModelSummary[]>(`/api/models?${new URLSearchParams({ ...f, offset: String(offset), limit: String(limit) })}`),
  // One page of models with the total number the filters match.
  modelsPage: async (f: Filters, offset: number, limit: number) => {
    const r = await fetch(`/api/models?${new URLSearchParams({ ...f, offset: String(offset), limit: String(limit) })}`);
    if (r.status === 401) signedOut();
    if (!r.ok) throw new Error(`${r.status} ${await r.text()}`);
    const items = (await r.json()) as ModelSummary[];
    return { items, total: Number(r.headers.get("X-Total-Count") ?? items.length) };
  },
  fixes: () => get<FixesState>("/api/fixes"),
  applyFix: (from: string, to: string) => send<{ id: number }>("POST", "/api/fixes/apply", { from, to }),
  undoFix: (id: number) => send("POST", `/api/fixes/${id}/undo`),
  health: () => get<HealthState>("/api/health"),
  healthRun: () => send("POST", "/api/health/run"),
  healthPause: () => send("POST", "/api/health/pause"),
  dismissHealthEvent: (id: number) => send("POST", `/api/health/events/${id}/dismiss`),
  setGaps: () => get<SetGap[]>("/api/set-gaps"),
  mergeDuplicates: (g: { sha256: string; size: number }, keepPart: number) =>
    send<{ merged: number; error?: string }>("POST", "/api/duplicates/merge", { sha256: g.sha256, size: g.size, keepPart }),
  duplicates: (minKB: number, offset: number) => get<DuplicatePage>(`/api/duplicates?min=${minKB}&limit=25&offset=${offset}`),
  facets: () => get<Record<"scale" | "supports" | "format" | "fill", Facet[]>>("/api/facets"),
  tags: () => get<Tag[]>("/api/tags"),
  setTags: (modelId: number, tags: string[]) => send<{ tags: string[] }>("PUT", `/api/models/${modelId}/tags`, { tags }),
  editTags: (ids: number[], add: string[], remove: string[]) =>
    send<{ models: number; added: string[]; removed: string[] }>("POST", "/api/models/tags", { ids, add, remove }),
  setName: (modelId: number, name: string) => send("PUT", `/api/models/${modelId}/name`, { name }),
  markPrinted: (variantId: number, note: string) => send<Print>("POST", `/api/variants/${variantId}/prints`, { note }),
  deletePrint: (id: number) => send("DELETE", `/api/prints/${id}`),
  queue: () => get<QueueItem[]>("/api/queue"),
  setHidden: (modelId: number, hidden: boolean) => send("PUT", `/api/models/${modelId}/hidden`, { hidden }),
  setLabel: (variantId: number, dims: Partial<Record<DimKey, string>>, option: string) =>
    send("PUT", `/api/variants/${variantId}/label`, { dims, option }),
  resetLabel: (variantId: number) => send("DELETE", `/api/variants/${variantId}/label`),
  printer: () => get<PrinterState>("/api/printer"),
  sendToPrinter: (partId: number, start: boolean) => send("POST", `/api/parts/${partId}/print`, { start }),
  printerControl: (action: "pause" | "resume" | "stop") => send("POST", `/api/printer/${action}`, {}),
  cancelTransfer: () => send("DELETE", "/api/printer/transfer"),
  printerFiles: (dir = "/local") => get<PrinterFile[]>(`/api/printer/files?dir=${encodeURIComponent(dir)}`),
  printExisting: (path: string) => send("POST", "/api/printer/files/print", { path }),
  deletePrinterFiles: (paths: string[]) => send("POST", "/api/printer/files/delete", { paths }),
  printerSettings: () => get<PrinterSettings>("/api/settings/printer"),
  savePrinterSettings: (s: PrinterSettings) => send("PUT", "/api/settings/printer", s),
  testPrinterSettings: (s: PrinterSettings) =>
    send<{ ok: boolean; machine: string; name?: string; firmware?: string }>("POST", "/api/settings/printer/test", s),
  notificationSettings: () => get<NotificationSettings>("/api/settings/notifications"),
  saveNotificationSettings: (s: NotificationSettings) => send("PUT", "/api/settings/notifications", s),
  testNotifications: (s: NotificationSettings) => send("POST", "/api/settings/notifications/test", s),
  scanState: () => get<ScanState>("/api/library/scan"),
  requestScan: () => send("POST", "/api/library/scan"),
  imports: () => get<ImportList>("/api/imports"),
  requestImport: (source: string) => send("POST", "/api/imports/request", { source }),
  bulkPlan: (b: BulkBody) => send<{ canApply: boolean; rows: BulkRow[] }>("POST", "/api/bulk/plan", b),
  bulkApply: (b: BulkBody) => send<{ done: number; failed: string[]; rows: BulkRow[] }>("POST", "/api/bulk/apply", b),
  storage: () => get<StorageReport>("/api/storage"),
  tidy: () => get<TidyState>("/api/tidy"),
  tidyRun: () => send("POST", "/api/tidy/run"),
  tidyApply: (paths: string[]) => send<{ done: number; failed: string[] }>("POST", "/api/tidy/apply", { paths }),
  runImport: () => send("POST", "/api/imports/run"),
  cleanDuplicateImports: () => send<{ removed: number }>("POST", "/api/imports/clean-duplicates"),
  retryFailedImports: () => send<{ requested: number }>("POST", "/api/imports/retry-failed"),
  previewImport: (source: string) => get<ImportPreview>(`/api/imports/preview?source=${encodeURIComponent(source)}`),
  enqueue: (variantId: number, note = "") => send("PUT", `/api/variants/${variantId}/queue`, { note }),
  dequeue: (variantId: number) => send("DELETE", `/api/variants/${variantId}/queue`),
  collections: () => get<Collection[]>("/api/collections"),
  createCollection: (name: string, note = "") => send<Collection>("POST", "/api/collections", { name, note }),
  updateCollection: (id: number, name: string, note: string) => send("PUT", `/api/collections/${id}`, { name, note }),
  deleteCollection: (id: number) => send("DELETE", `/api/collections/${id}`),
  editCollection: (id: number, add: number[], remove: number[]) => send("POST", `/api/collections/${id}/models`, { add, remove }),
  modelCollections: (modelId: number) => get<Collection[]>(`/api/models/${modelId}/collections`),
  sliceMeta: async (kind: "part" | "upload", id: number | string): Promise<SliceMeta | null> => {
    const r = await fetch(`/api/${kind === "part" ? "parts" : "plates"}/${id}/slicemeta`);
    return r.ok ? ((await r.json()) as SliceMeta) : null; // not a readable sliced file: no details
  },
  slicePreviewURL: (kind: "part" | "upload", id: number | string) => `/api/${kind === "part" ? "parts" : "plates"}/${id}/preview.png`,
  jobs: () => get<JobSummary[]>("/api/jobs"),
  createJob: (name: string, note = "") => send<{ id: number }>("POST", "/api/jobs", { name, note }),
  job: (id: number) => get<Job>(`/api/jobs/${id}`),
  updateJob: (id: number, name: string, note: string, state: string) => send("PUT", `/api/jobs/${id}`, { name, note, state }),
  deleteJob: (id: number) => send("DELETE", `/api/jobs/${id}`),
  setJobItems: (id: number, items: { partId: number; count: number }[]) => send("PUT", `/api/jobs/${id}/items`, { items }),
  setJobPlates: (id: number, plates: { partId?: number; uploadId?: string }[]) => send("PUT", `/api/jobs/${id}/plates`, { plates }),
  plateURL: (uploadId: string) => `/api/plates/${uploadId}`,
  uploadContents: (uploadId: string) => get<{ contents: PartLink[] }>(`/api/plates/${uploadId}/contents`),
  setUploadContents: (uploadId: string, items: { partId: number; count: number }[]) => send("PUT", `/api/plates/${uploadId}/contents`, { items }),
  sliceContents: (partId: number) => get<{ contents: PartLink[]; usedIn: PartLink[] }>(`/api/parts/${partId}/contents`),
  suggestSliceContents: (partId: number) => get<{ suggestions: PartLink[] }>(`/api/parts/${partId}/contents/suggest`),
  setSliceContents: (partId: number, items: { partId: number; count: number }[]) =>
    send("PUT", `/api/parts/${partId}/contents`, { items }),
  variantSlices: (variantId: number) => get<{ parts: Record<string, PartLink[]> }>(`/api/variants/${variantId}/slices`),
  model: (id: number) => get<ModelDetail>(`/api/models/${id}`),
  creators: () => get<Creator[]>("/api/creators"),
  issues: () => get<Issue[]>("/api/issues"),
  imageURL: (id: number) => `/api/images/${id}`,
  thumbURL: (id: number) => `/api/images/${id}/thumb`,
  previewURL: (modelId: number, version?: number) => `/api/models/${modelId}/thumb${version ? `?v=${version}` : ""}`,
  setPreview: (modelId: number, png: Blob) =>
    fetch(`/api/models/${modelId}/preview`, { method: "PUT", headers: { "Content-Type": "image/png" }, body: png }).then(async (r) => {
      if (r.status === 401) signedOut();
      if (!r.ok) throw new Error(`${r.status} ${await r.text()}`);
    }),
  reviewState: (creator: string, limit = 3) =>
    get<{ remaining: number; done: number; next: number[] }>(`/api/previews/review?${new URLSearchParams({ creator, limit: String(limit) })}`),
  skipReview: (modelId: number) => send("POST", `/api/previews/review/${modelId}/skip`),
  undoReview: (modelId: number) => send("DELETE", `/api/previews/review/${modelId}`),
  resetReview: (creator: string) => send<{ reset: number }>("POST", `/api/previews/review/reset?${new URLSearchParams({ creator })}`),
  resetPreview: (modelId: number) => send("DELETE", `/api/models/${modelId}/preview`),
  partURL: (id: number) => `/api/parts/${id}`,
  zipURL: (variantId: number) => `/api/variants/${variantId}/zip`,
};

export function formatBytes(n: number): string {
  if (n < 1e6) return `${(n / 1e3).toFixed(0)} KB`;
  if (n < 1e9) return `${(n / 1e6).toFixed(1)} MB`;
  return `${(n / 1e9).toFixed(2)} GB`;
}

export const formatDate = (unix: number) => new Date(unix * 1000).toLocaleDateString();

// showLibrary makes the library open with these filters, on its first page.
export function showLibrary(f: Partial<Filters>) {
  try {
    sessionStorage.setItem("filters", JSON.stringify({ ...emptyFilters, ...f }));
    sessionStorage.setItem("libpage", "0");
    sessionStorage.removeItem("libscroll");
  } catch {
    /* no storage: the library opens unfiltered */
  }
}

// uploadPlate sends a sliced file (.ctb, .goo, ...) to the app, with progress; it is stored in the
// app's data, not in the library.
export function uploadPlate(file: File, onProgress: (done: number, total: number) => void): Promise<{ id: string; name: string; size: number }> {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    xhr.open("POST", "/api/plates");
    xhr.upload.onprogress = (e) => e.lengthComputable && onProgress(e.loaded, e.total);
    xhr.onload = () => {
      if (xhr.status === 401) signedOut();
      if (xhr.status >= 200 && xhr.status < 300) resolve(JSON.parse(xhr.responseText));
      else reject(new Error(`${xhr.status} ${xhr.responseText || "upload failed"}`));
    };
    xhr.onerror = () => reject(new Error("upload failed"));
    const form = new FormData();
    form.append("file", file);
    xhr.send(form);
  });
}

export interface HealthEvent {
  id?: number;
  path: string;
  detail: string;
  atUnix?: number;
  otherCopies?: string[];
}
export interface HealthState {
  backup: { configured: boolean; lastUnix: number; overdue: boolean; error?: string };
  enabled: boolean;
  files: number;
  hashed: number;
  running: boolean;
  paused: boolean;
  current?: string;
  done: number;
  errors: number;
  corrupt: HealthEvent[];
  missing: HealthEvent[];
}
export interface DuplicateGroup {
  sha256: string;
  size: number;
  wasted: number;
  files: { partId: number; path: string; modelId: number; modelName: string; linked?: boolean }[];
}
export interface SetGap {
  modelId: number;
  modelName: string;
  sides: { label: string; files: number }[];
  missing: string[];
}
export interface DuplicatePage {
  total: number;
  wastedOnPage: number;
  canMerge?: boolean;
  groups: DuplicateGroup[];
}

export interface FixSuggestion {
  from: string;
  to: string;
  issues: number;
}
export interface FixRecord {
  id: number;
  from: string;
  to: string;
  atUnix: number;
  undone?: boolean;
}
export interface FixesState {
  canApply: boolean;
  suggestions: FixSuggestion[];
  journal: FixRecord[];
}
