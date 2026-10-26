import { useResource, type Resource } from "../hooks/useResource";
import { api, segment } from "./client";
import type { ActionRun } from "./actions";

// Les sauvegardes : la clé, les destinations, les plans et le dernier ordre
// de chacun. Sujets du direct : « backups » et « actions » (l'état d'une
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

// database : la base de la machine openCloud ; agent : ce qu'un agent produit
// sur sa machine.
export type BackupSourceKind = "database" | "agent";

export interface BackupPlan {
  // Le nom unique du plan, qui préfixe ses fichiers sur la destination.
  subject: string;
  // Le libellé affiché ; vide, la page montre le sujet.
  name: string;
  source_kind: BackupSourceKind;
  // Vides pour la base ; pour une source agent, la machine qui produit, le
  // producteur et ce qu'il en prend (« all » ou des identifiants séparés par
  // des virgules).
  source_machine_id: string;
  source_machine_name: string;
  producer: string;
  selector: string;
  destination_id: string;
  keep: number;
  // « daily » : le seul rythme.
  schedule: string;
  last_attempt_at: string | null;
  // Ce qui a gêné la dernière tentative, vide sinon : la clé d'un refus
  // (« backup.source_offline »), ou, pour un plan agent, le premier souci du
  // passage avec sa cible et sa raison (« backup.target_failed:5010:timeout »,
  // « backup.prune_failed:5010:… ») ; lastErrorText la lit.
  last_error: string;
  // Le dernier ordre backup.receive du plan ; null s'il n'y en a pas ou plus.
  last_run: ActionRun | null;
  // Les cibles du dernier passage d'un plan agent : reçues, et en échec.
  // Gardés en mémoire par le serveur : 0 et 0 pour la base, et après un
  // redémarrage tant que le plan n'a pas repassé.
  last_targets_ok: number;
  last_targets_failed: number;
}

export interface Backups {
  keyring: BackupKeyring | null;
  destinations: BackupDestination[];
  plans: BackupPlan[];
}

export interface DestinationRequest {
  name: string;
  machine_id: string;
  path: string;
}

// Le sujet part dans l'adresse, le reste dans le corps.
export interface PlanRequest {
  name: string;
  source_kind: BackupSourceKind;
  source_machine_id: string;
  producer: string;
  selector: string;
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

export async function saveBackupPlan(subject: string, request: PlanRequest): Promise<BackupPlan> {
  return api.put<BackupPlan>(`${backupsPath}/plans/${segment(subject)}`, request);
}

export async function deleteBackupPlan(subject: string): Promise<void> {
  return api.delete(`${backupsPath}/plans/${segment(subject)}`);
}

// Lance la sauvegarde du plan tout de suite. Pour la base, la réponse est
// l'ordre backup.receive ; pour un plan agent, le premier ordre
// backup.produce, le reste du passage (une cible après l'autre) se mène
// derrière la requête.
export async function runBackupNow(subject: string): Promise<ActionRun> {
  return api.post<ActionRun>(`${backupsPath}/plans/${segment(subject)}/actions/run`, {});
}
