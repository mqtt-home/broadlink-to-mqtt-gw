import { useCallback, useEffect, useRef, useState } from "react";
import { api, Device } from "./api";

// useDevices polls the bridge; faster while a learning session is running.
export function useDevices() {
  const [devices, setDevices] = useState<Device[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const learning = useRef(false);
  const timer = useRef<number>();

  const refresh = useCallback(async () => {
    try {
      const list = await api.devices();
      learning.current = list.some((d) => d.learn.state === "learning");
      setDevices(list);
      setError(null);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Unbekannter Fehler");
    }
  }, []);

  useEffect(() => {
    let stop = false;
    const tick = async () => {
      await refresh();
      if (!stop) timer.current = window.setTimeout(tick, learning.current ? 1000 : 4000);
    };
    void tick();
    return () => {
      stop = true;
      window.clearTimeout(timer.current);
    };
  }, [refresh]);

  return { devices, error, refresh };
}

// useNow re-renders every second while active (for the learn countdown).
export function useNow(active: boolean): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!active) return;
    setNow(Date.now());
    const id = window.setInterval(() => setNow(Date.now()), 500);
    return () => window.clearInterval(id);
  }, [active]);
  return now;
}

export async function copyText(text: string): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(text);
    return true;
  } catch {
    // navigator.clipboard needs HTTPS; the bridge is usually plain HTTP on the LAN.
    const ta = document.createElement("textarea");
    ta.value = text;
    ta.style.position = "fixed";
    ta.style.opacity = "0";
    document.body.appendChild(ta);
    ta.select();
    const ok = document.execCommand("copy");
    document.body.removeChild(ta);
    return ok;
  }
}
