// Le parcours d'un nouvel utilisateur, tâche par tâche : chronomètre, clics,
// captures, menus déroulants, blocs d'aide. Usage :
//   OC_USER=… OC_PASSWORD=… node parcours.mjs T2 T3 …
// Il vise un serveur de banc (OC_URL), jamais une production.
import fs from "node:fs";
import path from "node:path";
import { openBrowser, bodyText, Task, checkDropdowns, proseStats, BASE, SHOTS } from "./lib.mjs";

const results = process.env.OC_RESULTS ?? path.join(SHOTS, "mesures.jsonl");
const wanted = process.argv.slice(2);
const { browser, page } = await openBrowser();

const settle = (ms = 700) => page.waitForTimeout(ms);
// Relève les blocs d'aide longs de l'écran affiché, pour la tâche en cours.
const look = async (task, label) => {
  const found = await proseStats(page);
  (task.prose ??= []).push({ label, long: found.long.length, longest: Math.max(0, ...found.long.map((p) => p.words)), dead: found.dead });
};
const record = async (task, extra = {}) => {
  const entry = { ...task.end(), prose: task.prose ?? [], ...extra };
  fs.appendFileSync(results, JSON.stringify(entry) + "\n");
  return entry;
};
const dropdowns = async (where) => {
  const found = await checkDropdowns(page, where);
  for (const f of found) fs.appendFileSync(results, JSON.stringify({ dropdown: f }) + "\n");
  return found;
};
const text = async () => (await bodyText(page)).split("admin\n").pop();

