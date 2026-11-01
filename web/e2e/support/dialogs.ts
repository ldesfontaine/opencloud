import type { Locator, Page } from "@playwright/test";

import { label } from "./catalog";
import type { Findings } from "./findings";
import { exerciseSelects } from "./selects";

// Une valeur plausible pour un champ, d'après son type et son nom. Le parcours
// ne connaît aucun formulaire : il remplit ce qui est vide comme le ferait un
// opérateur pressé.
async function plausibleValue(field: Locator): Promise<string> {
  const type = (await field.getAttribute("type")) ?? "text";
  const hint = `${(await field.getAttribute("name")) ?? ""} ${(await field.getAttribute("id")) ?? ""} ${(await field.getAttribute("placeholder")) ?? ""}`.toLowerCase();
  if (type === "number") {
    return "5";
  }
  if (type === "email") {
    return "ops@exemple.fr";
  }
  if (type === "password") {
    return "mot-de-passe-de-test-123";
  }
  if (type === "url" || /url|https?:|adresse|address|cible|target/.test(hint)) {
    return "https://exemple.fr";
  }
  if (/port/.test(hint)) {
    return "8080";
  }
  return "essai-e2e";
}

// Remplit chaque champ texte visible et vide de la zone.
async function fillEmptyFields(scope: Locator): Promise<void> {
  const fields = scope.locator("input:visible:not([type=checkbox]):not([type=radio]):not([type=hidden]):not([role=combobox]):not([readonly]):not([disabled]), textarea:visible:not([readonly]):not([disabled])");
  const count = await fields.count();
  for (let index = 0; index < count; index++) {
    const field = fields.nth(index);
    if ((await field.inputValue()) === "") {
      await field.fill(await plausibleValue(field), { timeout: 2000 }).catch(() => undefined);
    }
  }
}

// Ferme la fenêtre par « Annuler », en répondant « abandonner » si elle
// demande avant de perdre la saisie.
async function cancelDialog(dialog: Locator): Promise<void> {
  await dialog.getByRole("button", { name: label("modal.cancel"), exact: true }).first().click({ timeout: 3000 });
  const abandon = dialog.getByText(label("modal.abandon_question"));
  if (await abandon.isVisible().catch(() => false)) {
    // La question remplace le pied : son bouton de confirmation est le premier.
    await dialog.locator(".modal-foot button").first().click({ timeout: 3000 });
  }
  await dialog.waitFor({ state: "detached", timeout: 3000 }).catch(() => undefined);
}

// La barre d'étapes ne coupe aucun libellé : la barre tient dans sa largeur,
// le nom de l'étape courante aussi, et chaque autre étape garde son titre
// complet (title) sous sa pastille.
async function checkStepBar(dialog: Locator, findings: Findings): Promise<void> {
  const bar = dialog.locator(".wizard-steps");
  if (!(await bar.isVisible())) {
    return;
  }
  const problems = await bar.evaluate((list) => {
    const found: string[] = [];
    if (list.scrollWidth > list.clientWidth) {
      found.push("la barre déborde de sa largeur");
    }
    for (const step of Array.from(list.querySelectorAll(".wizard-step"))) {
      const name = step.querySelector<HTMLElement>(".wizard-name");
      const jump = step.querySelector<HTMLElement>(".wizard-jump");
      const full = name?.textContent?.trim() ?? "";
      if (step.classList.contains("wizard-step-current")) {
        if (name !== null && name.scrollWidth > name.clientWidth) {
          found.push(`« ${full} » est coupé`);
        }
      } else if (jump?.getAttribute("title") !== full) {
        found.push(`« ${full} » n'a pas son titre complet`);
      }
    }
    return found;
  });
  for (const problem of problems) {
    findings.add(`barre d'étapes : ${problem}`);
  }
}

const maxSteps = 12;

