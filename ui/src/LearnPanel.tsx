import { useEffect, useState } from "react";
import { api, Device } from "./api";
import { useNow } from "./hooks";
import { nameError, sanitizeName } from "./ir";
import { Waveform } from "./Waveform";

type Props = {
  device: Device;
  refresh: () => Promise<void>;
  notify: (text: string, kind?: "ok" | "error") => void;
};

export function LearnPanel({ device, refresh, notify }: Props) {
  const l = device.learn;
  const [name, setName] = useState("");
  const [dismissed, setDismissed] = useState<string | null>(null);
  const [errorDismissed, setErrorDismissed] = useState(false);
  const [confirmOverwrite, setConfirmOverwrite] = useState(false);
  const [busy, setBusy] = useState(false);

  const learning = l.state === "learning";
  const done = l.state === "done" && !!l.code && l.code !== dismissed;
  const failed = l.state === "error" && !errorDismissed;

  // A learned code arrives with the hint the session was started with.
  useEffect(() => {
    if (done && l.hint) setName((n) => n || sanitizeName(l.hint ?? ""));
  }, [done, l.hint]);
  useEffect(() => setConfirmOverwrite(false), [name]);

  const now = useNow(learning);
  const secondsLeft = learning && l.started && l.timeout_seconds
    ? Math.max(0, Math.ceil((Date.parse(l.started) + l.timeout_seconds * 1000 - now) / 1000))
    : 0;

  const existing = device.commands.find((c) => c.name === name);
  const nErr = name ? nameError(name) : null;
  const readOnly = existing?.source === "config";

  async function start() {
    setBusy(true);
    try {
      setErrorDismissed(false);
      await api.startLearn(device.name, name);
      await refresh();
    } catch (e) {
      notify(msg(e), "error");
    } finally {
      setBusy(false);
    }
  }

  async function test() {
    try {
      await api.sendCode(device.name, l.code!);
      notify("Code gesendet. Hat das Gerät reagiert?");
    } catch (e) {
      notify(msg(e), "error");
    }
  }

  async function save() {
    const err = nameError(name);
    if (err) return notify(err, "error");
    if (existing && !confirmOverwrite) return setConfirmOverwrite(true);
    setBusy(true);
    try {
      await api.save(device.name, name, l.code!);
      setDismissed(l.code!);
      notify(`„${name}“ gespeichert.`);
      setName("");
      await refresh();
    } catch (e) {
      notify(msg(e), "error");
    } finally {
      setBusy(false);
    }
  }

  const disabled = !device.online || busy;

  return (
    <section className="learn" aria-labelledby={`learn-${device.name}`}>
      <div className={`ir-emitter${learning ? " active" : ""}`} aria-hidden="true">
        <span className="ir-dot" />
        <span className="ir-ring r1" />
        <span className="ir-ring r2" />
        <span className="ir-ring r3" />
      </div>

      <div className="learn-body">
        <h3 id={`learn-${device.name}`}>Taste anlernen</h3>

        {learning && (
          <div role="status">
            <p className="learn-lead">Drücke jetzt eine Taste auf der Original-Fernbedienung.</p>
            <p className="muted">
              Halte sie wenige Zentimeter an das Gerät. Noch {secondsLeft} s.
              {l.hint ? ` Gelernt wird „${l.hint}“.` : ""}
            </p>
          </div>
        )}

        {done && (
          <div className="learned">
            <p className="learn-lead">Signal empfangen.</p>
            <Waveform code={l.code!} width={320} height={34} />
            <div className="field">
              <label htmlFor={`name-${device.name}`}>Name der Taste</label>
              <input
                id={`name-${device.name}`}
                name="command-name"
                value={name}
                autoComplete="off"
                autoCapitalize="off"
                spellCheck={false}
                placeholder="z. B. tv_power"
                aria-invalid={!!nErr}
                aria-describedby={`name-help-${device.name}`}
                onChange={(e) => setName(sanitizeName(e.target.value))}
              />
              <p id={`name-help-${device.name}`} className={nErr || readOnly ? "hint error" : "hint"}>
                {readOnly
                  ? "Dieser Name steht in der Config-Datei und kann nicht überschrieben werden."
                  : confirmOverwrite
                    ? `„${name}“ gibt es schon. Noch einmal speichern überschreibt die Taste.`
                    : (nErr ?? "Buchstaben, Ziffern, _ und -. Der Name wird zum MQTT-Befehl.")}
              </p>
            </div>
            <div className="actions">
              <button className="btn primary" onClick={save} disabled={disabled || !name || !!nErr || readOnly}>
                {confirmOverwrite ? "Überschreiben" : "Speichern"}
              </button>
              <button className="btn" onClick={test} disabled={disabled}>Testen</button>
              <button className="btn quiet" onClick={() => setDismissed(l.code!)} disabled={busy}>Verwerfen</button>
            </div>
          </div>
        )}

        {failed && (
          <div role="alert">
            <p className="learn-lead error">Anlernen fehlgeschlagen.</p>
            <p className="muted">
              {l.error?.includes("timeout")
                ? "Es kam kein IR-Signal an. Halte die Fernbedienung näher an das Gerät und versuche es erneut."
                : l.error}
            </p>
          </div>
        )}

        {!learning && !done && (
          <div className="learn-start">
            {!failed && (
              <p className="muted">
                Gib einen Namen ein und drücke Anlernen. Den Namen kannst du auch erst nach dem Empfang vergeben.
              </p>
            )}
            <div className="inline">
              <input
                name="command-name"
                aria-label="Name der neuen Taste"
                value={name}
                autoComplete="off"
                autoCapitalize="off"
                spellCheck={false}
                placeholder="Name, z. B. tv_power"
                onChange={(e) => setName(sanitizeName(e.target.value))}
                onKeyDown={(e) => e.key === "Enter" && !disabled && !nErr && start()}
              />
              <button className="btn primary" onClick={start} disabled={disabled || !!nErr}>
                {failed ? "Nochmal anlernen" : "Anlernen"}
              </button>
            </div>
            {!device.online && <p className="hint error">Das Gerät ist nicht erreichbar.</p>}
          </div>
        )}
      </div>
    </section>
  );
}

function msg(e: unknown): string {
  return e instanceof Error ? e.message : "Unbekannter Fehler";
}
