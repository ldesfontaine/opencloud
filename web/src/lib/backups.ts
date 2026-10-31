import type { BackupObserved, BackupPlan, BackupRun, BackupSource, BackupSourceKind, Backups, PlanRequest } from "../api/backups";
import type { ProxmoxGuest } from "../api/proxmox";
import type { Translate } from "../i18n/context";

// La longueur minimale d'une phrase, en caractères (le serveur compte des
// runes, espaces de bord retirés).
export const minPassphrase = 16;

// Pourquoi les deux saisies de la phrase ne passent pas, en clé du
// catalogue ; null si elles passent. Le serveur ne reçoit qu'une phrase :
// la confirmation se juge ici.
export function passphraseProblem(first: string, second: string): string | null {
  if (first !== second) {
    return "backup.passphrase_mismatch";
  }
  if ([...first.trim()].length < minPassphrase) {
    return "backup.passphrase_too_short";
  }
  return null;
}

// Le sujet de la base, et ce qu'un agent sait produire (la liste du serveur,
// machine.Producers).
export const databaseSubject = "database";
export const sourceKinds: readonly BackupSourceKind[] = ["database", "agent"];
export const producers = ["proxmox-vzdump"] as const;

// Le sujet d'un plan : le mot qui préfixe ses fichiers sur la destination.
// Le serveur le juge de même ; ici, pour dire pourquoi avant d'envoyer.
const subjectPattern = /^[a-z0-9][a-z0-9-]{0,39}$/;

// La base est sauvegardée quand une clé existe et que le plan de la base a
// sa destination : sinon la page le dit, en tête. Les autres plans ne
// comptent pas : ils sauvegardent autre chose.
export function isBackedUp(backups: Backups): boolean {
  const plan = backups.plans.find((candidate) => candidate.source_kind === "database");
  return backups.keyring !== null && plan !== undefined && backups.destinations.some((destination) => destination.id === plan.destination_id);
}

// Le titre d'un plan : son libellé, à défaut son sujet ; la base, qui n'a
// pas de libellé, porte celui que le catalogue lui donne.
export function planTitle(plan: BackupPlan, databaseLabel: string): string {
  if (plan.name !== "") {
    return plan.name;
  }
  return plan.source_kind === "database" ? databaseLabel : plan.subject;
}

// D'où le plan prend : la base, ou ce qu'un agent produit sur sa machine, par
// quel producteur, et ce qu'il en prend.
export function sourceLabel(t: Translate, plan: BackupPlan): string {
  if (plan.source_kind === "database") {
    return t("backup.source_kind_database");
  }
  const selector = plan.selector === "all" ? t("backup.selector_all") : t("backup.selector_some", plan.selector);
  return t("backup.source_agent", plan.source_machine_name, t(`backup.producer_${plan.producer}`), selector);
}

// Les clés que last_error compose pour un souci de cible : la clé, le numéro,
// puis la raison (qui peut porter un « : », comme right_missing:backup.produce).
const targetProblemKeys = ["backup.target_failed", "backup.prune_failed"] as const;

// La phrase de last_error. Une clé de refus se traduit telle quelle ; un souci
// de cible dit quelle machine virtuelle, et pourquoi : la raison est une de
// celles que les ordres connaissent (describeReason), sinon « non rapportée ».
export function lastErrorText(t: Translate, lastError: string, describeReason: (reason: string) => string): string {
  for (const key of targetProblemKeys) {
    if (!lastError.startsWith(`${key}:`)) {
      continue;
    }
    const [target = "", ...reason] = lastError.slice(key.length + 1).split(":");
    const code = reason.join(":");
    return t(key, target, code === "" || code === "unknown" ? t("backup.target_reason_unknown") : describeReason(code));
  }
  return t(lastError);
}

// Le dernier passage d'un plan agent en deux compteurs ; null quand il n'y en
// a pas à dire (la base, ou un plan qui n'a pas passé depuis le démarrage).
export function passSummary(t: Translate, plan: Pick<BackupPlan, "last_targets_ok" | "last_targets_failed">): string | null {
  if (plan.last_targets_ok + plan.last_targets_failed === 0) {
    return null;
  }
  return `${t("backup.targets_ok", plan.last_targets_ok)} · ${t("backup.targets_failed", plan.last_targets_failed)}`;
}

// Les invités qu'un plan peut prendre : ni modèle (vzdump n'en sauvegarde
// pas), dans l'ordre des numéros.
export function selectableGuests(guests: readonly ProxmoxGuest[]): ProxmoxGuest[] {
  return guests.filter((guest) => !guest.template).sort((a, b) => a.vmid - b.vmid);
}

// Les numéros d'un sélecteur « 5010,5012 » ; « all » n'en nomme aucun.
export function selectorIds(selector: string): number[] {
  if (selector === "all") {
    return [];
  }
  return selector
    .split(",")
    .map(Number)
    .filter((id) => Number.isInteger(id) && id > 0);
}

// Coche ou décoche un numéro : le sélecteur reste trié, sans doublon. Sans
// aucun numéro, il est vide (la page demande d'en cocher un).
export function toggleSelectorId(selector: string, vmid: number, checked: boolean): string {
  const ids = new Set(selectorIds(selector));
  if (checked) {
    ids.add(vmid);
  } else {
    ids.delete(vmid);
  }
  return [...ids].sort((a, b) => a - b).join(",");
}

