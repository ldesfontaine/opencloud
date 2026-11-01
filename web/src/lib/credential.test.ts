import { expect, test } from "vitest";

import { maskCredential } from "./credential";

test("ne laisse voir que les quatre premiers caractères", () => {
  expect(maskCredential("AbCdEfGhIjKl")).toBe("AbCd…");
  expect(maskCredential("lecteur@pve!lecture")).toBe("lect…");
  expect(maskCredential("éàüöxyz")).toBe("éàüö…");
});

test("une valeur courte ne se montre pas du tout", () => {
  expect(maskCredential("abcd")).toBe("…");
  expect(maskCredential("")).toBe("…");
});
