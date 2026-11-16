import { useCallback, useEffect, useRef, useState } from "react";
import { api, Device } from "./api";
import { CommandKey } from "./CommandKey";
import { FindDevices } from "./FindDevices";
import { useDevices } from "./hooks";
import { LearnPanel } from "./LearnPanel";

type Notice = { text: string; kind: "ok" | "error"; id: number };

export function App() {
  const { devices, error, refresh } = useDevices();
  const [notice, setNotice] = useState<Notice | null>(null);
  const timer = useRef<number>();

  const notify = useCallback((text: string, kind: "ok" | "error" = "ok") => {
    window.clearTimeout(timer.current);
    setNotice({ text, kind, id: Date.now() });
    timer.current = window.setTimeout(() => setNotice(null), kind === "error" ? 7000 : 3000);
  }, []);
  useEffect(() => () => window.clearTimeout(timer.current), []);

  return (
    <>
      <header className="top">
        <h1>Fernbedienungen</h1>
        {error && (
          <p className="offline" role="alert">
            {error}. Die Anzeige ist möglicherweise veraltet.
          </p>
        )}
      </header>

      <main>
        {devices === null && !error && <p className="muted">Lade Geräte …</p>}
        {devices?.map((d) => (
          <DeviceSection key={d.name} device={d} refresh={refresh} notify={notify} />
        ))}
        {devices !== null && <FindDevices refresh={refresh} notify={notify} />}
      </main>

      <div className="notice-area" aria-live="polite">
        {notice && (
          <p key={notice.id} className={`notice ${notice.kind}`}>
            {notice.text}
          </p>
        )}
      </div>
    </>
  );
}

function DeviceSection({
  device,
  refresh,
  notify,
}: {
  device: Device;
  refresh: () => Promise<void>;
  notify: (text: string, kind?: "ok" | "error") => void;
}) {
  const [editMode, setEditMode] = useState(false);
  const [repeat, setRepeat] = useState(1);
  const [confirmRemove, setConfirmRemove] = useState(false);

  async function removeDevice() {
    try {
      await api.removeDevice(device.name);
      notify(`„${device.name}“ entfernt.`);
      await refresh();
    } catch (e) {
      notify(e instanceof Error ? e.message : "Entfernen fehlgeschlagen", "error");
    }
  }

  return (
    <section className="device" aria-labelledby={`dev-${device.name}`}>
      <div className="device-head">
        <div>
          <h2 id={`dev-${device.name}`}>{device.name}</h2>
          <p className="meta">
            <span className={`status ${device.online ? "on" : "off"}`}>
              {device.online ? "Online" : "Nicht erreichbar"}
            </span>
            <span>{device.model ?? "Broadlink"}</span>
            <span title={device.mac}>{device.host}</span>
            {device.locked && <span>Gesperrt</span>}
          </p>
        </div>

        <div className="controls">
          <div className="segmented" role="group" aria-label="Wiederholungen beim Senden">
            {[1, 2, 3].map((n) => (
              <button key={n} aria-pressed={repeat === n} onClick={() => setRepeat(n)}>
                {n}×
              </button>
            ))}
          </div>
          <button
            className={`btn${editMode ? " primary" : ""}`}
            aria-pressed={editMode}
            onClick={() => setEditMode((v) => !v)}
          >
            {editMode ? "Fertig" : "Bearbeiten"}
          </button>
        </div>
      </div>

      <LearnPanel device={device} refresh={refresh} notify={notify} />

      {device.commands.length === 0 ? (
        <p className="empty">
          Noch keine Tasten. Lerne oben die erste an. Sie erscheint danach hier und als Button in Home Assistant.
        </p>
      ) : (
        <ul className="keys">
          {device.commands.map((c) => (
            <CommandKey
              key={c.name}
              device={device}
              command={c}
              editMode={editMode}
              repeat={repeat}
              refresh={refresh}
              notify={notify}
            />
          ))}
        </ul>
      )}

      {editMode && device.source === "ui" && (
        <div className="actions remove">
          {confirmRemove ? (
            <>
              <span className="muted">Gerät samt gelernten Tasten entfernen?</span>
              <button className="btn danger small" onClick={removeDevice}>Ja, entfernen</button>
              <button className="btn quiet small" onClick={() => setConfirmRemove(false)}>Abbrechen</button>
            </>
          ) : (
            <button className="btn quiet small" onClick={() => setConfirmRemove(true)}>Gerät entfernen</button>
          )}
        </div>
      )}

      {device.last_command && (
        <p className="muted last">
          Zuletzt gesendet: <strong>{device.last_command === "(raw code)" ? "Roher Code (Test)" : device.last_command}</strong>
          {device.last_sent ? ` um ${new Date(device.last_sent).toLocaleTimeString("de-DE")}` : ""}
        </p>
      )}
    </section>
  );
}