// Mène un assistant de l'étape 1 jusqu'au récapitulatif : remplit, choisit
// dans chaque menu, presse « Suivant ». Une étape qui ne s'ouvre pas est une
// anomalie (le texte de la validation est rapporté).
async function walkWizard(page: Page, dialog: Locator, findings: Findings): Promise<void> {
  const summary = dialog.locator(".wizard-summary");
  let attempts = 0;
  for (let turn = 0; turn < maxSteps * 2; turn++) {
    await checkStepBar(dialog, findings);
    if (await summary.isVisible()) {
      findings.done.wizards++;
      return;
    }
    const title = (await dialog.locator(".wizard-title").innerText()).trim();
    // Les menus d'abord : un choix peut faire paraître d'autres champs.
    await exerciseSelects(page, dialog, findings);
    await fillEmptyFields(dialog);
    await dialog.getByRole("button", { name: label("wizard.next"), exact: true }).click();
    const advanced = await dialog
      .locator(".wizard-title")
      .filter({ hasNotText: title })
      .or(summary)
      .first()
      .waitFor({ state: "visible", timeout: 2500 })
      .then(() => true)
      .catch(() => false);
    if (advanced) {
      attempts = 0;
      continue;
    }
    // Une seconde passe : le premier tour a pu faire paraître des champs.
    attempts++;
    if (attempts >= 2) {
      const refusal = (await dialog.locator("[role=alert]").first().innerText().catch(() => "")).trim();
      findings.add(`l'assistant reste à l'étape « ${title} » : ${refusal || "aucune raison affichée"}`);
      return;
    }
  }
  findings.add(`l'assistant n'atteint pas le récapitulatif en ${maxSteps} étapes`);
}

// Un assistant d'un seul écran : ses menus, ses champs, puis chaque pli
// « Réglages » ouvert avec ses menus et ses champs. Il reste prêt à écrire :
// l'action finale est là et active (le geste d'écrire est joué par
// u3-assistants-courts.spec.ts, sur des données qu'il crée).
async function walkOneScreen(page: Page, dialog: Locator, findings: Findings): Promise<void> {
  await exerciseSelects(page, dialog, findings);
  await fillEmptyFields(dialog);
  const closedFolds = dialog.locator("details.fold:not([open]) > summary");
  for (let guard = 0; guard < maxSteps && (await closedFolds.count()) > 0; guard++) {
    await closedFolds.first().click();
    await exerciseSelects(page, dialog, findings);
    await fillEmptyFields(dialog);
  }
  const submit = dialog.locator("form.wizard-modal button[type=submit]");
  if (!(await submit.isEnabled())) {
    findings.add("l'assistant d'un écran n'offre pas son action finale");
  }
  findings.done.wizards++;
}

// Sans barre d'étapes, l'assistant tient en un écran.
async function walkAnyWizard(page: Page, dialog: Locator, findings: Findings): Promise<void> {
  if ((await dialog.locator(".wizard-bar").count()) === 0) {
    await walkOneScreen(page, dialog, findings);
    return;
  }
  await walkWizard(page, dialog, findings);
}

// Une fenêtre ouverte : ses menus déroulants, ses infobulles, puis, selon sa
// nature, un assistant mené au récapitulatif ou un formulaire rempli. Elle se
// referme par « Annuler ».
export async function exploreDialog(page: Page, findings: Findings): Promise<void> {
  const dialog = page.locator("dialog[open]").last();
  await dialog.waitFor({ state: "visible", timeout: 3000 });
  findings.done.dialogs++;
  if ((await dialog.locator(".wizard").count()) > 0) {
    await walkAnyWizard(page, dialog, findings);
  } else {
    await fillEmptyFields(dialog);
    await exerciseSelects(page, dialog, findings);
    // Un choix peut ouvrir l'assistant : « Déclarer un service » commence par
    // le choix d'un conteneur, qui remplit l'assistant.
    if ((await dialog.locator(".wizard").count()) > 0) {
      await walkAnyWizard(page, dialog, findings);
    }
  }
  await exerciseTips(dialog, findings);
  if (await dialog.isVisible()) {
    await cancelDialog(dialog).catch((error: unknown) => findings.add(`la fenêtre ne se ferme pas par « Annuler » : ${String(error)}`));
  }
}

// Chaque « ? » de la zone : au survol, son texte paraît et n'est coupé par rien.
export async function exerciseTips(scope: Locator, findings: Findings): Promise<void> {
  const tips = scope.locator(".field-tip-button:visible");
  const count = Math.min(await tips.count(), 6);
  for (let index = 0; index < count; index++) {
    const tip = tips.nth(index);
    await tip.hover({ timeout: 2000 }).catch(() => undefined);
    const text = tip.locator("xpath=following-sibling::*[@role='tooltip']");
    const box = await text.boundingBox();
    if (box === null) {
      findings.add("une infobulle « ? » ne paraît pas au survol");
      continue;
    }
    const covered = await text.evaluate((element) => {
      const rect = element.getBoundingClientRect();
      const top = document.elementFromPoint(rect.left + rect.width / 2, rect.top + rect.height / 2);
      return top === null || !element.contains(top);
    });
    if (covered) {
      findings.add(`une infobulle « ? » est coupée ou recouverte : ${((await text.textContent()) ?? "").slice(0, 40)}`);
    }
  }
}
