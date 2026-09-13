import { expect, test } from "vitest";

import type { Translate } from "../i18n/context";
import { deadlineIn, formatDuration, formatRemaining, lastPing, seenAgo } from "./time";

// Un faux catalogue : la clé et ses arguments, pour lire ce qui est demandé.
const t: Translate = (key, ...args) => [key, ...args].join(":");

const now = Date.parse("2026-09-12T12:00:00Z");

test("formatDuration arrondit à l'unité qui se lit", () => {
  expect(formatDuration(t, 400)).toBe("time.milliseconds:400");
  expect(formatDuration(t, 12_000)).toBe("time.seconds:12");
  expect(formatDuration(t, 5 * 60_000)).toBe("time.minutes:5");
  expect(formatDuration(t, 24 * 3_600_000)).toBe("time.hours:24");
  expect(formatDuration(t, 49 * 3_600_000)).toBe("time.days:2");
  expect(formatDuration(t, -5)).toBe("time.milliseconds:0");
});

test("formatRemaining arrondit vers le haut", () => {
  expect(formatRemaining(t, 24 * 3_600_000 - 900)).toBe("time.hours:24");
  expect(formatRemaining(t, 4 * 60_000 + 1)).toBe("time.minutes:5");
  expect(formatDuration(t, 4 * 60_000 + 1)).toBe("time.minutes:4");
});

test("seenAgo distingue jamais vu, à l'instant et il y a", () => {
  expect(seenAgo(t, null, now)).toBe("machine.never_seen");
  expect(seenAgo(t, "2026-09-12T12:00:00Z", now)).toBe("machine.seen_now");
  expect(seenAgo(t, "2026-09-12T11:59:48Z", now)).toBe("machine.seen_ago:time.seconds:12");
});

test("lastPing et deadlineIn parlent des tâches", () => {
  expect(lastPing(t, null, now)).toBe("job.never");
  expect(lastPing(t, "2026-09-12T11:58:00Z", now)).toBe("job.ago:time.minutes:2");
  expect(deadlineIn(t, null, now)).toBe("job.no_deadline");
  expect(deadlineIn(t, "2026-09-12T11:00:00Z", now)).toBe("job.deadline_passed");
  expect(deadlineIn(t, "2026-09-12T12:03:30Z", now)).toBe("job.deadline_in:time.minutes:4");
});