const tasks = {
  async T2() {
    await page.goto(BASE + "/"); await settle();
    const t = new Task(page, "T2 Ajouter une machine");
    await t.click(page.getByRole("link", { name: /^Machines/ }).first());
    await settle(400);
    await t.click(page.getByText("Ajouter une machine").first());
    await settle();
    await t.shot("02-ajouter-machine-1-nommer.png");
    await look(t, "nommer");
    await page.locator("input").first().fill(process.env.OC_MACHINE ?? "vps-lyon");
    await dropdowns("Nouvelle machine");
    await t.click(page.getByText("Avancé : droits de l'agent"));
    await t.click(page.getByText("Mettre à jour l'agent"));
    await t.click(page.getByText("Déployer un service Docker"));
    await t.click(page.getByText("Mettre à jour un service Docker"));
    await t.shot("02-ajouter-machine-2-droits.png");
    await t.click(page.getByRole("button", { name: "Créer le jeton" }));
    await settle(1200);
    await t.shot("02-ajouter-machine-3-installer.png");
    if (process.env.OC_TOKEN_FILE) fs.writeFileSync(process.env.OC_TOKEN_FILE, ((await bodyText(page)).match(/oc_[a-z0-9]{40,}/) ?? [""])[0]);
    await look(t, "installer");
    await t.click(page.getByRole("button", { name: "Suivant" }));
    await settle();
    await t.shot("02-ajouter-machine-4-attente.png");
    await record(t);
    console.log((await text()).slice(0, 1200));
  },
  async T3() {
    await page.goto(BASE + "/"); await settle();
    const t = new Task(page, "T3 Voir les conteneurs d'une machine");
    // Le réflexe : la tuile « Conteneurs » de la vue d'ensemble.
    const before = page.url();
    await t.click(page.getByText("Conteneurs", { exact: true }).first());
    await settle(500);
    if (page.url() === before) { t.guess("la tuile « Conteneurs » ne mène nulle part"); t.back(); await t.shot("03-conteneurs-0-tuile-muette.png"); }
    await t.click(page.getByRole("link", { name: /^Machines/ }).first());
    await settle(400);
    await t.click(page.getByRole("link", { name: "Ouvrir" }).first());
    await settle(800);
    await t.click(page.getByRole("tab", { name: /Conteneurs/ }).or(page.getByText("Conteneurs", { exact: true }).last()));
    await settle(900);
    await t.shot("03-conteneurs-1-liste.png");
    await look(t, "conteneurs");
    console.log((await text()).split("Agent\n")[1]?.slice(0, 1400));
    await record(t);
  },

  async T4a() {
    await page.goto(BASE + "/"); await settle();
    const t = new Task(page, "T4a Déclarer un service (proposition à accepter)");
    await t.click(page.getByRole("link", { name: "Services" }).first());
    await settle(900);
    await t.shot("04-service-0-propositions.png");
    await look(t, "propositions");
    const badge = await page.getByRole("link", { name: "Services" }).first().innerText();
    t.guess(`pastille du menu : « ${badge.replace(/\n/g, " ")} » — compte des conteneurs proposés, pas des services`);
    await t.click(page.getByRole("button", { name: "Accepter" }).first());
    await settle(1500);
    await t.shot("04-service-0b-accepte.png");
    await look(t, "après acceptation");
    console.log((await text()).slice(0, 700));
    await record(t);
  },

  async T4() {
    await page.goto(BASE + "/"); await settle();
    const t = new Task(page, "T4 Déclarer un service (formulaire)");
    await t.click(page.getByRole("link", { name: "Services" }).first());
    await settle(500);
    await t.click(page.getByText("Déclarer un service").first());
    await settle();
    await t.shot("04-service-1-identite.png");
    await look(t, "identité");
    await page.locator("input").first().fill("grafana");
    const combos = page.getByRole("combobox");
    await t.click(combos.nth(4));
    await t.click(page.getByRole("option", { name: /Conteneur seul/ }));
    await dropdowns("Déclarer un service · identité");
    await t.click(page.getByRole("button", { name: "Suivant" }));
    await settle();
    t.guess("l'image et le nom du conteneur sont à retaper : openCloud les connaît déjà (conteneur grafana-solo observé)");
    await t.shot("04-service-2-unites.png");
    await look(t, "unités");
    const inputs = page.locator("main input:visible");
    console.log("inputs unités", await inputs.count());
    await inputs.nth(await inputs.count() - 3).fill("grafana");
    await inputs.nth(await inputs.count() - 2).fill("grafana/grafana");
    await inputs.nth(await inputs.count() - 1).fill("grafana-solo");
    await t.click(page.getByRole("button", { name: "Suivant" }));
    await settle();
    await t.shot("04-service-3-projets.png");
    await look(t, "projets");
    console.log((await text()).split("Récapitulatif")[2]?.slice(0, 900) ?? (await text()).slice(-900));
    await t.click(page.getByRole("button", { name: "Suivant" }));
    await settle();
    await t.shot("04-service-4-recapitulatif.png");
    await look(t, "récapitulatif");
    console.log((await text()).slice(-900));
    await record(t);
  },
  async T5() {
    await page.goto(BASE + "/"); await settle();
    const t = new Task(page, "T5 Surveiller un site (Disponibilité)");
    // Le module n'est pas dans le menu : seule la carte « À configurer » le mène.
    const menu = await page.getByRole("link", { name: "Disponibilité" }).count();
    if (menu === 0) t.guess("« Disponibilité » n'est pas dans le menu avant d'avoir créé une sonde : seule la carte « À configurer » y mène");
    await t.click(page.locator("div,li").filter({ hasText: /^Disponibilité/ }).filter({ has: page.getByText("Configurer") }).last().getByText("Configurer"));
    await settle();
    await t.shot("05-sonde-1-cible.png");
    await look(t, "cible");
    const fields = page.locator("[role=dialog] input:visible, main input:visible");
    await dropdowns("Nouvelle sonde");
    await fields.nth(0).fill("site demo-shop");
    await fields.nth(await fields.count() - 1).fill("http://127.0.0.1:18481");
    await t.click(page.getByRole("button", { name: "Suivant" }));
    await settle();
    await t.shot("05-sonde-2-rythme.png");
    await look(t, "rythme");
    console.log((await text()).slice(-700));
    await t.click(page.getByRole("button", { name: "Suivant" }));
    await settle();
    await t.shot("05-sonde-3-recapitulatif.png");
    console.log((await text()).slice(-500));
    await t.click(page.getByRole("button", { name: /Créer|Enregistrer|Déclarer|Valider/ }).last());
    await settle(2500);
    await t.shot("05-sonde-4-resultat.png");
    await look(t, "résultat");
    console.log((await text()).slice(0, 1200));
    await record(t);
  },

  async T6() {
    await page.goto(BASE + "/"); await settle();
    const t = new Task(page, "T6 Tâche planifiée");
    await t.click(page.locator("div,li").filter({ hasText: /^Tâches planifiées/ }).filter({ has: page.getByText("Configurer") }).last().getByText("Configurer"));
    await settle();
    await t.shot("06-tache-1-tache.png");
    await look(t, "tâche");
    const fields = page.locator("[role=dialog] input:visible, main input:visible");
    await dropdowns("Nouvelle tâche");
    await fields.nth(0).fill("sauvegarde nextcloud");
    console.log("champs", await fields.count());
    await fields.nth(1).fill("60");
    await t.click(page.getByRole("button", { name: "Suivant" }));
    await settle();
    await t.shot("06-tache-2-recapitulatif.png");
    console.log((await text()).slice(-600));
    await t.click(page.getByRole("button", { name: /Créer|Enregistrer|Déclarer|Valider/ }).last());
    await settle(1500);
    await t.shot("06-tache-3-ping.png");
    await look(t, "ping");
    console.log((await text()).slice(0, 1800));
    await record(t);
  },
  async T7() {
    await page.goto(BASE + "/"); await settle();
    const t = new Task(page, "T7 Canal de notification et alerte");
    await t.click(page.getByRole("link", { name: "Alertes" }).first());
    await settle(500);
    await t.shot("07-alertes-0-page.png");
    await look(t, "alertes vide");
    // Le réflexe : chercher le canal sur la page Alertes (la doc dit « Alertes → Canaux »).
    const channelHere = await page.getByText("Canaux", { exact: true }).count();
    if (channelHere === 0) { t.guess("la doc dit « Alertes → Canaux » ; le canal se crée sous Paramètres → Notifications"); t.back(); }
    await t.click(page.getByRole("link", { name: "Paramètres" }).first());
    await settle(600);
    await t.click(page.getByText("Ajouter un canal").first());
    await settle();
    const dlg = page.getByRole("dialog");
    await t.shot("07-canal-1-destination.png");
    await look(t, "canal destination");
    await dropdowns("Nouveau canal");
    const ins = dlg.locator("input:visible");
    await ins.nth(0).fill("alertes-nuit");
    await ins.nth(1).fill("http://127.0.0.1:18483/nuit");
    await t.click(dlg.getByRole("button", { name: "Suivant" }));
    await settle(400);
    await t.shot("07-canal-2-envoi.png");
    await look(t, "canal envoi");
    await t.click(dlg.getByRole("button", { name: "Suivant" }));
    await settle(400);
    await t.click(dlg.getByRole("button", { name: /Créer le canal/ }));
    await settle(1200);
    await t.shot("07-canal-3-cree.png");
    await look(t, "canal créé");
    console.log("après création :", (await text()).split("Canaux")[1]?.slice(0, 700));
    await record(t);
  },

  async T7b() {
    // L'alerte : la sonde du site est tombée (le site est arrêté). On la cherche comme le ferait l'opérateur.
    await page.goto(BASE + "/"); await settle(800);
    const t = new Task(page, "T7b Lire l'alerte ouverte");
    await t.shot("07-alerte-0-vue-ensemble.png");
    await t.click(page.getByRole("link", { name: "Alertes" }).first());
    await settle(800);
    await t.shot("07-alerte-1-liste.png");
    await look(t, "alertes ouvertes");
    console.log((await text()).slice(0, 1300));
    const first = page.locator("main a, main [role=row], main li").filter({ hasText: /site demo-shop|Hors ligne|injoignable/i }).first();
    if (await first.count()) { await t.click(first); await settle(900); await t.shot("07-alerte-2-detail.png"); await look(t, "détail alerte"); console.log((await text()).slice(0, 1200)); }
    await record(t);
  },

  async T8() {
    await page.goto(BASE + "/"); await settle();
    const t = new Task(page, "T8 Plan de sauvegarde");
    await t.click(page.locator("div,li").filter({ hasText: /^Sauvegardes/ }).filter({ has: page.getByText("Configurer") }).last().getByText("Configurer"));
    await settle(900);
    await t.shot("08-sauvegardes-0-page.png", { full: true });
    await look(t, "page");
    // Le geste le plus visible : « Ajouter un plan ».
    await t.click(page.getByText("Ajouter un plan").first());
    await settle(600);
    const dlg = page.getByRole("dialog");
    await dropdowns("Ajouter un plan");
    await t.click(dlg.getByRole("button", { name: "Suivant" }));
    await settle(600);
    await t.shot("08-sauvegardes-1-plan-destination.png");
    await look(t, "plan destination");
    console.log("plan étape 2 :", (await dlg.innerText()).slice(0, 800));
    t.guess("le plan demande une destination et une clé qui n'existent pas encore : retour à la page");
    await page.keyboard.press("Escape"); t.back();
    await settle(400);
    await t.click(page.getByText("Créer la clé").first());
    await settle(500);
    const key = page.getByRole("dialog").locator("input:visible");
    await key.nth(0).fill("une phrase de passe de banc d'essai");
    await key.nth(1).fill("une phrase de passe de banc d'essai");
    await t.shot("08-sauvegardes-2-cle.png");
    await t.click(page.getByRole("dialog").getByRole("button", { name: "Créer la clé" }));
    await settle(1500);
    await t.shot("08-sauvegardes-3-apres-cle.png");
    console.log("après clé :", (await text()).slice(0, 700));
    const phrase = await page.getByText(/phrase|coffre|noter|copier/i).count();
    await t.click(page.getByText("Ajouter la destination").first());
    await settle(600);
    await t.click(page.getByRole("dialog").getByRole("combobox").first());
    await settle(400);
    const options = await page.getByRole("option").allInnerTexts();
    console.log("machines proposées comme destination :", options);
    await t.shot("08-sauvegardes-4-destination.png");
    await look(t, "destination");
    if (options.length === 0) t.guess("aucune machine proposée : il faut ré-enrôler une machine avec le droit « recevoir des sauvegardes » (non dit dans cette fenêtre)");
    await record(t);
  },
  async T9() {
    await page.goto(BASE + "/"); await settle();
    const t = new Task(page, "T9 Pare-feu OPNsense (jusqu'à l'étape clé)");
    const menu = await page.getByRole("link", { name: "Pare-feu" }).count();
    if (menu === 0) t.guess("« Pare-feu » n'est pas dans le menu : seule la carte « À configurer » y mène");
    await t.click(page.locator("div,li").filter({ hasText: /^Pare-feu/ }).filter({ has: page.getByText("Configurer") }).last().getByText("Configurer"));
    await settle(900);
    await t.shot("09-parefeu-1-page.png", { full: true });
    await look(t, "page");
    console.log((await text()).slice(0, 300));
    const wizard = page.getByRole("dialog");
    const ins = (await wizard.count()) ? wizard.locator("input:visible") : page.locator("main input:visible");
    await dropdowns("Déclarer un pare-feu");
    await ins.nth(0).fill("opnsense-bureau");
    await t.click(page.getByRole("button", { name: "Suivant" }).last());
    await settle(500);
    await t.shot("09-parefeu-2-acces.png");
    await look(t, "accès");
    console.log((await text()).slice(-900));
    await record(t);
  },

  async T10() {
    await page.goto(BASE + "/"); await settle();
    const t = new Task(page, "T10 Accès SSH");
    // Où sont les clés SSH ? Le réflexe : fiche d'une machine, ou menu.
    await t.click(page.getByRole("link", { name: /^Machines/ }).first());
    await settle(500);
    await t.click(page.getByText("Accès SSH", { exact: true }).first());
    await settle(1000);
    await t.shot("10-ssh-1-acces.png", { full: true });
    await look(t, "accès ssh");
    console.log((await text()).slice(0, 1800));
    await record(t);
  },

  async T11() {
    await page.goto(BASE + "/"); await settle();
    const t = new Task(page, "T11 Journal");
    await t.click(page.getByRole("link", { name: "Journal" }).first());
    await settle(900);
    await t.shot("11-journal-1-liste.png", { full: true });
    await look(t, "journal");
    await dropdowns("Journal");
    await t.click(page.getByText("Voir le détail").first());
    await settle(500);
    await t.shot("11-journal-2-detail.png");
    await look(t, "détail journal");
    console.log((await text()).slice(0, 900));
    await record(t);
  },

  async T12() {
    await page.goto(BASE + "/"); await settle();
    const t = new Task(page, "T12 Paramètres");
    await t.click(page.getByRole("link", { name: "Paramètres" }).first());
    await settle(900);
    await t.shot("12-parametres-1-page.png", { full: true });
    await look(t, "paramètres");
    await dropdowns("Paramètres");
    await record(t);
  },

  async T13() {
    await page.goto(BASE + "/"); await settle();
    const t = new Task(page, "T13 Mettre à jour un agent");
    await t.click(page.getByRole("link", { name: /^Machines/ }).first());
    await settle(500);
    await t.click(page.getByRole("link", { name: "Ouvrir" }).nth(1));
    await settle(900);
    await t.click(page.getByRole("tab", { name: "Agent" }).or(page.getByText("Agent", { exact: true }).first()));
    await settle(900);
    await t.shot("13-agent-1-fiche.png", { full: true });
    await look(t, "agent");
    console.log((await text()).split("Agent\n").slice(1).join("Agent\n").slice(0, 1800));
    await record(t);
  },
  // Un serveur neuf (OC_URL), sans compte : le premier lancement.
  async T1() {
    await page.goto(BASE + "/"); await settle(900);
    const t = new Task(page, "T1 Premier lancement");
    await t.shot("01-premier-lancement-1-compte.png");
    await look(t, "compte");
    await dropdowns("Première configuration");
    const inputs = page.locator("input:visible");
    await inputs.nth(0).fill("admin");
    await inputs.nth(1).fill("audit-demo-phrase-123");
    await inputs.nth(2).fill("audit-demo-phrase-123");
    await t.click(page.getByRole("button", { name: "Créer le compte" }));
    await settle(1500);
    await t.shot("01-premier-lancement-2-agent.png", { full: true });
    if (process.env.OC_TOKEN_FILE) fs.writeFileSync(process.env.OC_TOKEN_FILE, ((await bodyText(page)).match(/oc_[a-z0-9]{40,}/) ?? [""])[0]);
    await look(t, "agent de la machine");
    await t.click(page.getByRole("button", { name: "Ouvrir l'interface" }));
    await settle(1500);
    await t.shot("01-premier-lancement-3-vue-ensemble.png");
    await look(t, "vue d'ensemble");
    await record(t);
  },

  // Les menus déroulants et les fenêtres de pages que le parcours n'ouvre pas.
  async SWEEP() {
    const t = new Task(page, "SWEEP menus déroulants hors parcours");
    const urls = ["/machines/local", "/alertes", "/disponibilite", "/taches", "/services", "/services/projets", "/journal", "/parametres", "/sauvegardes"];
    for (const url of urls) {
      await page.goto(BASE + url); await settle(900);
      await dropdowns(url);
    }
    // Les fenêtres : fiche d'une machine, alerte, silence.
    const dialogs = [
      ["/machines/local", "Corriger le type"],
      ["/machines/local", "Ré-enrôler"],
      ["/alertes", "Faire taire"],
      ["/parametres", "Ajouter un silence"],
      ["/parametres", "Créer un jeton"],
      ["/sauvegardes", "Ajouter la destination"],
    ];
    for (const [url, button] of dialogs) {
      await page.goto(BASE + url); await settle(900);
      const target = page.getByText(button, { exact: false }).first();
      if (!(await target.count())) continue;
      await target.click().catch(() => {}); await settle(600);
      if (await page.locator('[role="dialog"]:visible').count()) {
        await dropdowns(`${url} · fenêtre « ${button} »`);
        await page.keyboard.press("Escape");
      }
    }
    await record(t);
  },
};

for (const name of wanted) {
  if (!tasks[name]) { console.error("tâche inconnue", name); continue; }
  try { await tasks[name](); } catch (error) {
    console.error("ÉCHEC", name, String(error).split("\n")[0]);
    await page.screenshot({ path: path.join(process.env.S ?? ".", `fail-${name}.png`) });
  }
}
await browser.close();
