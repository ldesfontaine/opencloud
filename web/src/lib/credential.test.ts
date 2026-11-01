import { expect, test } from "vitest";

import type { Translate } from "../i18n/context";
import { credentialText, hintText } from "./credential";

const t: Translate = (key) => key;

test("ne laisse voir que les quatre premiers caractères", () => {
  expect(credentialText(t, "AbCdEfGhIjKl")).toBe("AbCd…");
  expect(credentialText(t, "lecteur@pve!lecture")).toBe("lect…");
  expect(credentialText(t, "éàüöxyz")).toBe("éàüö…");
});

test("une valeur courte ne se montre pas du tout", () => {
  expect(credentialText(t, "abcd")).toBe("…");
});

test("sans valeur, un libellé dit pourquoi : jamais un « … » seul", () => {
  expect(credentialText(t, "")).toBe("credential.not_shown");
  expect(credentialText(t, "   ")).toBe("credential.not_shown");
  expect(hintText(t, "")).toBe("credential.not_shown");
  expect(hintText(t, "AbCd…")).toBe("AbCd…");
});
