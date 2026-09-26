import { useEffect, useState } from "react";
import { api, formatBytes, formatDuration, printable, type PrinterFile, type PrinterState } from "./api";

// The printer: live status, the current transfer, and its storage.
export function Printer() {
  const [st, setSt] = useState<PrinterState | null>(null);
  const [files, setFiles] = useState<PrinterFile[] | null>(null);
  const [error, setError] = useState("");
  const [confirm, setConfirm] = useState<string | null>(null); // "stop" or a file path to delete

  const refresh = () => api.printer().then(setSt, (e) => setError(String(e)));
  const loadFiles = () => api.printerFiles().then(setFiles, () => setFiles(null));
  useEffect(() => {
    refresh();
    loadFiles();
    const t = setInterval(refresh, 2000);
    return () => clearInterval(t);
  }, []);
  const act = (p: Promise<unknown>) =>
    p.then(
      () => (setError(""), setConfirm(null), refresh(), loadFiles()),
      (e) => setError(`Could not do that: ${e instanceof Error ? e.message : e}`),
    );

  if (!st) return <p>Loading…</p>;
  if (!st.enabled) {
    return (
      <p className="empty">
        No printer configured. Set it up under <a href="#/settings">Settings</a>.
      </p>
    );
  }
  const s = st.status!;
  const job = s.job;
  const t = st.transfer;
  const busy = s.machine !== "idle";
  return (
    <div className="printer">
      {error && <p className="error">{error}</p>}
      <section className="printer-card">
        <h2>
          {s.name || "Printer"} <span className={`machine ${s.machine}`}>{s.machine}</span>
        </h2>
        {s.firmware && <div className="sub">firmware {s.firmware}</div>}
        {job && (
          <div className="job">
            <div className="job-title">
              {job.file} — {job.state}
              {job.error && <span className="error"> ({job.error})</span>}
            </div>
            <div className="progress">
              <div style={{ width: `${Math.round(job.progress * 100)}%` }} />
            </div>
            <div className="sub">
              layer {job.layer} / {job.layers} · {Math.round(job.progress * 100)}%
              {job.remainingMs > 0 && ` · ${formatDuration(job.remainingMs)} left`}
            </div>
            {["printing", "preparing", "paused", "pausing"].includes(job.state) && (
              <div className="actions">
                {job.state === "paused" ? (
                  <button onClick={() => act(api.printerControl("resume"))}>Resume</button>
                ) : (
                  <button onClick={() => act(api.printerControl("pause"))}>Pause</button>
                )}
                {confirm === "stop" ? (
                  <>
                    <button className="danger" onClick={() => act(api.printerControl("stop"))}>
                      Really stop the print
                    </button>
                    <button onClick={() => setConfirm(null)}>Keep printing</button>
                  </>
                ) : (
                  <button onClick={() => setConfirm("stop")}>Stop…</button>
                )}
              </div>
            )}
          </div>
        )}
      </section>

      {t && (
        <section className="printer-card">
          <h2>Transfer</h2>
          <div className="job-title">
            {t.file} — {t.state}
            {t.start && t.state === "sending" && " (starts printing when done)"}
            {t.error && <span className="error"> ({t.error})</span>}
          </div>
          <div className="progress">
            <div style={{ width: `${t.size ? Math.round((t.sent / t.size) * 100) : 0}%` }} />
          </div>
          <div className="sub">
            {formatBytes(t.sent)} / {formatBytes(t.size)}
          </div>
          {t.state === "sending" && (
            <div className="actions">
              <button onClick={() => act(api.cancelTransfer())}>Cancel</button>
            </div>
          )}
        </section>
      )}

      <section className="printer-card">
        <h2>
          On the printer{" "}
          <button className="link" onClick={loadFiles}>
            refresh
          </button>
        </h2>
        {files === null ? (
          <p className="sub">Could not read the printer's storage.</p>
        ) : files.length === 0 ? (
          <p className="sub">No files in the internal storage.</p>
        ) : (
          <ul className="parts">
            {files
              .filter((f) => !f.folder)
              .map((f) => (
                <li key={f.path}>
                  <span>{f.path.split("/").pop()}</span>
                  <span className="file-actions">
                    {f.size != null && formatBytes(f.size)}
                    {printable(f.path) && (
                      <button className="small" disabled={busy} onClick={() => act(api.printExisting(f.path))}>
                        Print
                      </button>
                    )}
                    {confirm === f.path ? (
                      <>
                        <button className="small danger" onClick={() => act(api.deletePrinterFiles([f.path]))}>
                          Delete
                        </button>
                        <button className="small" onClick={() => setConfirm(null)}>
                          Keep
                        </button>
                      </>
                    ) : (
                      <button className="small" onClick={() => setConfirm(f.path)}>
                        Delete…
                      </button>
                    )}
                  </span>
                </li>
              ))}
          </ul>
        )}
      </section>
    </div>
  );
}
