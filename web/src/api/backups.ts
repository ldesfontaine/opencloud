import { useResource, type Resource } from "../hooks/useResource";
import { api } from "./client";
import { route } from "./routes";
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

interface DestinationRequest {
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

// L'aperçu de la page : ce qui est sauvegardé (et comment), où ça va, et ce
// que chaque machine peut faire. Des faits : instants ISO, octets, mots
// fermés ; l'écran compose ses phrases. Sujets du direct : voir topicsOf.

// database : la base de la machine openCloud ; vm, lxc : un invité Proxmox ;
// service : un service Compose déclaré ; docker_volume : un volume nommé ;
// firewall : la configuration d'un pare-feu déclaré, lue par le serveur.
type OverviewSourceKind = "database" | "vm" | "lxc" | "service" | "docker_volume" | "firewall";

// observed : une tâche vzdump que Proxmox mène de lui-même ; archive : un plan
// d'archives chiffrées d'openCloud ; restic : une sauvegarde restic d'openCloud.
type ProtectionOrigin = "observed" | "archive" | "restic";

type ProtectionState = "ok" | "late" | "failed" | "refused" | "running" | "never" | "unknown";

// La rétention en clair : known faux, inconnue ; applied faux, rien ne la
// fait respecter. Restic : last_forget, la dernière purge du dépôt par la
// cible, null avant la première.
export interface BackupRetention {
  known: boolean;
  applied: boolean;
  keep_all: boolean;
  keep_last: number;
  keep_daily: number;
  keep_weekly: number;
  keep_monthly: number;
  keep_yearly: number;
  last_forget: BackupForget | null;
}

// La dernière purge d'un dépôt restic par sa cible. reason : un mot fermé
// (retention_below_floor…) ou la clé d'un refus.
export interface BackupForget {
  at: string | null;
  state: "sent" | "succeeded" | "failed" | "refused";
  reason: string;
}

// Le plancher qu'une cible tient : elle refuse toute rétention qui garderait
// moins.
export interface RetentionFloor {
  keep_daily: number;
  keep_weekly: number;
  keep_monthly: number;
}

// Le chemin de la dernière exécution restic, et de quoi dire ce qu'il faut
// ouvrir : source_name doit joindre target_host:target_port.
export interface BackupPath {
  path: "direct" | "relay";
  reason: string;
  source_name: string;
  target_name: string;
  target_host: string;
  target_port: number;
}

export interface ResticRun {
  run_id: string;
  finished_at: string;
  state: string;
  reason: string;
  path: string;
  path_reason: string;
  bytes_added: number;
  duration_seconds: number;
  // Portée service : ce que chaque partie a donné à ce passage.
  parts: BackupPartResult[];
}

// Ce qu'une sauvegarde de service contient : son dossier, un volume nommé de
// Docker, ou la base d'un conteneur (engine : postgres, mariadb, mysql, mongo).
export type BackupPartKind = "folder" | "volume" | "database";

export interface BackupSourcePart {
  kind: BackupPartKind;
  name: string;
  engine: string;
}

// Ce qu'une partie a donné au dernier passage : ok, failed, ou skipped (le
// conteneur ou le volume n'est plus sur la machine). reason : un mot fermé.
export interface BackupPartResult {
  kind: BackupPartKind;
  name: string;
  state: "ok" | "failed" | "skipped";
  reason: string;
  bytes_added: number;
}

// La vérification du dépôt d'une sauvegarde restic par sa cible : state est
// "" (jamais tentée), sent, succeeded, failed ou refused ; reason un mot fermé
// ou la clé d'un refus. Des comptes, jamais un nom ni un contenu. right : le
// droit restic.verify de la cible (ok, missing, unavailable, unknown) ; stale :
// le dernier test réussi est trop ancien.
export interface BackupVerify {
  state: "" | "sent" | "succeeded" | "failed" | "refused";
  reason: string;
  at: string | null;
  success_at: string | null;
  snapshots: number;
  files: number;
  bytes: number;
  right: "ok" | "missing" | "unavailable" | "unknown";
  right_reason: string;
  stale: boolean;
}

export interface BackupProtection {
  origin: ProtectionOrigin;
  // La tâche vzdump, le sujet du plan, ou la sauvegarde restic.
  id: string;
  name: string;
  // restic : vm ou service ; vide sinon.
  scope: string;
  target_machine_id: string;
  target_machine_name: string;
  storage: string;
  archive_storage: string;
  state: ProtectionState;
  // Un mot fermé, ou la clé d'un refus.
  reason: string;
  last_at: string | null;
  // La dernière réussite, même quand le dernier essai a échoué.
  last_success_at: string | null;
  next_at: string | null;
  schedule: string;
  size_bytes: number | null;
  added_bytes: number | null;
  copies: number | null;
  retention: BackupRetention;
  path: BackupPath | null;
  // restic, portée service : le résultat de chaque partie au dernier passage.
  parts: BackupPartResult[];
  runs: ResticRun[];
  // restic : la vérification du dépôt ; null pour les autres origines.
  verify: BackupVerify | null;
}

export interface BackupSource {
  key: string;
  kind: OverviewSourceKind;
  name: string;
  machine_id: string;
  machine_name: string;
  vmid: number | null;
  service_id: string | null;
  driver: string;
  used_by: string[];
  // L'hôte n'a pas pu lire ses archives ou ses tâches : « inconnu », jamais « aucune ».
  observed_unknown: boolean;
  // Un service : ce que « le service entier » sauvegardera ; vide sinon.
  parts: BackupSourcePart[];
  protections: BackupProtection[];
}

// restic : un rest-server d'openCloud ; storage : un stockage Proxmox ;
// destination : la destination d'un plan d'archives.
type BackupTargetKind = "restic" | "storage" | "destination";

export interface BackupTarget {
  key: string;
  kind: BackupTargetKind;
  name: string;
  machine_id: string;
  machine_name: string;
  online: boolean;
  // restic : running, stopped, error (reason binary_missing : rest-server
  // manque), pending ; storage : active, inactive ; destination : l'état du
  // droit de l'agent.
  state: string;
  reason: string;
  host: string;
  port: number;
  dir: string;
  version: string;
  fingerprint: string;
  disk_total_bytes: number;
  disk_free_bytes: number;
  disk_used_bytes: number;
  backup_bytes: number;
  reported_at: string | null;
  // Restic : null tant que la cible ne l'a pas rapporté.
  retention_floor: RetentionFloor | null;
  node: string;
  type: string;
  // Les clés des sources qu'elle garde.
  holds: string[];
}

export interface BackupMachineRight {
  right: string;
  state: BackupRight;
  reason: string;
  // Le dossier du droit restic.target.
  path: string;
}

export interface BackupMachine {
  machine_id: string;
  machine_name: string;
  // Le système et l'architecture enrôlés : de quoi composer la commande
  // d'installation de restic ou de rest-server.
  os: string;
  arch: string;
  online: boolean;
  // restic.produce tenu et restic trouvé.
  restic_available: boolean;
  is_target: boolean;
  proxmox_host: boolean;
  proxmox_jobs_unknown: boolean;
  rights: BackupMachineRight[];
}

export interface BackupOverview {
  sources: BackupSource[];
  targets: BackupTarget[];
  machines: BackupMachine[];
}

// Ce que l'opérateur saisit pour faire d'une machine une cible ; un dossier
// vide prend celui du droit restic.target. Le plancher ne fait que monter.
interface ResticTargetRequest {
  host: string;
  port: number;
  dir: string;
  retention_floor: RetentionFloor;
}

// La rétention d'une sauvegarde, que la cible applique après chaque nuit.
export interface ResticRetentionRequest {
  keep_last: number;
  keep_daily: number;
  keep_weekly: number;
  keep_monthly: number;
  keep_yearly: number;
}

// Une sauvegarde restic, de la machine source vers une cible. Portée vm :
// vmid ; portée service : service_id.
export interface ResticBackupRequest {
  name: string;
  scope: "vm" | "service";
  vmid: number;
  service_id: string;
  target_machine_id: string;
  retention: ResticRetentionRequest;
}

const backupsPath = "/api/backups";

export function useBackups(): Resource<Backups> {
  return useResource<Backups>(backupsPath);
}

export function useBackupOverview(): Resource<BackupOverview> {
  return useResource<BackupOverview>(`${backupsPath}/overview`);
}

// La phrase part ici et nulle part ailleurs : le serveur scelle la clé
// privée avec elle et ne la garde pas.
export async function createKeyring(passphrase: string, replace: boolean): Promise<BackupKeyring> {
  return api.send<BackupKeyring>(route("POST /api/backups/keyring"), { passphrase, replace });
}

export async function createBackupDestination(request: DestinationRequest): Promise<BackupDestination> {
  return api.send<BackupDestination>(route("POST /api/backups/destinations"), request);
}

export async function deleteBackupDestination(id: string): Promise<void> {
  return api.send(route("DELETE /api/backups/destinations/{id}", { id }));
}

export async function saveBackupPlan(subject: string, request: PlanRequest): Promise<BackupPlan> {
  return api.send<BackupPlan>(route("PUT /api/backups/plans/{subject}", { subject }), request);
}

export async function deleteBackupPlan(subject: string): Promise<void> {
  return api.send(route("DELETE /api/backups/plans/{subject}", { subject }));
}

// Lance la sauvegarde du plan tout de suite. Pour la base, la réponse est
// l'ordre backup.receive ; pour un plan agent, le premier ordre
// backup.produce, le reste du passage (une cible après l'autre) se mène
// derrière la requête.
export async function runBackupNow(subject: string): Promise<ActionRun> {
  return api.send<ActionRun>(route("POST /api/backups/plans/{subject}/actions/run", { subject }), {});
}

// Les gestes restic. Aucun mot de passe ne passe ici : le serveur les tire,
// les garde scellés et les remet aux deux machines par ordre signé.
export async function setupResticTarget(machineID: string, request: ResticTargetRequest): Promise<void> {
  return api.send(route("POST /api/machines/{id}/actions/restic-target-setup", { id: machineID }), request);
}

export async function removeResticTarget(machineID: string): Promise<void> {
  return api.send(route("POST /api/machines/{id}/actions/restic-target-remove", { id: machineID }), {});
}

export async function createResticBackup(machineID: string, request: ResticBackupRequest): Promise<{ id: string }> {
  return api.send<{ id: string }>(route("POST /api/machines/{id}/restic-backups", { id: machineID }), request);
}

export async function updateResticBackup(machineID: string, backupID: string, request: { name: string; retention: ResticRetentionRequest }): Promise<void> {
  return api.send(route("PUT /api/machines/{id}/restic-backups/{backup}", { id: machineID, backup: backupID }), request);
}

export async function deleteResticBackup(machineID: string, backupID: string): Promise<void> {
  return api.send(route("DELETE /api/machines/{id}/restic-backups/{backup}", { id: machineID, backup: backupID }));
}

export async function verifyResticBackup(machineID: string, backupID: string): Promise<ActionRun> {
  return api.send<ActionRun>(route("POST /api/machines/{id}/restic-backups/{backup}/actions/verify", { id: machineID, backup: backupID }), {});
}

export async function runResticBackup(machineID: string, backupID: string): Promise<ActionRun> {
  return api.send<ActionRun>(route("POST /api/machines/{id}/restic-backups/{backup}/actions/run", { id: machineID, backup: backupID }), {});
}

// Un instantané du dépôt d'une sauvegarde : l'identifiant complet, l'instant
// (ISO) et les dossiers qu'il a pris.
interface BrowseSnapshot {
  id: string;
  time: string;
  paths: string[];
}

// Un fils d'un dossier d'un instantané : son nom (jamais un chemin), ce qu'il
// est, sa taille en octets et sa date (ISO, vide si inconnue).
interface BrowseEntry {
  name: string;
  kind: "dir" | "file" | "link" | "other";
  size: number;
  mtime: string;
}

// Ce que la cible a lu : les instantanés (sans « snapshot »), ou le contenu
// d'un dossier ; truncated : le dossier avait plus de fils que la borne. Rien
// n'est gardé par le serveur.
export interface BrowseReport {
  snapshots: BrowseSnapshot[];
  entries: BrowseEntry[];
  truncated: boolean;
}

// La cible lit son dépôt : les instantanés (snapshot vide), ou le dossier path
// de l'instantané snapshot. La requête attend la cible.
export async function browseResticBackup(machineID: string, backupID: string, snapshot: string, path: string): Promise<BrowseReport> {
  return api.send<BrowseReport>(route("POST /api/machines/{id}/restic-backups/{backup}/actions/browse", { id: machineID, backup: backupID }), { snapshot, path });
}
