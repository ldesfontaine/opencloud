import { describe, expect, it } from "vitest";

import { childPath, crumbsOf, moveCursor, parentPath, shellQuote, toggleName } from "./fileExplorer";

describe("childPath et parentPath", () => {
  it("joignent et remontent, la racine tient", () => {
    expect(childPath("/", "srv")).toBe("/srv");
    expect(childPath("/srv", "app")).toBe("/srv/app");
    expect(parentPath("/srv/app")).toBe("/srv");
    expect(parentPath("/srv")).toBe("/");
    expect(parentPath("/")).toBe("/");
  });
});

describe("crumbsOf", () => {
  it("rend un maillon par dossier, avec son chemin", () => {
    expect(crumbsOf("/srv/appli")).toEqual([
      { name: "srv", path: "/srv" },
      { name: "appli", path: "/srv/appli" },
    ]);
    expect(crumbsOf("/")).toEqual([]);
  });
});

describe("toggleName", () => {
  it("coche puis décoche", () => {
    expect(toggleName([], "a")).toEqual(["a"]);
    expect(toggleName(["a", "b"], "a")).toEqual(["b"]);
  });
});

describe("moveCursor", () => {
  it("ne sort pas de la liste", () => {
    expect(moveCursor(0, -1, 3)).toBe(0);
    expect(moveCursor(2, 1, 3)).toBe(2);
    expect(moveCursor(-1, 1, 3)).toBe(0);
    expect(moveCursor(0, 1, 0)).toBe(-1);
  });
});

describe("shellQuote", () => {
  it("protège les espaces, les apostrophes et les caractères du shell", () => {
    expect(shellQuote("a b")).toBe("'a b'");
    expect(shellQuote("l'an $(rm -rf /)")).toBe("'l'\\''an $(rm -rf /)'");
  });
});
