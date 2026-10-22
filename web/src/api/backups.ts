import { useResource, type Resource } from "../hooks/useResource";
import { api, segment } from "./client";
import type { ActionRun } from "./actions";

// La sauvegarde de la base : la clé, les destinations, le plan et son dernier
// ordre. Sujets du direct : « backups » et « actions » (l'état d'une
// sauvegarde est celui de son dernier ordre). Jamais la phrase de passe, ni
// la part privée de la clé : le serveur ne les rend pas.

export interface BackupKeyring {
  // La clé publique age (age1…) : ce qui chiffre chaque nuit.
  recipient: string;
  fingerprint: string;
  created_at: string;
}

// ok : l'agent a rapporté un droit qui couvre le dossier ; missing : il en
// a rapporté et aucun ne le couvre ; unavailable : le droit couvre le dossier
// mais le compte de l'agent ne peut pas l'exercer (right_reason dit pourquoi) ;
// unknown : il n'a rien rapporté encore.
export type BackupRight = "ok" | "missing" | "unavailable" | "unknown";

export interface BackupDestination {
  id: string;
  name: string;
  machine_id: string;
  machine_name: string;
  path: string;
  right: BackupRight;
  // La raison de « unavailable » ; absente sinon.
  right_reason?: string;
  created_at: string;
}

export interface BackupPlan {
  destination_id: string;
  keep: number;
  // « daily » : le seul rythme.
  schedule: string;
  last_attempt_at: string | null;
  // La clé du refus de la dernière tentative qui n'a pas pu partir ; vide sinon.
  last_error: string;
  // Le dernier ordre backup.receive ; null s'il n'y en a pas ou plus.
  last_run: ActionRun | null;
}

export interface Backups {
  keyring: BackupKeyring | null;
  destinations: BackupDestination[];
  plan: BackupPlan | null;
}

export interface DestinationRequest {
  name: string;
  machine_id: string;
  path: string;
}

export interface PlanRequest {
  destination_id: string;
  keep: number;
}

const backupsPath = "/api/backups";

export function useBackups(): Resource<Backups> {
  return useResource<Backups>(backupsPath);
}

// La phrase part ici et nulle part ailleurs : le serveur scelle la clé
// privée avec elle et ne la garde pas.
export async function createKeyring(passphrase: string, replace: boolean): Promise<BackupKeyring> {
  return api.post<BackupKeyring>(`${backupsPath}/keyring`, { passphrase, replace });
}

export async function createBackupDestination(request: DestinationRequest): Promise<BackupDestination> {
  return api.post<BackupDestination>(`${backupsPath}/destinations`, request);
}

export async function deleteBackupDestination(id: string): Promise<void> {
  return api.delete(`${backupsPath}/destinations/${segment(id)}`);
}

export async function saveBackupPlan(request: PlanRequest): Promise<BackupPlan> {
  return api.put<BackupPlan>(`${backupsPath}/plan`, request);
}

export async function deleteBackupPlan(): Promise<void> {
  return api.delete(`${backupsPath}/plan`);
}

// Lance la sauvegarde tout de suite ; la réponse est l'ordre backup.receive.
export async function runBackupNow(): Promise<ActionRun> {
  return api.post<ActionRun>(`${backupsPath}/actions/run`, {});
}
