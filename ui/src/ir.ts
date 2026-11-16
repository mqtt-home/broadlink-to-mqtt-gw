// Broadlink IR packets: 0x26, repeat, length (LE16), then pulse durations in
// units of 32.84 µs; a zero byte introduces a 16-bit big-endian duration.
// Pulses alternate mark (IR on) / space (IR off).
const TICK_US = 32.84;

export function decodeBroadlink(b64: string): number[] | null {
  let bytes: Uint8Array;
  try {
    bytes = Uint8Array.from(atob(b64), (c) => c.charCodeAt(0));
  } catch {
    return null;
  }
  if (bytes.length < 4 || bytes[0] !== 0x26) return null;
  const end = Math.min(bytes[2] + (bytes[3] << 8) + 4, bytes.length);
  const out: number[] = [];
  for (let i = 4; i < end; ) {
    let v = bytes[i++];
    if (v === 0) {
      if (i + 1 >= bytes.length) return null;
      v = (bytes[i] << 8) | bytes[i + 1];
      i += 2;
    }
    out.push(Math.round(v * TICK_US));
  }
  return out;
}

// waveformPath turns pulse durations into an SVG path of a square wave in a
// width×height box. Durations are compressed (square root) so that short
// pulses stay visible next to the long start burst.
export function waveformPath(pulses: number[], width = 120, height = 20, maxPulses = 90): string {
  const used = pulses.slice(0, maxPulses);
  if (used.length === 0) return "";
  const widths = used.map((p) => Math.sqrt(p));
  const total = widths.reduce((a, b) => a + b, 0);
  const lo = height - 2;
  const hi = 2;
  let x = 0;
  let d = `M0 ${lo}`;
  used.forEach((_, i) => {
    const y = i % 2 === 0 ? hi : lo; // mark = high
    const prev = i % 2 === 0 ? lo : hi;
    d += ` L${x.toFixed(2)} ${y}`;
    x += (widths[i] / total) * width;
    d += ` L${x.toFixed(2)} ${y}`;
    if (i === used.length - 1 && y !== lo) d += ` L${x.toFixed(2)} ${prev}`;
  });
  return d;
}

// sanitizeName keeps only characters the bridge accepts for command names.
// It runs on every keystroke, so it must not trim: "Volume " has to become
// "Volume_" so that the next letter lands after the underscore.
export function sanitizeName(raw: string): string {
  return raw.replace(/\s+/g, "_").replace(/[^A-Za-z0-9_-]/g, "");
}

export function nameError(name: string): string | null {
  if (!name) return "Name fehlt.";
  if (!/^[A-Za-z0-9_-]+$/.test(name)) return "Erlaubt sind Buchstaben, Ziffern, _ und -.";
  if (name === "learn") return "„learn“ ist reserviert.";
  return null;
}
