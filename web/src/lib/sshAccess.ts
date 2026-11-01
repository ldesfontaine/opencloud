import type { SSHReport, SSHState } from "../api/sshkeys";
import { isWeak } from "./sshkeys";

// Qui peut se connecter à quoi, lu des relevés d'authorized_keys que les
// agents envoient. Deux lectures de la même donnée : par clé (où entre-t-elle ?)
// et par machine (qui entre chez elle ?). Fonctions pures : l'écran ne fait
// que les montrer.

// Le compte root : celui dont une clé donne tout.
const rootUID = 0;

// Un compte où une clé est acceptée.
interface GrantedAccount {
  name: string;
  privileged: boolean;
}

// Les comptes d'une machine qui acceptent une clé.
interface Grant {
  machineID: string;
  machineName: string;
  accounts: GrantedAccount[];
}

export interface KeyAccess {
  fingerprint: string;
  type: string;
  bits: number;
  // Le premier commentaire non vide rencontré : une même clé en porte souvent plusieurs.
  comment: string;
  weak: boolean;
  // Vrai quand la clé ouvre au moins un compte root.
  privileged: boolean;
  grants: Grant[];
}

// Une machine est lue en entier quand l'agent a rapporté, sans troncature et
// sans compte illisible : seulement alors l'absence d'une clé prouve quelque chose.
export function isComplete(report: SSHReport): boolean {
  return report.reported_at !== null && !report.truncated && report.accounts.every((account) => account.state !== "denied" && account.state !== "unsafe" && account.state !== "too_large" && !account.truncated);
}

// Les clés distinctes de toutes les machines, une ligne par empreinte, les
// plus exposées d'abord : celles qui ouvrent root, puis les faibles, puis le
// nombre de machines, puis le commentaire.
export function buildKeyAccess(reports: SSHReport[]): KeyAccess[] {
  const rows = new Map<string, KeyAccess>();
  for (const report of reports) {
    for (const account of report.accounts) {
      for (const key of account.keys) {
        let row = rows.get(key.fingerprint);
        if (row === undefined) {
          row = { fingerprint: key.fingerprint, type: key.type, bits: key.bits, comment: "", weak: isWeak(key), privileged: false, grants: [] };
          rows.set(key.fingerprint, row);
        }
        if (row.comment === "" && key.comment !== "") {
          row.comment = key.comment;
        }
        const privileged = account.uid === rootUID;
        row.privileged = row.privileged || privileged;
        addGrant(row, report, { name: account.name, privileged });
      }
    }
  }
  return [...rows.values()].sort(byExposure);
}

// Un compte qui porte la même clé dans deux fichiers n'est compté qu'une fois.
function addGrant(row: KeyAccess, report: SSHReport, account: GrantedAccount) {
  let grant = row.grants.find((candidate) => candidate.machineID === report.machine_id);
  if (grant === undefined) {
    grant = { machineID: report.machine_id, machineName: report.machine_name, accounts: [] };
    row.grants.push(grant);
  }
  if (!grant.accounts.some((candidate) => candidate.name === account.name)) {
    grant.accounts.push(account);
    grant.accounts.sort((left, right) => Number(right.privileged) - Number(left.privileged) || left.name.localeCompare(right.name));
  }
}

function byExposure(left: KeyAccess, right: KeyAccess): number {
  return (
    Number(right.privileged) - Number(left.privileged) ||
    Number(right.weak) - Number(left.weak) ||
    right.grants.length - left.grants.length ||
    left.comment.localeCompare(right.comment) ||
    left.fingerprint.localeCompare(right.fingerprint)
  );
}

export type KeyFilter = "all" | "weak" | "privileged" | "single";

export const keyFilters: KeyFilter[] = ["all", "weak", "privileged", "single"];

export function filterKeys(rows: KeyAccess[], filter: KeyFilter): KeyAccess[] {
  switch (filter) {
    case "weak":
      return rows.filter((row) => row.weak);
    case "privileged":
      return rows.filter((row) => row.privileged);
    case "single":
      return rows.filter((row) => row.grants.length === 1);
    default:
      return rows;
  }
}

// Une clé d'un compte, ce qu'il faut pour la nommer.
interface AccountKey {
  fingerprint: string;
  comment: string;
  weak: boolean;
}

interface AccountAccess {
  name: string;
  privileged: boolean;
  // Ce que l'agent a pu lire du compte (SSHState) : un compte illisible ne
  // montre pas de clé, et le dire vaut mieux que le taire.
  state: SSHState;
  keys: AccountKey[];
}

export interface MachineAccess {
  machineID: string;
  machineName: string;
  reportedAt: string | null;
  complete: boolean;
  accounts: AccountAccess[];
}

// Les machines par nom, et dans chacune les comptes qui ont une clé ou qu'on
// n'a pas pu lire (root en tête). Un compte lu sans clé n'ouvre rien : il est omis.
export function buildMachineAccess(reports: SSHReport[]): MachineAccess[] {
  return reports
    .map((report) => ({
      machineID: report.machine_id,
      machineName: report.machine_name,
      reportedAt: report.reported_at,
      complete: isComplete(report),
      accounts: report.accounts
        .filter((account) => account.keys.length > 0 || account.state === "denied" || account.state === "unsafe" || account.state === "too_large")
        .map((account) => ({
          name: account.name,
          privileged: account.uid === rootUID,
          state: account.state,
          keys: account.keys.map((key) => ({ fingerprint: key.fingerprint, comment: key.comment, weak: isWeak(key) })),
        }))
        .sort((left, right) => Number(right.privileged) - Number(left.privileged) || left.name.localeCompare(right.name)),
    }))
    .sort((left, right) => left.machineName.localeCompare(right.machineName));
}

// Un fichier que l'agent refuse de lire (non sûr, trop gros) : l'opérateur
// peut le corriger sur la machine. C'est ce qui mérite « À regarder » ; un
// compte simplement illisible pour l'agent, lui, est l'état normal d'un
// compte dédié (audit G7).
export function needsGesture(report: SSHReport): boolean {
  return report.accounts.some((account) => account.state === "unsafe" || account.state === "too_large");
}

export interface AccessSummary {
  keys: number;
  privileged: number;
  weak: number;
  single: number;
  // Les machines dont un fichier demande un geste de l'opérateur.
  incomplete: number;
  // Les machines lues en partie pour une raison normale (comptes que l'agent
  // ne lit pas, relevé tronqué, pas encore relevée) : en gris, sans alarme.
  partial: number;
}

export function summarizeAccess(rows: KeyAccess[], reports: SSHReport[]): AccessSummary {
  return {
    keys: rows.length,
    privileged: filterKeys(rows, "privileged").length,
    weak: filterKeys(rows, "weak").length,
    single: filterKeys(rows, "single").length,
    incomplete: reports.filter(needsGesture).length,
    partial: reports.filter((report) => !isComplete(report) && !needsGesture(report)).length,
  };
}
