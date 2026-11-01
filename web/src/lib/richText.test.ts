import { expect, test } from "vitest";

import { splitLinks } from "./richText";

test("un texte sans lien reste un seul morceau", () => {
  expect(splitLinks("Rien à suivre.")).toEqual([{ kind: "text", text: "Rien à suivre." }]);
});

test("un renvoi de menu devient un lien, le texte autour est gardé", () => {
  expect(splitLinks("Allez dans [Paramètres → E-mails](/parametres#e-mails) puis revenez.")).toEqual([
    { kind: "text", text: "Allez dans " },
    { kind: "link", label: "Paramètres → E-mails", to: "/parametres#e-mails" },
    { kind: "text", text: " puis revenez." },
  ]);
});

test("une adresse extérieure ou protocole-relatif n'est jamais un lien", () => {
  expect(splitLinks("[x](https://example.org) [y](//example.org)")).toEqual([{ kind: "text", text: "[x](https://example.org) [y](//example.org)" }]);
});

test("deux renvois dans une phrase", () => {
  expect(splitLinks("[A](/a) et [B](/b)").map((part) => part.kind)).toEqual(["link", "text", "link"]);
});
