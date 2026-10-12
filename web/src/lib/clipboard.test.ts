import { afterEach, expect, test, vi } from "vitest";

import { copyText } from "./clipboard";

// Une fausse page : la zone de texte qu'on y pose, et ce que la commande
// de copie répond.
function fakeDocument(commandWorks: boolean) {
  const area = { value: "", style: {} as Record<string, string>, setAttribute: vi.fn(), select: vi.fn(), remove: vi.fn() };
  const page = {
    createElement: vi.fn(() => area),
    body: { appendChild: vi.fn() },
    execCommand: vi.fn(() => commandWorks),
  };
  vi.stubGlobal("document", page);
  return { area, page };
}

afterEach(() => vi.unstubAllGlobals());

test("copyText passe par le presse-papiers du navigateur quand il existe", async () => {
  const writeText = vi.fn(() => Promise.resolve());
  vi.stubGlobal("navigator", { clipboard: { writeText } });
  const { page } = fakeDocument(true);
  expect(await copyText("abc")).toBe(true);
  expect(writeText).toHaveBeenCalledWith("abc");
  expect(page.execCommand).not.toHaveBeenCalled();
});

test("copyText se replie sur la commande de copie hors HTTPS", async () => {
  vi.stubGlobal("navigator", {});
  const { area, page } = fakeDocument(true);
  expect(await copyText("abc")).toBe(true);
  expect(area.value).toBe("abc");
  expect(page.execCommand).toHaveBeenCalledWith("copy");
  expect(area.remove).toHaveBeenCalled();
});

test("copyText se replie aussi quand le navigateur refuse", async () => {
  vi.stubGlobal("navigator", { clipboard: { writeText: () => Promise.reject(new Error("denied")) } });
  fakeDocument(true);
  expect(await copyText("abc")).toBe(true);
});

test("copyText dit quand rien n'a pu être copié", async () => {
  vi.stubGlobal("navigator", {});
  fakeDocument(false);
  expect(await copyText("abc")).toBe(false);
});
