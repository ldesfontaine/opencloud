import { expect, test } from "vitest";

import { format } from "./format";

test("format remplit %s et %d dans l'ordre", () => {
  expect(format("%d machines, %d en ligne.", [3, 1])).toBe("3 machines, 1 en ligne.");
  expect(format("vu il y a %s", ["12 s"])).toBe("vu il y a 12 s");
});

test("format laisse un verbe sans argument tel quel", () => {
  expect(format("code %d", [])).toBe("code %d");
  expect(format("sans verbe", [1])).toBe("sans verbe");
});

test("format lit %% comme un pourcentage, sans consommer d'argument", () => {
  expect(format("Disque à %d %%", [91])).toBe("Disque à 91 %");
  expect(format("%s on %s. Threshold %d %%.", ["/data", "vps", 85])).toBe("/data on vps. Threshold 85 %.");
  expect(format("seuil à 85 %%", [])).toBe("seuil à 85 %");
});

test("format ne confond pas %%d avec un verbe", () => {
  expect(format("100%%d", [7])).toBe("100%d");
});
