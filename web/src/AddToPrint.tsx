import { useState } from "react";
import { api, type JobSummary } from "./api";

// AddToPrint puts parts (with copies) into a planned print: pick one or make a new one.
export function AddToPrint({ parts, label, compact = false }: { parts: { id: number; count: number }[]; label: string; compact?: boolean }) {
  const [open, setOpen] = useState(false);
  const [jobs, setJobs] = useState<JobSummary[]>([]);
  const [choice, setChoice] = useState("");
  const [name, setName] = useState("");
  const [lastId, setLastId] = useState<number | null>(null);

  const show = () => {
    setOpen(true);
    setLastId(null);
    api.jobs().then((js) => {
      const planned = js.filter((j) => j.state === "planned");
      setJobs(planned);
      setChoice(planned[0] ? String(planned[0].id) : "new");
    }, () => setJobs([]));
  };
  const add = async () => {
    try {
      let id = Number(choice);
      let existing: { partId: number; count: number }[] = [];
      if (choice === "new") {
        id = (await api.createJob(name)).id;
      } else {
        existing = (await api.job(id)).items.filter((i) => i.partId).map((i) => ({ partId: i.partId as number, count: i.count }));
      }
      const merged = [...existing];
      for (const p of parts) {
        const hit = merged.find((m) => m.partId === p.id);
        if (hit) hit.count += p.count;
        else merged.push({ partId: p.id, count: p.count });
      }
      await api.setJobItems(id, merged);
      setOpen(false);
      setName("");
      setLastId(id);
    } catch (e) {
      alert(String(e));
    }
  };
  return (
    <span className="add-to-print">
      {!open ? (
        <button className={compact ? "view3d" : ""} onClick={show}>
          {label}
        </button>
      ) : (
        <span className="add-form">
          <select value={choice} onChange={(e) => setChoice(e.target.value)}>
            {jobs.map((j) => (
              <option key={j.id} value={j.id}>
                {j.name}
              </option>
            ))}
            <option value="new">New print…</option>
          </select>
          {choice === "new" && <input value={name} onChange={(e) => setName(e.target.value)} placeholder="name of the new print" maxLength={100} />}
          <button onClick={add} disabled={choice === "new" && !name.trim()}>
            Add
          </button>
          <button className="link" onClick={() => setOpen(false)}>
            cancel
          </button>
        </span>
      )}
      {!open && lastId !== null && (
        <a className="muted" href={`#/print/${lastId}`}>
          added · open print
        </a>
      )}
    </span>
  );
}
