import { expect, test } from "vitest";

import { closedMenu, filterOptions, fitMenu, hasSearch, openMenu, placeMenu, pressKey, searchMenu, typeaheadIndex, type KeyContext, type MenuState, type SelectOption } from "./select";

const machines: SelectOption[] = [
  { value: "m1", label: "vps-paris-1" },
  { value: "m2", label: "vps-lyon-2" },
  { value: "m3", label: "nas-maison" },
  { value: "m4", label: "État des services" },
];

function onTrigger(value: string, visible: readonly SelectOption[] = machines): KeyContext {
  return { visible, value, searchable: false, source: "trigger" };
}

test("ouvrir rend active l'option choisie, sinon la première", () => {
  expect(openMenu(machines, "m3")).toEqual({ isOpen: true, active: 2, query: "", typed: "" });
  expect(openMenu(machines, "inconnue").active).toBe(0);
  expect(openMenu([], "").active).toBe(-1);
});

test("fermé, ↓ ↑ Entrée et Espace ouvrent ; Début et Fin vont aux bouts", () => {
  for (const key of ["ArrowDown", "ArrowUp", "Enter", " "]) {
    expect(pressKey(closedMenu, key, onTrigger("m2"))?.state).toEqual({ isOpen: true, active: 1, query: "", typed: "" });
  }
  expect(pressKey(closedMenu, "Home", onTrigger("m2"))?.state.active).toBe(0);
  expect(pressKey(closedMenu, "End", onTrigger("m2"))?.state.active).toBe(3);
});

test("fermé, Tab, Échap et les flèches de côté restent au navigateur", () => {
  for (const key of ["Tab", "Escape", "ArrowLeft", "Shift"]) {
    expect(pressKey(closedMenu, key, onTrigger("m1"))).toBeNull();
  }
});

test("ouvert, ↓ et ↑ déplacent l'option active sans sortir de la liste", () => {
  const open = openMenu(machines, "m1");
  expect(pressKey(open, "ArrowUp", onTrigger("m1"))?.state.active).toBe(0);
  const down = pressKey(open, "ArrowDown", onTrigger("m1"))?.state as MenuState;
  expect(down.active).toBe(1);
  const atEnd = { ...open, active: 3 };
  expect(pressKey(atEnd, "ArrowDown", onTrigger("m1"))?.state.active).toBe(3);
});

test("Entrée et Espace choisissent l'option active, ferment et rendent le focus", () => {
  const open = { ...openMenu(machines, "m1"), active: 2 };
  for (const key of ["Enter", " "]) {
    expect(pressKey(open, key, onTrigger("m1"))).toEqual({ state: closedMenu, chosen: "m3", returnsFocus: true });
  }
});

test("Échap ferme sans choisir et rend le focus ; Tab ferme et laisse passer au champ suivant", () => {
  const open = { ...openMenu(machines, "m1"), active: 2 };
  expect(pressKey(open, "Escape", onTrigger("m1"))).toEqual({ state: closedMenu, returnsFocus: true });
  expect(pressKey(open, "Tab", onTrigger("m1"))).toEqual({ state: closedMenu, returnsFocus: true, keepsDefault: true });
});

test("une lettre saute à la première option qui commence par elle, la même lettre passe à la suivante", () => {
  expect(typeaheadIndex(machines, "n", 0)).toBe(2);
  expect(typeaheadIndex(machines, "v", 0)).toBe(1);
  expect(typeaheadIndex(machines, "vv", 1)).toBe(0);
  expect(typeaheadIndex(machines, "vps-l", 0)).toBe(1);
  expect(typeaheadIndex(machines, "e", 0)).toBe(3);
  expect(typeaheadIndex(machines, "z", 0)).toBe(-1);
});

test("taper ouvre la liste sur l'option tapée, et les lettres qui se suivent font un mot", () => {
  const opened = pressKey(closedMenu, "n", onTrigger("m1"))?.state as MenuState;
  expect(opened).toEqual({ isOpen: true, active: 2, query: "", typed: "n" });
  const word = pressKey({ ...opened, active: 0, typed: "vps-" }, "l", onTrigger("m1"))?.state as MenuState;
  expect(word.active).toBe(1);
  expect(word.typed).toBe("vps-l");
});

