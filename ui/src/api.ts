export type Command = {
  name: string;
  source: "config" | "ui";
  bytes: number;
  code: string; // base64
};

export type LearnState = "idle" | "learning" | "done" | "error";

export type Learn = {
  state: LearnState;
  hint?: string;
  code?: string;
  error?: string;
  started?: string;
  timeout_seconds?: number;
};

export type Device = {
  name: string;
  host: string;
  model?: string;
  mac?: string;
  online: boolean;
  locked: boolean;
  last_command: string | null;
  last_sent: string | null;
  learn: Learn;
  commands: Command[];
};

export class ApiError extends Error {
  constructor(message: string, readonly status: number) {
    super(message);
  }
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  let res: Response;
  try {
    res = await fetch(path, {
      method,
      headers: method === "GET" ? undefined : { "Content-Type": "application/json" },
      body: method === "GET" || method === "DELETE" ? undefined : JSON.stringify(body ?? {}),
    });
  } catch {
    throw new ApiError("Bridge nicht erreichbar", 0);
  }
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new ApiError(translate(data.error, res.status), res.status);
  return data as T;
}

// The API reports errors in English; map the ones users can act on.
function translate(msg: string | undefined, status: number): string {
  if (!msg) return `Fehler ${status}`;
  if (msg.includes("config file")) return "Dieser Befehl steht in der Config-Datei und lässt sich hier nicht ändern.";
  if (msg.includes("learning in progress") || msg.includes("busy")) return "Das Gerät lernt gerade. Bitte warten.";
  if (msg.includes("already exists")) return "Dieser Name ist schon vergeben.";
  if (msg.includes("command not found") || status === 404) return "Befehl oder Gerät nicht gefunden.";
  if (msg.includes("timeout")) return "Keine Antwort vom Gerät (Timeout).";
  return msg;
}

const dev = (d: string) => `/api/devices/${encodeURIComponent(d)}`;
const cmd = (d: string, c: string) => `${dev(d)}/commands/${encodeURIComponent(c)}`;

export const api = {
  devices: () => request<Device[]>("GET", "/api/devices"),
  startLearn: (d: string, hint: string) => request<Learn>("POST", `${dev(d)}/learn`, { hint }),
  send: (d: string, c: string, repeat: number) => request("POST", `${cmd(d, c)}/send`, { repeat }),
  sendCode: (d: string, code: string, repeat = 1) => request("POST", `${dev(d)}/send`, { code, repeat }),
  save: (d: string, c: string, code: string) => request("PUT", cmd(d, c), { code }),
  rename: (d: string, from: string, to: string) => request("POST", `${cmd(d, from)}/rename`, { to }),
  remove: (d: string, c: string) => request("DELETE", cmd(d, c)),
};
