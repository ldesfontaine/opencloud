import { renderToStaticMarkup } from "react-dom/server";
import { MemoryRouter } from "react-router";
import { expect, test, vi } from "vitest";

import type { StatusPage } from "../api/status";
import { ComponentForm } from "./StatusComponentForm";

vi.mock("../i18n/context", () => ({ useT: () => (key: string) => key }));

function pageOf(id: string, slug: string, title: string): StatusPage {
  return { id, slug, title, announcement: "", language: "fr", project_id: "", public_url: "", created_at: "", updated_at: "" };
}

// L'ordre du serveur (par adresse) met « Interne » avant la page demandée.
const pages = [pageOf("p-interne", "interne", "Interne"), pageOf("p-etat", "services", "État des services")];

test("à son premier rendu, le formulaire d'un composant montre la page demandée", () => {
  const markup = renderToStaticMarkup(
    <MemoryRouter>
      <ComponentForm existing={null} pageID="p-etat" pages={pages} />
    </MemoryRouter>,
  );
  expect(markup).toContain('id="component-page"');
  expect(markup).toContain(">État des services<");
});
