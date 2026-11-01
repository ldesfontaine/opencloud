import { expect, test } from "vitest";

import type { Translate } from "../i18n/context";
import { counted } from "./counted";

const t: Translate = (key, ...args) => `${key}(${args.join("|")})`;

test("un seul prend la clé du singulier, tout autre nombre celle du pluriel", () => {
  expect(counted(t, "firewall.rules", 1)).toBe("firewall.rules_one(1)");
  expect(counted(t, "firewall.rules", 0)).toBe("firewall.rules(0)");
  expect(counted(t, "firewall.rules", 14)).toBe("firewall.rules(14)");
});
