import { useCallback, useEffect, useState } from "react";
import { api, formatBytes, formatDate, isSliced, uploadPlate, type Job, type JobPlate, type JobSummary } from "./api";
import { PartPicker } from "./PartPicker";
import { SliceEditor } from "./SliceEditor";
import { PlateInfo } from "./PlateInfo";

const PLATE_TYPES = ".ctb,.goo,.cbddlp,.photon,.pws,.chitubox,.lys,.lyt";

// The prints page: a print is a named set of parts (any models, with copies) and the plates
// (sliced files, from the library or uploaded) used for it. Marking it printed counts its parts
// as printed in "printed X of Y parts" of their models.
export function Prints({ id }: { id: number | null }) {
  return id ? <PrintDetail id={id} /> : <PrintList />;
}

function PrintList() {
  const [list, setList] = useState<JobSummary[]>([]);
  const [name, setName] = useState("");
  const [error, setError] = useState("");
  const load = () => api.jobs().then(setList, (e) => setError(String(e)));
  useEffect(() => {
    load();
  }, []);
  const create = () =>
    api.createJob(name).then(
      (r) => {
        location.hash = `#/print/${r.id}`;
      },
      (e) => setError(String(e)),
    );
  return (
    <div className="prints">
      <h1>Prints</h1>
      <form
        className="collection-new"
        onSubmit={(e) => {
          e.preventDefault();
          if (name.trim()) create();
        }}
      >
        <input value={name} onChange={(e) => setName(e.target.value)} placeholder="New print, e.g. Dungeon night wave 1" maxLength={100} />
        <button disabled={!name.trim()}>Create</button>
      </form>
      {error && <p className="error">{error}</p>}
      {list.length === 0 && <p className="empty">No prints yet. A print gathers the parts (from any models) and plates you print together.</p>}
      <ul className="collection-list">
        {list.map((j) => (
          <li key={j.id}>
            <a className="name" href={`#/print/${j.id}`}>
              {j.name}
            </a>
            <span className={`state ${j.state}`}>{j.state === "printed" ? `printed ${j.printedUnix ? formatDate(j.printedUnix) : ""}` : "planned"}</span>
            <span className="muted">
              {j.items} part{j.items === 1 ? "" : "s"} ({j.copies} copies) · {j.plates} plate{j.plates === 1 ? "" : "s"}
            </span>
          </li>
        ))}
      </ul>
    </div>
  );
}

