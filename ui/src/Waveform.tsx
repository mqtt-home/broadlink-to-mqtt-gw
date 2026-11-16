import { useMemo } from "react";
import { decodeBroadlink, waveformPath } from "./ir";

type Props = {
  code: string;
  width?: number;
  height?: number;
  /** Changing this value replays the sweep animation. */
  playKey?: number;
};

// Waveform draws the pulse train of an IR code. It is the fingerprint of a
// key: two different buttons almost never look alike.
export function Waveform({ code, width = 120, height = 20, playKey = 0 }: Props) {
  const d = useMemo(() => {
    const pulses = decodeBroadlink(code);
    return pulses ? waveformPath(pulses, width, height) : "";
  }, [code, width, height]);

  if (!d) return <span className="wave wave-empty">Kein IR-Code</span>;
  return (
    <svg
      className="wave"
      viewBox={`0 0 ${width} ${height}`}
      preserveAspectRatio="none"
      aria-hidden="true"
      focusable="false"
    >
      <path className="wave-base" d={d} pathLength={1} />
      {playKey > 0 && <path key={playKey} className="wave-play" d={d} pathLength={1} />}
    </svg>
  );
}
