import { expect, test } from "vitest";

import type { Translate } from "../i18n/context";
import { machinesSubtitle } from "./machines";

const t: Translate = (key, ...args) => [key, ...args].join(":");

test("machinesSubtitle dit l'accueil sans machine, puis le compte et celles en ligne", () => {
  expect(machinesSubtitle(t, 0, 0)).toBe("overview.subtitle");
  expect(machinesSubtitle(t, 1, 1)).toBe("machines.subtitle_one:1");
  expect(machinesSubtitle(t, 4, 3)).toBe("machines.subtitle:4:3");
});
