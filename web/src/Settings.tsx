import { useEffect, useState } from "react";
import { api, EVENT_LABELS, type NotificationSettings, type PrinterSettings } from "./api";

// Settings: the printer and notifications.
export function Settings() {
  return (
    <>
      <PrinterSection />
      <NotificationsSection />
    </>
  );
}

function PrinterSection() {
  const [s, setS] = useState<PrinterSettings | null>(null);
  const [msg, setMsg] = useState("");
  const [error, setError] = useState("");
  useEffect(() => {
    api.printerSettings().then(setS, (e) => setError(String(e)));
  }, []);
  if (error && !s) return <p className="error">{error}</p>;
  if (!s) return <p>Loading…</p>;

  const values = (): PrinterSettings => ({
    host: s.host.trim(),
    controlPort: s.controlPort || undefined,
    discoveryPort: s.discoveryPort || undefined,
  });
  const fail = (e: unknown) => (setMsg(""), setError(e instanceof Error ? e.message : String(e)));
  const port = (v: string) => (v === "" ? undefined : Number(v));

  return (
    <div className="settings">
      <h2>Printer</h2>
      <p className="sub">
        An ELEGOO resin printer on your network (SDCP, e.g. Saturn 4 Ultra). Leave the address empty to turn the
        printer features off.
        {s.source === "default" && " Currently set by PRINTER_ADDR; saving here overrides it."}
      </p>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          setError("");
          api.savePrinterSettings(values()).then(() => setMsg("Saved."), fail);
        }}
      >
        <label>
          Address (IP or host name)
          <input value={s.host} onChange={(e) => setS({ ...s, host: e.target.value })} placeholder="192.168.1.50" />
        </label>
        <label>
          Control port <span className="sub">(default 3030)</span>
          <input
            type="number"
            min={1}
            max={65535}
            value={s.controlPort ?? ""}
            onChange={(e) => setS({ ...s, controlPort: port(e.target.value) })}
            placeholder="3030"
          />
        </label>
        <label>
          Discovery port <span className="sub">(default 3000)</span>
          <input
            type="number"
            min={1}
            max={65535}
            value={s.discoveryPort ?? ""}
            onChange={(e) => setS({ ...s, discoveryPort: port(e.target.value) })}
            placeholder="3000"
          />
        </label>
        <div className="actions">
          <button
            type="button"
            disabled={!s.host.trim()}
            onClick={() => {
              setError("");
              setMsg("Testing…");
              api.testPrinterSettings(values()).then(
                (r) =>
                  r.ok
                    ? setMsg(`Connected: ${r.name ?? "printer"} (firmware ${r.firmware ?? "?"}), ${r.machine}.`)
                    : fail(new Error("No answer from a printer at that address.")),
                fail,
              );
            }}
          >
            Test connection
          </button>
          <button type="submit">Save</button>
        </div>
      </form>
      {msg && <p className="ok">{msg}</p>}
      {error && <p className="error">{error}</p>}
    </div>
  );
}

