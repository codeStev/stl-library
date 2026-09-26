import { useEffect, useState } from "react";
import { api, formatDate, type QueueItem } from "./api";

// Variants waiting to be printed, oldest first.
export function Queue() {
  const [items, setItems] = useState<QueueItem[] | null>(null);
  const [error, setError] = useState("");
  const reload = () => api.queue().then(setItems, (e) => setError(String(e)));
  const act = (p: Promise<unknown>) => p.then(reload, (e) => setError(`Could not save: ${e instanceof Error ? e.message : e}`));
  useEffect(() => {
    reload();
  }, []);

  if (error) return <p className="error">{error}</p>;
  if (!items) return <p>Loading…</p>;
  if (items.length === 0) return <p className="empty">The print queue is empty. Add variants from a model's page.</p>;
  return (
    <ul className="queue">
      {items.map((it) => (
        <li key={it.variantId}>
          <a href={`#/model/${it.modelId}`}>
            <img src={api.previewURL(it.modelId)} alt="" loading="lazy" />
          </a>
          <div className="queue-text">
            <a href={`#/model/${it.modelId}`} className="title">
              {it.model}
            </a>
            <div className="sub">
              {it.label || "files"} · added {formatDate(it.added)}
            </div>
            {it.note && <div className="sub">{it.note}</div>}
          </div>
          <div className="queue-actions">
            <a className="button" href={api.zipURL(it.variantId)}>
              Download
            </a>
            <button onClick={() => act(api.markPrinted(it.variantId, ""))}>Printed</button>
            <button onClick={() => act(api.dequeue(it.variantId))}>Remove</button>
          </div>
        </li>
      ))}
    </ul>
  );
}
