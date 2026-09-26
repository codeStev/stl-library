import { useEffect, useState } from "react";
import { api, type PrinterSettings } from "./api";

// Settings: how to reach the printer.
export function Settings() {
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
          <input value={s.host} onChange={(e) => setS({ ...s, host: e.target.value })} placeholder="192.168.2.35" />
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
