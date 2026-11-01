import type { Locator, Page } from "@playwright/test";

import type { Findings } from "./findings";

const triggerSelector = "button[role=combobox]";

async function describeControl(control: Locator): Promise<string> {
  const text = (await control.innerText()).trim().replace(/\s+/g, " ");
  const name = (await control.getAttribute("aria-label")) ?? (await control.getAttribute("id")) ?? "";
  return `« ${text || name} »`;
}

// Ouvre le menu déroulant, vérifie que sa liste se voit et se clique (le clic
// réel de Playwright échoue si la liste est recouverte ou inerte), y choisit
// une autre option que la courante, et vérifie que la liste se referme.
async function exerciseSelect(page: Page, trigger: Locator, findings: Findings): Promise<void> {
  const name = await describeControl(trigger);
  try {
    await trigger.scrollIntoViewIfNeeded({ timeout: 1500 });
    await trigger.click({ timeout: 1500 });
    const list = page.locator("[role=listbox]").last();
    await list.waitFor({ state: "visible", timeout: 1500 });
    // Une liste vide (aucune machine, aucune destination) est un état, pas une panne.
    if ((await list.locator("[role=option]").count()) === 0) {
      await page.keyboard.press("Escape");
      return;
    }
    // Une option grisée ne convient pas : elle dit pourquoi et mène à ce qui y
    // remédie ; elle ne se choisit pas, le parcours choisit une autre.
    const grayed = list.locator("[role=option][aria-disabled=true]");
    for (let index = 0; index < (await grayed.count()); index++) {
      const reason = grayed.nth(index).locator("xpath=following-sibling::*[contains(@class,'select-option-reason')]");
      if ((await reason.count()) === 0 || (await reason.locator("a").count()) === 0) {
        findings.add(`menu déroulant ${name} : une option grisée ne dit pas pourquoi, ou ne mène nulle part`);
      }
    }
    const usable = list.locator("[role=option]:not([aria-disabled=true])");
    const other = usable.and(list.locator("[aria-selected=false]")).first();
    if ((await usable.count()) === 0) {
      await page.keyboard.press("Escape");
      return;
    }
    const option = (await other.count()) > 0 ? other : usable.first();
    await option.click({ timeout: 1500 });
    await list.waitFor({ state: "hidden", timeout: 1500 });
    findings.done.selects++;
  } catch (error) {
    findings.add(`menu déroulant ${name} inutilisable : ${error instanceof Error ? error.message : String(error)}`);
    await page.keyboard.press("Escape").catch(() => undefined);
  }
}

// Chaque menu déroulant visible et actif de la zone, un à un. Choisir une
// option peut faire apparaître d'autres champs : le décompte se refait à chaque tour.
export async function exerciseSelects(page: Page, scope: Locator, findings: Findings): Promise<number> {
  const done = new Set<string>();
  for (let guard = 0; guard < 40; guard++) {
    const triggers = scope.locator(`${triggerSelector}:visible:not([disabled])`);
    const count = await triggers.count();
    let next: Locator | null = null;
    for (let index = 0; index < count; index++) {
      const candidate = triggers.nth(index);
      const key = `${index}|${(await candidate.getAttribute("id")) ?? ""}|${(await candidate.getAttribute("aria-label")) ?? ""}`;
      if (!done.has(key)) {
        done.add(key);
        next = candidate;
        break;
      }
    }
    if (next === null) {
      break;
    }
    await exerciseSelect(page, next, findings);
  }
  return done.size;
}
