import { describe, expect, it } from "vitest";
import { decodeBroadlink, nameError, sanitizeName, waveformPath } from "./ir";

const b64 = (bytes: number[]) => btoa(String.fromCharCode(...bytes));

describe("decodeBroadlink", () => {
  it("decodes short and long pulses", () => {
    // 0x26, repeat 0, length 5: 0x10, 0x20, then 0x00 0x01 0x00 (=256 ticks)
    const pulses = decodeBroadlink(b64([0x26, 0, 5, 0, 0x10, 0x20, 0, 1, 0]))!;
    expect(pulses).toHaveLength(3);
    expect(pulses[0]).toBe(Math.round(16 * 32.84));
    expect(pulses[2]).toBe(Math.round(256 * 32.84));
  });

  it("rejects non-IR and malformed data", () => {
    expect(decodeBroadlink("!!!")).toBeNull();
    expect(decodeBroadlink(b64([0x24, 0, 1, 0, 1]))).toBeNull();
    expect(decodeBroadlink(b64([0x26, 0]))).toBeNull();
    expect(decodeBroadlink(b64([0x26, 0, 2, 0, 5, 0]))).toBeNull(); // 16-bit duration cut off
  });

  it("ignores trailing padding beyond the declared length", () => {
    expect(decodeBroadlink(b64([0x26, 0, 2, 0, 5, 6, 0, 0, 0]))).toHaveLength(2);
  });
});

describe("waveformPath", () => {
  it("is empty for no pulses and bounded in width", () => {
    expect(waveformPath([])).toBe("");
    const d = waveformPath([9000, 4500, 560, 560, 560, 1690], 120, 20);
    const xs = [...d.matchAll(/L([\d.]+) /g)].map((m) => Number(m[1]));
    expect(Math.max(...xs)).toBeCloseTo(120, 0);
    expect(d.startsWith("M0 18")).toBe(true);
  });

  it("limits the number of pulses", () => {
    const d = waveformPath(new Array(500).fill(500), 120, 20, 10);
    expect((d.match(/L/g) ?? []).length).toBeLessThan(30);
  });
});

describe("names", () => {
  it("sanitizes input", () => {
    expect(sanitizeName("TV an/aus")).toBe("TV_anaus");
    expect(sanitizeName("Volume ")).toBe("Volume_"); // typing a space must not be lost
    expect(sanitizeName("lauter äö")).toBe("lauter_");
  });
  it("validates", () => {
    expect(nameError("")).not.toBeNull();
    expect(nameError("learn")).not.toBeNull();
    expect(nameError("tv_power-2")).toBeNull();
    expect(nameError("a b")).not.toBeNull();
  });
});