function NotificationsSection() {
  const [s, setS] = useState<NotificationSettings | null>(null);
  const [msg, setMsg] = useState("");
  const [error, setError] = useState("");
  const load = () => api.notificationSettings().then(setS, (e) => setError(String(e)));
  useEffect(() => {
    load();
  }, []);
  if (!s) return error ? <p className="error">{error}</p> : null;
  const set = (patch: Partial<NotificationSettings>) => setS({ ...s, ...patch });
  const fail = (e: unknown) => (setMsg(""), setError(e instanceof Error ? e.message : String(e)));
  const body = (): NotificationSettings => ({ ...s, allEvents: undefined });

  return (
    <div className="settings">
      <h2>Notifications</h2>
      <p className="sub">
        Get told when a print finishes or fails and when new models were imported - by{" "}
        <a href="https://ntfy.sh" target="_blank" rel="noreferrer">
          ntfy
        </a>{" "}
        (push to your phone) and/or email.
      </p>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          setError("");
          api.saveNotificationSettings(body()).then(() => (setMsg("Saved."), load()), fail);
        }}
      >
        <fieldset>
          <legend>Send when</legend>
          {(s.allEvents ?? Object.keys(EVENT_LABELS)).map((ev) => (
            <label key={ev} className="check">
              <input
                type="checkbox"
                checked={!!s.events[ev]}
                onChange={(e) => set({ events: { ...s.events, [ev]: e.target.checked } })}
              />
              {EVENT_LABELS[ev] ?? ev}
            </label>
          ))}
        </fieldset>
        <fieldset>
          <legend>ntfy</legend>
          <label>
            Topic URL
            <input value={s.ntfyUrl} onChange={(e) => set({ ntfyUrl: e.target.value })} placeholder="https://ntfy.sh/my-printer" />
          </label>
          <label>
            Access token <span className="sub">(optional{s.ntfyTokenSet ? "; one is saved - leave empty to keep it" : ""})</span>
            <input type="password" value={s.ntfyToken ?? ""} onChange={(e) => set({ ntfyToken: e.target.value })} autoComplete="off" />
          </label>
          {s.ntfyTokenSet && (
            <label className="check">
              <input type="checkbox" checked={!!s.clearNtfyToken} onChange={(e) => set({ clearNtfyToken: e.target.checked })} />
              remove the saved token
            </label>
          )}
        </fieldset>
        <fieldset>
          <legend>Email</legend>
          <label>
            SMTP server
            <input value={s.smtpHost} onChange={(e) => set({ smtpHost: e.target.value })} placeholder="smtp.example.org" />
          </label>
          <div className="row">
            <label>
              Port
              <input
                type="number"
                value={s.smtpPort ?? ""}
                onChange={(e) => set({ smtpPort: e.target.value ? Number(e.target.value) : undefined })}
                placeholder="587"
              />
            </label>
            <label>
              Security
              <select value={s.smtpSecurity ?? "starttls"} onChange={(e) => set({ smtpSecurity: e.target.value as NotificationSettings["smtpSecurity"] })}>
                <option value="starttls">STARTTLS</option>
                <option value="tls">TLS</option>
                <option value="none">none</option>
              </select>
            </label>
          </div>
          <label>
            Username
            <input value={s.smtpUsername ?? ""} onChange={(e) => set({ smtpUsername: e.target.value })} autoComplete="off" />
          </label>
          <label>
            Password <span className="sub">{s.smtpPasswordSet ? "(one is saved - leave empty to keep it)" : ""}</span>
            <input type="password" value={s.smtpPassword ?? ""} onChange={(e) => set({ smtpPassword: e.target.value })} autoComplete="new-password" />
          </label>
          {s.smtpPasswordSet && (
            <label className="check">
              <input type="checkbox" checked={!!s.clearSmtpPassword} onChange={(e) => set({ clearSmtpPassword: e.target.checked })} />
              remove the saved password
            </label>
          )}
          <label>
            From
            <input value={s.emailFrom ?? ""} onChange={(e) => set({ emailFrom: e.target.value })} placeholder="STL Library <stl@example.org>" />
          </label>
          <label>
            To <span className="sub">(comma-separated)</span>
            <input value={s.emailTo ?? ""} onChange={(e) => set({ emailTo: e.target.value })} placeholder="me@example.org" />
          </label>
        </fieldset>
        <div className="actions">
          <button
            type="button"
            onClick={() => {
              setError("");
              setMsg("Sending…");
              api.testNotifications(body()).then(() => setMsg("Test sent - check your phone / inbox."), fail);
            }}
          >
            Send test
          </button>
          <button type="submit">Save</button>
        </div>
      </form>
      {msg && <p className="ok">{msg}</p>}
      {error && <p className="error">{error}</p>}
    </div>
  );
}
