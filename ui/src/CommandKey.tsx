import { useEffect, useRef, useState } from "react";
import { api, Command, Device } from "./api";
import { copyText } from "./hooks";
import { nameError, sanitizeName } from "./ir";
import { Waveform } from "./Waveform";

type Props = {
  device: Device;
  command: Command;
  editMode: boolean;
  repeat: number;
  refresh: () => Promise<void>;
  notify: (text: string, kind?: "ok" | "error") => void;
};

export function CommandKey({ device, command, editMode, repeat, refresh, notify }: Props) {
  const [playKey, setPlayKey] = useState(0);
  const [sending, setSending] = useState(false);
  const [renaming, setRenaming] = useState(false);
  const [newName, setNewName] = useState(command.name);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const deleteTimer = useRef<number>();
  const readOnly = command.source === "config";

  useEffect(() => () => window.clearTimeout(deleteTimer.current), []);
  useEffect(() => {
    if (!editMode) {
      setRenaming(false);
      setConfirmDelete(false);
    }
  }, [editMode]);

  async function press() {
    setSending(true);
    setPlayKey((k) => k + 1);
    try {
      await api.send(device.name, command.name, repeat);
      void refresh();
    } catch (e) {
      notify(errText(e), "error");
    } finally {
      setSending(false);
    }
  }

  async function rename() {
    const to = sanitizeName(newName);
    const err = nameError(to);
    if (err) return notify(err, "error");
    if (to === command.name) return setRenaming(false);
    try {
      await api.rename(device.name, command.name, to);
      notify(`Umbenannt in „${to}“.`);
      setRenaming(false);
      await refresh();
    } catch (e) {
      notify(errText(e), "error");
    }
  }

  async function remove() {
    if (!confirmDelete) {
      setConfirmDelete(true);
      deleteTimer.current = window.setTimeout(() => setConfirmDelete(false), 4000);
      return;
    }
    try {
      await api.remove(device.name, command.name);
      notify(`„${command.name}“ gelöscht.`);
      await refresh();
    } catch (e) {
      notify(errText(e), "error");
    }
  }

  async function copy() {
    notify((await copyText(command.code)) ? "Code kopiert." : "Kopieren nicht möglich.", "ok");
  }

  return (
    <li className={`key${editMode ? " editing" : ""}`}>
      <button
        className={`key-press${sending ? " sending" : ""}`}
        onClick={press}
        disabled={editMode || !device.online}
        title={device.online ? `„${command.name}“ senden` : "Gerät nicht erreichbar"}
      >
        <span className="key-name">{command.name}</span>
        <Waveform code={command.code} playKey={playKey} />
      </button>

      {editMode && (
        <div className="key-edit">
          {readOnly ? (
            <p className="hint">In der Config-Datei definiert.</p>
          ) : renaming ? (
            <form
              className="inline"
              onSubmit={(e) => {
                e.preventDefault();
                void rename();
              }}
            >
              <input
                name="new-name"
                aria-label={`Neuer Name für ${command.name}`}
                value={newName}
                autoFocus
                autoComplete="off"
                autoCapitalize="off"
                spellCheck={false}
                onChange={(e) => setNewName(sanitizeName(e.target.value))}
              />
              <button className="btn primary small" type="submit">OK</button>
            </form>
          ) : (
            <div className="actions">
              <button className="btn small" onClick={() => setRenaming(true)}>Umbenennen</button>
              <button className={`btn small${confirmDelete ? " danger" : ""}`} onClick={remove}>
                {confirmDelete ? "Wirklich löschen?" : "Löschen"}
              </button>
            </div>
          )}
          <button className="btn quiet small" onClick={copy}>Code kopieren</button>
        </div>
      )}
    </li>
  );
}

function errText(e: unknown): string {
  return e instanceof Error ? e.message : "Unbekannter Fehler";
}
