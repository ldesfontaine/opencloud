import { renderToStaticMarkup } from "react-dom/server";
import { expect, test, vi } from "vitest";

import { EnumSelect, Select } from "./Select";

// Sans serveur, la traduction rend la clé : le test lit ce qui s'affiche.
vi.mock("../i18n/context", () => ({ useT: () => (key: string) => key }));

const options = [
  { value: "m1", label: "vps-paris-1" },
  { value: "m2", label: "vps-lyon-2" },
];

test("le déclencheur est un combobox fermé qui annonce sa liste", () => {
  const markup = renderToStaticMarkup(<Select id="machine" value="m2" options={options} onChange={() => {}} />);
  expect(markup).toMatch(/^<button type="button" id="machine" class="select" role="combobox" aria-haspopup="listbox" aria-expanded="false"/);
  expect(markup).toContain(">vps-lyon-2</span>");
  // Fermé, la liste n'est pas dans la page, et rien ne la désigne.
  expect(markup).not.toContain('role="listbox"');
  expect(markup).not.toContain("aria-controls");
});

test("aucun <select> natif n'est rendu", () => {
  const markup = renderToStaticMarkup(<Select value="m1" options={options} onChange={() => {}} aria-label="Machine" />);
  expect(markup).not.toContain("<select");
  expect(markup).not.toContain("<option");
  expect(markup).toContain('aria-label="Machine"');
});

test("une valeur qu'aucune option ne porte montre l'invite, jamais une autre option", () => {
  const markup = renderToStaticMarkup(<Select value="ailleurs" options={options} onChange={() => {}} />);
  expect(markup).toContain('<span class="select-value select-placeholder">select.choose</span>');
  expect(markup).not.toContain("vps-paris-1");
  const custom = renderToStaticMarkup(<Select value="" options={[]} onChange={() => {}} placeholder="Aucune machine" />);
  expect(custom).toContain(">Aucune machine</span>");
});

test("désactivé, le déclencheur ne s'ouvre pas", () => {
  const markup = renderToStaticMarkup(<Select value="m1" options={options} onChange={() => {}} disabled />);
  expect(markup).toContain('disabled=""');
});

test("EnumSelect traduit chaque valeur par sa clé", () => {
  const markup = renderToStaticMarkup(<EnumSelect value="slack" values={["slack", "discord"]} labelKey="channel.format_" onChange={() => {}} />);
  expect(markup).toContain(">channel.format_slack</span>");
});