function PrintDetail({ id }: { id: number }) {
  const [job, setJob] = useState<Job | null>(null);
  const [error, setError] = useState("");
  const [editing, setEditing] = useState<JobPlate | null>(null);
  const [progress, setProgress] = useState<number | null>(null);
  const [draft, setDraft] = useState({ name: "", note: "" });
  const [dirty, setDirty] = useState(false);

  const load = useCallback(
    () =>
      api.job(id).then(
        (j) => {
          setJob(j);
          setDraft({ name: j.name, note: j.note });
          setDirty(false);
        },
        (e) => setError(String(e)),
      ),
    [id],
  );
  useEffect(() => {
    load();
  }, [load]);
  if (!job) return <p>{error || "Loading…"}</p>;

  const run = (p: Promise<unknown>) =>
    p.then(
      () => {
        setError("");
        return load();
      },
      (e) => setError(String(e)),
    );
  const saveItems = (items: { partId: number; count: number }[]) => run(api.setJobItems(job.id, items));
  const current = job.items.filter((i) => i.partId).map((i) => ({ partId: i.partId as number, count: i.count }));
  const plateRefs = (plates: JobPlate[]) => plates.filter((p) => !p.missing).map((p) => (p.uploadId ? { uploadId: p.uploadId } : { partId: p.partId }));
  const copies = job.items.reduce((n, i) => n + i.count, 0);

  return (
    <div className="print-detail">
      <p>
        <a href="#/prints">← All prints</a>
      </p>
      <form
        className="print-head"
        onSubmit={(e) => {
          e.preventDefault();
          run(api.updateJob(job.id, draft.name, draft.note, job.state));
        }}
      >
        <input value={draft.name} maxLength={100} onChange={(e) => (setDraft({ ...draft, name: e.target.value }), setDirty(true))} className="title" />
        <textarea value={draft.note} maxLength={500} placeholder="note" onChange={(e) => (setDraft({ ...draft, note: e.target.value }), setDirty(true))} />
        {dirty && <button>Save name and note</button>}
      </form>
      <div className="actions">
        <span className={`state ${job.state}`}>{job.state === "printed" ? `printed ${job.printedUnix ? formatDate(job.printedUnix) : ""}` : "planned"}</span>
        {job.state === "planned" ? (
          <button onClick={() => run(api.updateJob(job.id, job.name, job.note, "printed"))}>Mark printed</button>
        ) : (
          <button onClick={() => run(api.updateJob(job.id, job.name, job.note, "planned"))}>Back to planned</button>
        )}
        <button
          className="link"
          onClick={() => {
            if (confirm(`Delete the print “${job.name}”? The models and files stay.`)) api.deleteJob(job.id).then(() => (location.hash = "#/prints"), (e) => setError(String(e)));
          }}
        >
          Delete
        </button>
      </div>
      {error && <p className="error">{error}</p>}

      <h2>
        Parts ({job.items.length}, {copies} copies)
      </h2>
      {job.items.length === 0 && <p className="muted">No parts yet. Search a model below and add the files you print.</p>}
      <ul className="job-items">
        {job.items.map((it) => (
          <li key={it.path} className={it.missing ? "missing" : ""}>
            <input
              type="number"
              min={1}
              max={10000}
              value={it.count}
              disabled={!it.partId}
              onChange={(e) => saveItems(current.map((c) => (c.partId === it.partId ? { ...c, count: Math.max(1, Math.min(10000, Number(e.target.value) || 1)) } : c)))}
              aria-label={`Copies of ${it.name}`}
            />
            <span>×</span>
            <span className="name">{it.name}</span>
            {it.modelId ? (
              <a className="muted" href={`#/model/${it.modelId}`}>
                {it.modelName}
              </a>
            ) : (
              it.missing && <span className="muted">(file not in the library any more)</span>
            )}
            <button className="link" onClick={() => saveItems(current.filter((c) => c.partId !== it.partId))} aria-label={`Remove ${it.name}`}>
              ✕
            </button>
          </li>
        ))}
      </ul>
      <PartPicker
        exclude={isSliced}
        skip={current.map((c) => c.partId)}
        onAdd={(ps) => saveItems([...current, ...ps.map((p) => ({ partId: p.id, count: p.count }))])}
      />

      <h2>Plates ({job.plates.length})</h2>
      <ul className="job-items">
        {job.plates.map((p) => (
          <li key={p.uploadId ?? p.partId ?? p.name} className={p.missing ? "missing" : ""}>
            <span className="name">{p.name}</span>
            <span className="muted">{p.uploadId ? `uploaded · ${formatBytes(p.size ?? 0)}` : p.missing ? "file not in the library any more" : "library"}</span>
            {p.uploadId && (
              <>
                <a href={api.plateURL(p.uploadId)}>download</a>
                <button className="link" onClick={() => setEditing(editing?.uploadId === p.uploadId ? null : p)}>
                  Contains…
                </button>
              </>
            )}
            <button className="link" onClick={() => run(api.setJobPlates(job.id, plateRefs(job.plates.filter((x) => x !== p))))} aria-label={`Remove ${p.name}`}>
              ✕
            </button>
            {(p.uploadId || p.partId) && !p.missing && <PlateInfo kind={p.uploadId ? "upload" : "part"} id={(p.uploadId ?? p.partId) as string | number} />}
            {editing && editing.uploadId === p.uploadId && p.uploadId && (
              <SliceEditor
                title={p.name}
                load={() => api.uploadContents(p.uploadId as string)}
                save={(items) => api.setUploadContents(p.uploadId as string, items)}
                onSaved={() => setEditing(null)}
                onCancel={() => setEditing(null)}
              />
            )}
          </li>
        ))}
      </ul>
      <div className="slice-add">
        <label className="upload">
          Upload a plate (.ctb, .goo, …)
          <input
            type="file"
            accept={PLATE_TYPES}
            multiple
            onChange={async (e) => {
              const files = Array.from(e.target.files ?? []);
              e.target.value = "";
              let plates = plateRefs(job.plates);
              try {
                for (const f of files) {
                  setProgress(0);
                  const u = await uploadPlate(f, (done, total) => setProgress(Math.round((done / total) * 100)));
                  plates = [...plates, { uploadId: u.id }];
                }
                await run(api.setJobPlates(job.id, plates));
              } catch (err) {
                setError(String(err));
              } finally {
                setProgress(null);
              }
            }}
          />
        </label>
        {progress !== null && (
          <span className="progress">
            <progress value={progress} max={100} /> {progress}%
          </span>
        )}
      </div>
      <PartPicker
        label="Use as plate:"
        exclude={(n) => !isSliced(n)}
        skip={job.plates.map((p) => p.partId ?? 0)}
        onAdd={(ps) => run(api.setJobPlates(job.id, [...plateRefs(job.plates), ...ps.map((p) => ({ partId: p.id }))]))}
      />
    </div>
  );
}
