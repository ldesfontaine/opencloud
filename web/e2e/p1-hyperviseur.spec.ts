import { mkdirSync } from "node:fs";

import { label } from "./support/catalog";
import { expect, test } from "./support/findings";

// L'onglet Hyperviseur en cartes (P1). Deux hôtes fictifs : celui de la machine
// distante est lu avec un jeton PVEAuditor (les archives ne sont pas lisibles :
// la fiche dit le droit qui manque, les VM disent « Inconnue »), celui de la
// machine openCloud avec le droit de lecture (chaque VM dit sa dernière
// sauvegarde). Chaque bouton est cliqué, chaque menu ouvert avec un choix,
// chaque pli ouvert. Avec E2E_CAPTURES=1, il laisse les captures P1-*.png
// (clair et sombre).

const captureFolder = new URL("../../docs/projet/refonte-ux/captures/", import.meta.url).pathname;
const capturing = process.env.E2E_CAPTURES === "1";

interface MachineRef {
  id: string;
  name: string;
}

for (const theme of ["light", "dark"] as const) {
  test(`hyperviseur : jeton PVEAuditor, droit manquant dit en clair, ${theme}`, async ({ page, findings }) => {
    await page.addInitScript((value) => localStorage.setItem("theme", value), theme);
    const suffix = theme === "light" ? "clair" : "sombre";
    const shoot = async (name: string, fullPage = true) => {
      if (capturing) {
        mkdirSync(captureFolder, { recursive: true });
        await page.screenshot({ path: `${captureFolder}P1-${name}-${suffix}.png`, fullPage });
      }
    };
    const answer = (await (await page.request.get("/api/machines")).json()) as { machines: MachineRef[] };
    const remote = answer.machines.find((machine) => machine.name === "vps-paris-1");
    findings.where = `/machines/${remote?.id ?? ""}/hyperviseur`;
    await page.goto(findings.where);

    // Le droit qui manque, avec les commandes exactes ; jamais « Aucune ».
    await expect(page.getByText(label("machine.backup_blind_title"), { exact: true })).toBeVisible();
    await expect(page.getByText("pveum role add LectureSauvegardes", { exact: false })).toBeVisible();
    await expect(page.getByText("pveum acl modify /storage/pi-sauvegardes --roles LectureSauvegardes --tokens 'lecteur@pve!lecture'", { exact: true })).toBeVisible();
    await expect(page.getByText(label("machine.proxmox_backup_unknown"), { exact: true }).first()).toBeVisible();
    await expect(page.getByText(label("machine.proxmox_no_backup"), { exact: true })).toHaveCount(0);
    await expect(page.getByText(label("machine.proxmox_storage_blind"), { exact: true })).toBeVisible();

    // Chaque bouton « Copier » se clique (le presse-papiers peut être refusé en test).
    for (const button of await page.getByRole("button", { name: label("machine.copy"), exact: true }).all()) {
      await button.click();
    }

    // Les arrêtées sont repliées ; les modèles à part, repliés aussi.
    const stopped = page.locator("details.fold", { hasText: label("machine.proxmox_stopped") });
    await expect(stopped).not.toHaveAttribute("open", "");
    await stopped.locator("summary").click();
    await expect(page.getByText("windows-test", { exact: true })).toBeVisible();
    const templates = page.locator("details.fold", { hasText: label("machine.proxmox_templates") });
    await templates.locator("summary").click();
    await expect(page.getByText("debian-12 · 9000", { exact: true })).toBeVisible();

    // Le menu d'une VM : ouvert, un geste choisi, sa confirmation refermée.
    await page.getByRole("button", { name: "Plus d'actions : immich" }).click();
    const menu = page.getByRole("menu");
    await expect(menu).toBeVisible();
    // La machine est hors ligne : les gestes restent au menu, grisés, avec la raison.
    await expect(menu.getByRole("menuitem", { name: new RegExp(label("machine.proxmox_gesture_shutdown")) })).toBeDisabled();
    await expect(menu.getByText(label("machine.proxmox_blocked_no_agent"))).toHaveCount(0);
    await shoot("hyperviseur-menu", false);
    await page.keyboard.press("Escape");
    await expect(menu).toHaveCount(0);

    // « Relire maintenant » relit l'hôte et laisse la fiche à l'écran.
    await page.getByRole("button", { name: label("machine.backup_blind_refresh"), exact: true }).click();
    await expect(page.getByText(label("machine.backup_blind_title"), { exact: true })).toBeVisible();
    await shoot("hyperviseur-droit-manquant");

    // Retirer l'hôte : la fenêtre s'ouvre, on la referme sans retirer.
    await page.getByRole("button", { name: label("machine.proxmox_remove"), exact: true }).click();
    const dialog = page.locator("dialog[open]");
    await expect(dialog).toBeVisible();
    await shoot("hyperviseur-retrait");
    await dialog.getByRole("button", { name: label("modal.cancel"), exact: true }).first().click();
    await expect(dialog).toHaveCount(0);

    // Modifier mène au formulaire de l'hôte.
    await page.getByRole("link", { name: label("machine.proxmox_edit"), exact: true }).click();
    await expect(page).toHaveURL(/\/proxmox$/);
    expect(findings.unknown()).toEqual([]);
  });

  test(`hyperviseur : jeton qui lit les archives, dernière sauvegarde de chaque VM, ${theme}`, async ({ page, findings }) => {
    await page.addInitScript((value) => localStorage.setItem("theme", value), theme);
    const suffix = theme === "light" ? "clair" : "sombre";
    // Le second hôte : celui de l'hôte déjà déclaré, relu avec le jeton « !complet »,
    // qui lit aussi les archives. Il est retiré à la fin : l'état est rendu tel quel.
    const answer = (await (await page.request.get("/api/machines")).json()) as { machines: MachineRef[] };
    const remote = answer.machines.find((machine) => machine.name === "vps-paris-1");
    const declared = (await (await page.request.get(`/api/machines/${remote?.id ?? ""}/proxmox`)).json()) as { api_url: string; pin: string };
    const put = await page.request.put("/api/machines/local/proxmox", { data: { api_url: declared.api_url, token_id: "lecteur@pve!complet", token_secret: "0a1b2c3d-4e5f-6071-8293-a4b5c6d7e8f9", pin: declared.pin } });
    expect(put.ok()).toBe(true);
    findings.where = "/machines/local/hyperviseur";
    await page.goto(findings.where);

    await expect(page.getByText(label("machine.backup_blind_title"), { exact: true })).toHaveCount(0);
    await expect(page.getByText(label("machine.proxmox_storage_receives"), { exact: true })).toBeVisible();
    await expect(page.getByText(label("machine.proxmox_unlinked"), { exact: true }).first()).toBeVisible();
    // 9 h : récente, la pastille est verte ; windows-test n'a jamais été sauvegardée.
    await expect(page.getByText("il y a 9 h", { exact: true }).first()).toBeVisible();
    await page.locator("details.fold", { hasText: label("machine.proxmox_stopped") }).locator("summary").click();
    await expect(page.getByText(label("machine.proxmox_no_backup"), { exact: true })).toHaveCount(1);
    // Le menu d'une VM de la machine openCloud (hors ligne) : gestes grisés, raison dite.
    await page.getByRole("button", { name: "Plus d'actions : vpn" }).click();
    const menu = page.getByRole("menu");
    await expect(menu).toBeVisible();
    await expect(menu.getByRole("menuitem").first()).toBeDisabled();
    await page.keyboard.press("Escape");
    await expect(menu).toHaveCount(0);
    if (process.env.E2E_CAPTURES === "1") {
      mkdirSync(captureFolder, { recursive: true });
      await page.screenshot({ path: `${captureFolder}P1-hyperviseur-cartes-${suffix}.png`, fullPage: true });
    }
    const removal = await page.request.post("/api/machines/local/actions/remove-proxmox", { headers: { "Content-Type": "application/json" }, data: {} });
    expect(removal.status(), await removal.text()).toBe(204);
    expect(findings.unknown()).toEqual([]);
  });
}
