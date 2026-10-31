import { expect, test } from "vitest";

import { productName, wordmarkParts } from "./product";

// Une fausse page : la balise que le serveur remplit, ou rien.
function page(content: string | null): Document {
  const meta = content === null ? null : { getAttribute: () => content };
  return { querySelector: (selector: string) => (selector === 'meta[name="application-name"]' ? meta : null) } as unknown as Document;
}

test("productName lit le nom posé par le serveur dans la page", () => {
  expect(productName(page("openCloud"))).toBe("openCloud");
});

test("productName rend une chaîne vide quand la balise manque", () => {
  expect(productName(page(null))).toBe("");
});

test("wordmarkParts coupe le nom à sa majuscule intérieure", () => {
  expect(wordmarkParts("openCloud")).toEqual({ light: "open", bold: "Cloud" });
});

test("wordmarkParts garde d'un seul poids un nom sans majuscule intérieure", () => {
  expect(wordmarkParts("Atelier")).toEqual({ light: "Atelier", bold: "" });
  expect(wordmarkParts("")).toEqual({ light: "", bold: "" });
});
