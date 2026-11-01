import { renderToStaticMarkup } from "react-dom/server";
import { expect, test, vi } from "vitest";

import { PublicUrlNote } from "./PublicUrlNote";

vi.mock("../i18n/context", () => ({ useT: () => (key: string) => key }));

test("une adresse publique ne dit rien", () => {
  expect(renderToStaticMarkup(<PublicUrlNote local={false} suggestion="" />)).toBe("");
});

test("un essai local : note neutre, la ligne à poser et le redémarrage", () => {
  const markup = renderToStaticMarkup(<PublicUrlNote local suggestion="" />);
  expect(markup).toContain("note-neutral");
  expect(markup).not.toContain("note-danger");
  expect(markup).toContain("public_url.local_note");
  expect(markup).toContain("public_url = &quot;https://…&quot;");
  expect(markup).toContain("public_url.restart_command");
});

test("une page publique avec une adresse locale : rouge, la ligne porte l'adresse de la page", () => {
  const markup = renderToStaticMarkup(<PublicUrlNote local suggestion="https://cloud.exemple.fr" />);
  expect(markup).toContain("note-danger");
  expect(markup).toContain("public_url.mismatch_note");
  expect(markup).toContain("public_url = &quot;https://cloud.exemple.fr&quot;");
});
