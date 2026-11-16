import { useState } from "react";
import { api, Found } from "./api";
import { nameError, sanitizeName } from "./ir";

type Props = {
  refresh: () => Promise<void>;
  notify: (text: string, kind?: "ok" | "error") => void;
};

// A readable default name: the name stored on the device, else rm_<last octet>.
function suggestName(f: Found): string {
  const fromDevice = sanitizeName((f.name ?? "").toLowerCase()).replace(/[^a-z0-9_-]/g, "");
  if (fromDevice && !nameError(fromDevice)) return fromDevice;
  const last = f.host.split(".").pop();
  return `rm_${last}`;
}

export function FindDevices({ refresh, notify }: Props) {
  const [found, setFound] = useState<Found[] | null>(null);
  const [searching, setSearching] = useState(false);

  async function search() {
    setSearching(true);
    try {
      setFound(await api.discover());
    } catch (e) {
      notify(e instanceof Error ? e.message : "Suche fehlgeschlagen", "error");
    } finally {
      setSearching(false);
    }
  }

  return (
    <section className="find" aria-labelledby="find-title">
      <div className="find-head">
        <div>
          <h2 id="find-title">Geräte im Netz</h2>
          <p className="hint">Sucht Broadlink-Fernbedienungen im LAN, die noch nicht eingebunden sind.</p>
        </div>
        <button className="btn" onClick={search} disabled={searching}>
          {searching ? "Suche läuft …" : found ? "Erneut suchen" : "Suchen"}
        </button>
      </div>

      {found && found.length === 0 && (
        <p className="empty">
          Nichts gefunden. Das Gerät muss im selben Netz hängen und die Bridge muss das Netz kennen
          (<code>discovery.networks</code> in der Config).
        </p>
      )}
      {found && found.length > 0 && (
        <ul className="found">
          {found.map((f) => (
            <FoundRow key={f.mac || f.host} found={f} refresh={refresh} notify={notify} onAdded={search} />
          ))}
        </ul>
      )}
    </section>
  );
}

function FoundRow({
  found,
  refresh,
  notify,
  onAdded,
}: Props & { found: Found; onAdded: () => Promise<void> }) {
  const [name, setName] = useState(() => suggestName(found));
  const [busy, setBusy] = useState(false);
  const err = nameError(name);

  async function add() {
    setBusy(true);
    try {
      await api.addDevice(name, found.host);
      notify(`„${name}“ eingebunden. Jetzt kannst du Tasten anlernen.`);
      await refresh();
      await onAdded();
    } catch (e) {
      notify(e instanceof Error ? e.message : "Hinzufügen fehlgeschlagen", "error");
    } finally {
      setBusy(false);
    }
  }

  return (
    <li className="found-row">
      <div>
        <strong>{found.model}</strong>
        {found.name ? ` · ${found.name}` : ""}
        <p className="meta">
          <span>{found.host}</span>
          <span>{found.mac}</span>
        </p>
      </div>
      {found.added ? (
        <span className="muted">Schon eingebunden als „{found.added}“</span>
      ) : !found.supported ? (
        <span className="muted">Kein unterstützter IR-Sender</span>
      ) : (
        <div className="inline add">
          <input
            name="device-name"
            aria-label={`Name für ${found.host}`}
            aria-invalid={!!err}
            value={name}
            onChange={(e) => setName(sanitizeName(e.target.value))}
          />
          <button className="btn primary" onClick={add} disabled={busy || !!err || !name}>
            Hinzufügen
          </button>
        </div>
      )}
    </li>
  );
}
