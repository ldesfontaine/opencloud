import { expect, test } from "vitest";
import { pinnedKeyState, type ServerKey } from "./actions";

const rotating: ServerKey = { public_key: "x", fingerprint: "SHA256:new", previous_fingerprint: "SHA256:old" };

// Pendant une rotation, la clé précédente est encore bonne ; une autre ne
// l'est pas.
test("pinnedKeyState tells the current, the previous and another key apart", () => {
  expect(pinnedKeyState(rotating, "SHA256:new")).toBe("current");
  expect(pinnedKeyState(rotating, "SHA256:old")).toBe("previous");
  expect(pinnedKeyState(rotating, "SHA256:other")).toBe("differs");
  expect(pinnedKeyState({ ...rotating, previous_fingerprint: null }, "SHA256:old")).toBe("differs");
  expect(pinnedKeyState(null, "SHA256:old")).toBe("current");
});
