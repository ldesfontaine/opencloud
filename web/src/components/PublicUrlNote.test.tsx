import { renderToStaticMarkup } from "react-dom/server";
import { expect, test, vi } from "vitest";

import { PublicUrlNote } from "./PublicUrlNote";

vi.mock("../i18n/context", () => ({ useT: () => (key: string) => key }));

test("une adresse publique ne dit rien", () => {
  expect(renderToStaticMarkup(<PublicUrlNote local={false} suggestion="" />)).toBe("");
});

test("un essai local sans suggestion : la forme attendue en exemple, aucun bouton Copier sur la ligne exemple", () => {
  const markup = renderToStaticMarkup(<PublicUrlNote local suggestion="" />);
  expect(markup).toContain("note-neutral");
  expect(markup).not.toContain("note-danger");
  expect(markup).toContain("public_url.local_note");
  expect(markup).toContain("public_url.example_label");
  expect(markup).toContain("public_url.example_line");
  // La ligne exemple n'est pas une boîte à copier ; seule la commande de relance l'est.
  expect(markup.match(/token-box/g)).toHaveLength(1);
  expect(markup.match(/machine\.copy/g)).toHaveLength(1);
  expect(markup).toContain("public_url.restart_command");
  // Aucune ligne invalide offerte à la copie.
  expect(markup).not.toContain("https://…");
});

test("une page publique avec une adresse locale : rouge, la ligne à copier porte l'adresse de la page", () => {
  const markup = renderToStaticMarkup(<PublicUrlNote local suggestion="https://cloud.exemple.fr" />);
  expect(markup).toContain("note-danger");
  expect(markup).toContain("public_url.mismatch_note");
  expect(markup).toContain("public_url = &quot;https://cloud.exemple.fr&quot;");
  expect(markup).not.toContain("public_url.example_line");
  // La ligne et la commande de relance se copient : deux boutons.
  expect(markup.match(/machine\.copy/g)).toHaveLength(2);
});
