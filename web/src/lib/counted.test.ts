import { expect, test } from "vitest";

import type { Translate } from "../i18n/context";
import { counted } from "./counted";

const t: Translate = (key, ...args) => `${key}(${args.join("|")})`;

test("un seul prend la clé du singulier, tout autre nombre celle du pluriel", () => {
  expect(counted(t, "firewall.rules_count", 1)).toBe("firewall.rules_count_one(1)");
  expect(counted(t, "firewall.rules_count", 0)).toBe("firewall.rules_count(0)");
  expect(counted(t, "firewall.rules_count", 14)).toBe("firewall.rules_count(14)");
});
