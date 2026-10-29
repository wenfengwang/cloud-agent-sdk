import { describe, expect, it } from "vitest";
import { compact, formatDuration, formatUsage, initials } from "@/lib/state/format";

describe("format", () => {
  it("formats durations", () => {
    expect(formatDuration(4_200)).toBe("4s");
    expect(formatDuration(63_000)).toBe("1m 3s");
    expect(formatDuration(3_720_000)).toBe("1h 2m");
  });
  it("formats counts and usage", () => {
    expect(compact(412)).toBe("412");
    expect(compact(1830)).toBe("1.8k");
    expect(compact(20480)).toBe("20k");
    expect(formatUsage({ inputTokens: 1830, outputTokens: 412, cacheReadTokens: 20480, cacheWriteTokens: 0 })).toBe(
      "1.8k in · 412 out · 20k cache read",
    );
  });
  it("derives initials", () => {
    expect(initials("Alex Rivera")).toBe("AR");
    expect(initials("alex")).toBe("A");
  });
});
