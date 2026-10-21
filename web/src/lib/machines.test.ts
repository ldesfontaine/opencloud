import { expect, test } from "vitest";

import type { Translate } from "../i18n/context";
import { machineSeen, machinesSubtitle } from "./machines";

const t: Translate = (key, ...args) => [key, ...args].join(":");

test("machinesSubtitle dit l'accueil sans machine, puis le compte et celles en ligne", () => {
  expect(machinesSubtitle(t, 0, 0)).toBe("overview.subtitle");
  expect(machinesSubtitle(t, 1, 1)).toBe("machines.subtitle_one:1");
  expect(machinesSubtitle(t, 4, 3)).toBe("machines.subtitle:4:3");
});

test("machineSeen : en ligne, vue à l'instant ; hors ligne, depuis son dernier signal", () => {
  const now = Date.parse("2026-09-12T12:00:00Z");
  expect(machineSeen(t, { online: true, last_seen_at: "2026-09-12T11:50:00Z" }, now)).toBe("machine.seen_now");
  expect(machineSeen(t, { online: false, last_seen_at: "2026-09-12T11:59:48Z" }, now)).toBe("machine.seen_ago:time.seconds:12");
  expect(machineSeen(t, { online: false, last_seen_at: null }, now)).toBe("machine.never_seen");
});
