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