// Ce que le formulaire d'un plan tient : tout en texte, comme les champs.
export interface PlanFormValues {
  subject: string;
  name: string;
  kind: BackupSourceKind;
  machine: string;
  producer: string;
  selector: string;
  destination: string;
  keep: string;
}

// Un plan neuf : la base d'abord tant qu'elle n'a pas son plan, ensuite une
// source agent (la base n'en a qu'un).
export function emptyPlanForm(hasDatabasePlan: boolean): PlanFormValues {
  return {
    subject: "",
    name: "",
    kind: hasDatabasePlan ? "agent" : "database",
    machine: "",
    producer: producers[0],
    selector: "all",
    destination: "",
    keep: "14",
  };
}

export function planFormOf(plan: BackupPlan): PlanFormValues {
  return {
    subject: plan.subject,
    name: plan.name,
    kind: plan.source_kind,
    machine: plan.source_machine_id,
    producer: plan.producer === "" ? producers[0] : plan.producer,
    selector: plan.selector === "" ? "all" : plan.selector,
    destination: plan.destination_id,
    keep: String(plan.keep),
  };
}

// Les sources qu'un plan neuf peut prendre : la base n'a qu'un plan.
export function sourceKindsFor(hasDatabasePlan: boolean): readonly BackupSourceKind[] {
  return hasDatabasePlan ? ["agent"] : sourceKinds;
}

// Pourquoi le sujet ne passe pas, en clé du catalogue ; null s'il passe. La
// base n'en saisit pas : son sujet est fixe. taken : les sujets des plans
// existants, pour un plan neuf seulement (l'écriture remplacerait le plan qui
// porte déjà ce sujet).
export function subjectProblem(values: PlanFormValues, taken: readonly string[] = []): string | null {
  const subject = values.kind === "database" ? databaseSubject : values.subject;
  if (values.kind === "agent" && (!subjectPattern.test(subject) || subject === databaseSubject)) {
    return "backup.plan_invalid";
  }
  return taken.includes(subject) ? "backup.plan_subject_taken" : null;
}

// Le sujet et le corps de l'écriture. Une base n'a ni machine, ni
// producteur, ni sélecteur : le serveur les refuse s'ils sont là.
export function planRequestOf(values: PlanFormValues, destination: string): { subject: string; request: PlanRequest } {
  const isAgent = values.kind === "agent";
  return {
    subject: isAgent ? values.subject : databaseSubject,
    request: {
      name: values.name.trim(),
      source_kind: values.kind,
      source_machine_id: isAgent ? values.machine : "",
      producer: isAgent ? values.producer : "",
      selector: isAgent ? values.selector.trim() : "",
      destination_id: destination,
      keep: Number(values.keep),
    },
  };
}

// À quel point une source est protégée : rien ne la sauvegarde, Proxmox seul en
// garde des archives, ou un plan du serveur la couvre. L'inventaire montre
// d'abord celles dont rien ne répond.
export type Protection = "none" | "observed" | "covered";

export function protectionOf(source: BackupSource): Protection {
  if (source.covered_by.length > 0) {
    return "covered";
  }
  return source.observed === null ? "none" : "observed";
}

// La dernière sauvegarde d'une source : la plus récente entre la dernière
// exécution réussie d'un plan et la plus récente archive vue sur un stockage
// Proxmox. null quand ni l'une ni l'autre n'existe.
export interface LastBackup {
  at: string;
  bytes: number | null;
  // La cible : la destination du plan, ou le stockage Proxmox.
  target: string;
  origin: "plan" | "observed";
}

export function lastBackupOf(source: BackupSource): LastBackup | null {
  const planned = source.last_ok === null ? null : plannedBackup(source.last_ok);
  const observed = source.observed === null ? null : observedBackup(source.observed);
  if (planned === null || observed === null) {
    return planned ?? observed;
  }
  return Date.parse(observed.at) > Date.parse(planned.at) ? observed : planned;
}

function plannedBackup(run: BackupRun): LastBackup {
  return { at: run.finished_at, bytes: run.size_bytes, target: run.destination_name, origin: "plan" };
}

function observedBackup(observed: BackupObserved): LastBackup {
  return { at: observed.last_at, bytes: observed.last_bytes, target: observed.storage, origin: "observed" };
}

// Le dernier essai d'un plan quand il n'a pas réussi : plus récent que la
// dernière réussite, ou sans réussite. null quand tout va bien (ou rien tenté).
export function failedAttemptOf(source: BackupSource): BackupRun | null {
  const last = source.last_run;
  if (last === null || last.result === "ok") {
    return null;
  }
  return source.last_ok === null || Date.parse(last.finished_at) > Date.parse(source.last_ok.finished_at) ? last : null;
}

// La raison d'une ligne de l'historique : la clé d'un refus se traduit telle
// quelle, un mot fermé passe par le décodeur des ordres.
export function runReasonText(t: Translate, reason: string, describeReason: (reason: string) => string): string {
  if (reason === "") {
    return "";
  }
  return reason.startsWith("backup.") ? t(reason) : describeReason(reason);
}
