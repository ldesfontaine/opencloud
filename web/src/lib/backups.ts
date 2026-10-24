import type { BackupPlan, BackupSourceKind, Backups, PlanRequest } from "../api/backups";
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
// backup.Producers).
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

// Pourquoi le sujet ne passe pas, en clé du catalogue ; null s'il passe. La
// base n'en saisit pas : son sujet est fixe.
export function subjectProblem(values: PlanFormValues): string | null {
  if (values.kind === "database") {
    return null;
  }
  return subjectPattern.test(values.subject) && values.subject !== databaseSubject ? null : "backup.plan_invalid";
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
