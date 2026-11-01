import { label } from "./support/catalog";
import { createFirstAccount, mockFirstSetup } from "./support/firstSetup";
import { expect, test } from "./support/findings";

// Le chantier U1 (#447) : l'installation d'une machine en étapes copiables, un
// refus de droit qui porte son geste, et un sélecteur de machine qui grise
// celles qui ne conviennent pas. Chaque geste est joué comme un opérateur le
// ferait, sur les données fictives.

test("Ajouter une machine : cinq étapes, un bloc chacune, le jeton une seule fois", async ({ page, findings }) => {
  findings.where = "/machines/nouvelle";
  await page.goto("/machines/nouvelle");
  const dialog = page.locator("dialog[open]");
  await dialog.getByLabel(label("machine.name_label")).fill("vps-essai-1");
  await dialog.getByText(label("machine.rights_advanced")).click();
  await dialog.getByLabel(label("machine.capability_docker_deploy")).check();
  await dialog.getByLabel(label("machine.capability_backup_receive")).check();
  await dialog.locator("#machine-backup-path").fill("/srv/sauvegardes");
  await dialog.getByRole("button", { name: label("machine.create_token") }).click();

  // Les cinq étapes, dans l'ordre où on les colle, chacune avec son titre.
  const titles = ["binary", "prepare", "enroll", "unit", "rights"].map((id) => label(`machine.install_${id}_title`));
  const items = dialog.locator(".checklist-item");
  await expect(items).toHaveCount(5);
  for (const [index, title] of titles.entries()) {
    await expect(items.nth(index)).toContainText(title);
    // Un seul bloc à copier par étape, et son bouton.
    await expect(items.nth(index).locator(".token-box")).toHaveCount(1);
    await expect(items.nth(index).getByRole("button", { name: label("machine.copy") })).toBeVisible();
  }

  // Aucune documentation à côté : la fenêtre ne renvoie nulle part.
  await expect(dialog).not.toContainText(/documentation d'installation|voir la documentation/i);
  await expect(dialog.getByRole("link")).toHaveCount(0);

  // Le jeton n'est que dans la commande de l'enrôlement.
  const text = (await dialog.innerText()).replace(/\s+/g, " ");
  expect(text.match(/oc_[a-z0-9_]{8,}/g)?.length).toBe(1);
  await expect(items.nth(2).locator(".token-box")).toContainText("oc_");
  // Les lignes longues passent à la ligne : rien ne déborde du bloc.
  const overflowing = await dialog.locator(".token-box pre").evaluateAll((blocks) => blocks.filter((block) => block.scrollWidth > block.clientWidth).length);
  expect(overflowing).toBe(0);

  // Chaque bouton « Copier » répond (copié, ou copie refusée par le navigateur sans terminal).
  await items.nth(0).getByRole("button", { name: label("machine.copy") }).click();
  await expect(items.nth(0).getByRole("button", { name: new RegExp(`${label("machine.copied")}|${label("machine.copy_failed")}`) })).toBeVisible();

  // Une case cochée reste cochée quand on va à l'attente et qu'on revient.
  await items.nth(0).getByRole("checkbox").check();
  await dialog.getByRole("button", { name: label("wizard.next") }).click();
  // L'horloge des données fictives est figée : le jeton y est déjà expiré, ce qui est aussi une attente.
  await expect(dialog.locator(".wizard-step-current")).toContainText(label("machine.step_wait"));
  await dialog.getByRole("button", { name: label("wizard.previous") }).click();
  await expect(items.nth(0).getByRole("checkbox")).toBeChecked();

  await dialog.getByRole("button", { name: label("wizard.next") }).click();
  await dialog.locator(".modal-foot").getByRole("button", { name: label("modal.close") }).click();
  await expect(dialog).toHaveCount(0);
  expect(findings.unknown()).toEqual([]);
});

test("Hôte Proxmox sans sudo : l'installation et la migration se collent en root, binaire téléchargé et vérifié", async ({ page, findings }) => {
  findings.where = "/machines/nouvelle (rôle Proxmox)";
  await page.goto("/machines/nouvelle");
  const dialog = page.locator("dialog[open]");
  await dialog.getByLabel(label("machine.name_label")).fill("pve-sans-sudo");
  await dialog.locator("#machine-role").click();
  await page.locator("[role=listbox] [role=option]").filter({ hasText: label("role.proxmox") }).click();
  await dialog.getByRole("button", { name: label("machine.create_token") }).click();
  const steps = dialog.locator(".checklist").first();
  await expect(steps).toContainText(label("machine.install_binary_title"));
  // Aucun bloc ne nomme sudo, et celui du binaire se télécharge et se vérifie.
  const blocks = (await steps.locator(".token-box pre").allInnerTexts()).join("\n");
  expect(blocks).not.toMatch(/\bsudo\b/);
  expect(blocks).toContain("curl -fLO");
  expect(blocks).toContain("sha256sum -c --ignore-missing SHA256SUMS");
  expect(blocks).toContain("runuser -u opencloud-agent --");
  await dialog.getByRole("button", { name: label("wizard.next") }).click();
  await dialog.locator(".modal-foot").getByRole("button", { name: label("modal.close") }).click();

  // La fiche d'un hôte Proxmox dont l'agent n'est pas migré : le bloc est en root d'office.
  await page.route("**/api/machines/*/agent-update", (route) =>
    route.fulfill({ json: { current: "v0.0.1", target: "v0.0.2", arch: "amd64", available: false, reason: "rollback_unavailable" } }),
  );
  await page.route("**/api/machines", async (route) => {
    const response = await route.fetch();
    const body = (await response.json()) as { machines: { role: string }[] };
    body.machines.forEach((machine) => (machine.role = "proxmox"));
    await route.fulfill({ response, json: body });
  });
  await page.goto("/machines");
  await page.locator("table a.svc").first().click();
  await page.goto(`${new URL(page.url()).pathname.replace(/\/$/, "")}/agent`);
  const migration = page.locator(".note", { hasText: label("machine.agent_migration_title") });
  await expect(migration.getByRole("button", { name: label("machine.agent_migration_root") })).toHaveAttribute("aria-pressed", "true");
  let block = await migration.locator(".token-box pre").innerText();
  expect(block).not.toMatch(/\bsudo\b/);
  expect(block).toContain("sha256sum -c --ignore-missing SHA256SUMS");
  expect(block).toContain("runuser -u opencloud-agent -- install");
  // L'autre variante reste au choix.
  await migration.getByRole("button", { name: label("machine.agent_migration_sudo") }).click();
  block = await migration.locator(".token-box pre").innerText();
  expect(block).toContain("sudo systemctl stop opencloud-agent.service");
  expect(findings.unknown()).toEqual([]);
});

test("Destination de sauvegarde : les machines sans le droit sont grisées, avec la raison et le lien", async ({ page, findings }) => {
  findings.where = "/sauvegardes/destinations/nouvelle";
  await page.goto("/sauvegardes/destinations/nouvelle");
  const dialog = page.locator("dialog[open]");
  await dialog.locator("#backup-destination-machine").click();
  const list = page.locator("[role=listbox]").last();
  const grayed = list.locator("[role=option][aria-disabled=true]");
  const usable = list.locator("[role=option]:not([aria-disabled=true])");
  expect(await grayed.count()).toBeGreaterThan(0);
  expect(await usable.count()).toBeGreaterThan(0);
  const first = grayed.first();
  const note = list.locator(".select-option-group").first();
  await expect(note.locator(".select-option-reason")).toContainText(label("machine.fit_right_missing").replace("%s", "").trim().slice(0, 10));
  // Cliquer la ligne grisée ne la choisit pas.
  await first.click({ position: { x: 8, y: 8 }, force: true });
  await expect(list).toBeVisible();
  // Le lien mène à « Ré-enrôler » avec le droit déjà coché.
  await note.getByRole("link", { name: label("machine.reenroll_with_right") }).click();
  await expect(page).toHaveURL(/\/machines\/[^/]+\/reenroller\?droit=backup\.receive/);
  const reenroll = page.locator("dialog[open]");
  await expect(reenroll.getByRole("checkbox", { name: label("machine.capability_backup_receive") })).toBeChecked();
  await expect(reenroll.getByRole("checkbox", { name: label("machine.capability_observe") })).toBeChecked();

  // Créer le jeton : la première étape est d'arrêter l'agent en place.
  await reenroll.locator("#machine-backup-path").fill("/srv/sauvegardes");
  await reenroll.getByRole("button", { name: label("machine.rights_submit") }).click();
  const items = reenroll.locator(".checklist-item");
  await expect(items.first()).toContainText(label("machine.install_stop_title"));
  await reenroll.getByRole("button", { name: label("wizard.next") }).click();
  await reenroll.locator(".modal-foot").getByRole("button", { name: label("modal.close") }).click();
  expect(findings.unknown()).toEqual([]);
});

test("Sonde : une machine sans agent est grisée dans le sélecteur", async ({ page, findings }) => {
  findings.where = "/disponibilite/nouvelle";
  await page.goto("/disponibilite/nouvelle");
  const dialog = page.locator("dialog[open]");
  await dialog.locator("details.fold > summary").first().click();
  await dialog.locator("#probe-machine").click();
  const list = page.locator("[role=listbox]").last();
  await expect(list.locator("[role=option]").first()).toBeVisible();
  // Les options grisées, s'il y en a, portent toutes leur raison et leur lien.
  const grayed = list.locator("[role=option][aria-disabled=true]");
  for (let index = 0; index < (await grayed.count()); index++) {
    await expect(list.locator(".select-option-group .select-option-reason a").nth(index)).toBeVisible();
  }
  await page.keyboard.press("Escape");
  expect(findings.unknown()).toEqual([]);
});

test("Première configuration : le compte créé, les étapes de l'agent se collent, rien ne renvoie à la documentation", async ({ page, findings }) => {
  findings.where = "/ (première configuration)";
  await mockFirstSetup(page);
  await page.goto("/");
  await createFirstAccount(page);
  const items = page.locator(".checklist-item");
  await expect(items).toHaveCount(3);
  for (const id of ["prepare", "enroll", "unit"]) {
    await expect(page.locator(".checklist")).toContainText(label(`machine.install_${id}_title`));
  }
  await expect(page.locator("main")).not.toContainText(/documentation d'installation|voir la documentation/i);
  // Chaque bloc a son bouton « Copier » ; le jeton n'est que dans l'enrôlement.
  await expect(page.getByRole("button", { name: label("machine.copy") })).toHaveCount(3);
  expect(((await page.locator("main").innerText()).match(/oc_exemple_de_jeton/g) ?? []).length).toBe(1);
  await items.first().getByRole("checkbox").check();
  await page.getByRole("button", { name: label("auth.setup_continue") }).click();
  expect(findings.unknown()).toEqual([]);
});
