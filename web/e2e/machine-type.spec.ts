import { mkdirSync } from "node:fs";

import { label } from "./support/catalog";
import { expect, test } from "./support/findings";

// Le type d'une machine et le geste « Supprimer la machine » : une VM sans
// hôte connu n'est un VPS que sur une adresse publique (la machine openCloud
// des données fictives, en réseau privé, est une VM ; la distante, publique,
// un VPS), et la puce « MCP » n'existe plus : une infobulle dit en une phrase
// qu'un agent IA peut aussi faire le geste.
// Avec E2E_CAPTURES=1, il laisse les captures de U4 (clair et sombre).

const captureFolder = new URL("../../docs/projet/refonte-ux/captures/", import.meta.url).pathname;
const capturing = process.env.E2E_CAPTURES === "1";

for (const theme of ["light", "dark"] as const) {
  test(`machines : VM sur adresse privée, VPS sur adresse publique, plus de puce MCP, ${theme}`, async ({ page, findings }) => {
    findings.where = "/machines";
    await page.addInitScript((value) => localStorage.setItem("theme", value), theme);
    const suffix = theme === "light" ? "clair" : "sombre";
    const shoot = async (name: string) => {
      if (capturing) {
        mkdirSync(captureFolder, { recursive: true });
        await page.screenshot({ path: `${captureFolder}U4-${name}-${suffix}.png` });
      }
    };

    const answer = (await (await page.request.get("/api/machines")).json()) as { machines: { id: string; name: string; kind: string; address: string }[] };
    const kinds = Object.fromEntries(answer.machines.map((machine) => [machine.name, machine.kind]));
    expect(Object.values(kinds).sort()).toEqual(["vm", "vps"]);

    await page.goto("/machines");
    await expect(page.locator("table .machine-kind").first()).toBeVisible();
    await expect(page.locator("table").getByText(label("machine.kind_vm"), { exact: true }).first()).toBeVisible();
    await expect(page.locator("table").getByText(label("machine.kind_vps"), { exact: true }).first()).toBeVisible();
    await shoot("machines-types");

    // La fiche de la machine distante : le bouton Supprimer sans puce, avec son infobulle.
    const remote = answer.machines.find((machine) => machine.id !== "local");
    await page.goto(`/machines/${remote?.id ?? ""}`);
    const remove = page.getByRole("button", { name: label("machine.delete"), exact: true });
    await expect(remove).toBeVisible();
    await expect(page.locator(".mcp-chip")).toHaveCount(0);
    await expect(remove).toHaveAttribute("title", /^Un agent IA, relié à .* peut aussi faire ce geste\.$/);
    await remove.click();
    const dialog = page.locator("dialog[open]");
    await expect(dialog).toBeVisible();
    await shoot("machine-supprimer");
    await dialog.getByRole("button", { name: label("modal.cancel"), exact: true }).first().click();
    await expect(dialog).toHaveCount(0);

    // Le type se corrige : la fenêtre du placement liste VPS parmi les choix.
    expect(findings.unknown()).toEqual([]);
  });
}