test("la recherche apparaît au-delà de huit options et filtre sans casse ni accent", () => {
  expect(hasSearch(8)).toBe(false);
  expect(hasSearch(9)).toBe(true);
  expect(filterOptions(machines, "etat").map((option) => option.value)).toEqual(["m4"]);
  expect(filterOptions(machines, "VPS").map((option) => option.value)).toEqual(["m1", "m2"]);
  expect(filterOptions(machines, "  ")).toBe(machines);
});

test("dans la recherche, l'espace et les lettres s'écrivent ; Entrée choisit la première trouvée", () => {
  const searched = searchMenu(openMenu(machines, "m1"), machines, "lyon");
  expect(searched.active).toBe(0);
  const visible = filterOptions(machines, searched.query);
  const inSearch: KeyContext = { visible, value: "m1", searchable: true, source: "search" };
  expect(pressKey(searched, " ", inSearch)).toBeNull();
  expect(pressKey(searched, "a", inSearch)).toBeNull();
  // Début et Fin déplacent le curseur du texte, pas l'option active.
  expect(pressKey(searched, "Home", inSearch)).toBeNull();
  expect(pressKey(searched, "End", inSearch)).toBeNull();
  expect(pressKey(searched, "Enter", inSearch)?.chosen).toBe("m2");
});

test("une recherche sans résultat n'a pas d'option active, et Entrée ne choisit rien", () => {
  const searched = searchMenu(openMenu(machines, "m1"), machines, "zzz");
  expect(searched.active).toBe(-1);
  const inSearch: KeyContext = { visible: [], value: "m1", searchable: true, source: "search" };
  expect(pressKey(searched, "Enter", inSearch)).toEqual({ state: closedMenu, returnsFocus: true });
});

test("fermé avec recherche, une lettre ouvre la liste et va dans la recherche", () => {
  const context: KeyContext = { visible: machines, value: "m1", searchable: true, source: "trigger" };
  expect(pressKey(closedMenu, "n", context)?.state).toEqual({ isOpen: true, active: 0, query: "n", typed: "" });
});

test("des options retirées pendant l'ouverture ramènent l'option active dans la liste", () => {
  const open = { ...openMenu(machines, "m4"), active: 3 };
  expect(fitMenu(open, 4)).toBe(open);
  expect(fitMenu(open, 2).active).toBe(1);
  expect(fitMenu(open, 0).active).toBe(-1);
});

test("la liste s'ouvre sous le déclencheur, au moins aussi large que lui", () => {
  const place = placeMenu({ top: 100, bottom: 136, left: 40, width: 240 }, { width: 1280, height: 800 });
  expect(place).toEqual({ top: 140, bottom: null, left: 40, minWidth: 240, maxWidth: 1232, maxHeight: 280 });
});

test("la liste s'ouvre dessus quand la place manque en bas", () => {
  const place = placeMenu({ top: 700, bottom: 736, left: 40, width: 240 }, { width: 1280, height: 800 });
  expect(place.top).toBeNull();
  expect(place.bottom).toBe(104);
  expect(place.maxHeight).toBe(280);
});

test("la liste ne déborde jamais de la fenêtre, à droite comme sur un téléphone", () => {
  const nearRight = placeMenu({ top: 100, bottom: 136, left: 1200, width: 240 }, { width: 1280, height: 800 });
  expect(nearRight.left).toBe(1032);
  expect(nearRight.left + nearRight.minWidth).toBeLessThanOrEqual(1272);
  const phone = placeMenu({ top: 100, bottom: 144, left: 16, width: 400 }, { width: 360, height: 640 });
  expect(phone.left).toBe(8);
  expect(phone.minWidth).toBe(344);
  expect(phone.maxWidth).toBe(344);
});
