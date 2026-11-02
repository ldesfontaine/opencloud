import type { BackupForget, BackupMachine, BackupOverview, BackupPath, BackupProtection, BackupRetention, BackupSource, BackupTarget, RetentionFloor } from "../api/backups";
import type { Translate } from "../i18n/context";
import { counted } from "./counted";
import type { MissingSoftware } from "./resticInstall";
import { reenrollPath, type Unfit } from "./rights";
import { folded } from "./select";
import { ago } from "./time";
import type { Tone } from "./tone";

// La page Sauvegardes lit l'aperçu du serveur et en tire ses phrases : l'état
// de chaque source, la rétention en clair, ce qu'il faut ouvrir pour passer en
// direct, ce que chaque machine peut faire dans l'assistant.

// L'état d'une source, de toutes ses protections : un échec se voit toujours ;
// sinon une copie réussie suffit ; « inconnu » n'est ni bon ni mauvais.
type SourceStatus = "failed" | "none" | "never" | "late" | "running" | "unknown" | "ok";

export function sourceStatus(source: Pick<BackupSource, "protections" | "observed_unknown">): SourceStatus {
  const states = source.protections.map((protection) => protection.state);
  if (states.length === 0) {
    return source.observed_unknown ? "unknown" : "none";
  }
  if (states.includes("failed") || states.includes("refused")) {
    return "failed";
  }
  for (const state of ["ok", "running", "late", "unknown"] as const) {
    if (states.includes(state)) {
      return state;
    }
  }
  return "never";
}

export const statusTones: Record<SourceStatus, Tone> = {
  failed: "danger",
  none: "warn",
  never: "warn",
  late: "warn",
  running: "accent",
  unknown: "neutral",
  ok: "ok",
};

// Ce qui demande l'opérateur passe en tête ; à rang égal, par machine puis par nom.
const statusRank: Record<SourceStatus, number> = { failed: 0, none: 1, never: 2, late: 3, running: 4, unknown: 5, ok: 6 };

export function sortSources(sources: readonly BackupSource[]): BackupSource[] {
  return [...sources].sort(
    (a, b) => statusRank[sourceStatus(a)] - statusRank[sourceStatus(b)] || a.machine_name.localeCompare(b.machine_name) || a.name.localeCompare(b.name),
  );
}

function needsAttention(status: SourceStatus): boolean {
  return status === "failed" || status === "none" || status === "never" || status === "late";
}

function isProtected(status: SourceStatus): boolean {
  return status === "ok" || status === "running";
}

// Le filtre de la liste : tout, ce qui demande un geste, ce qui est à jour.
export const sourceFilters = ["all", "attention", "protected"] as const;
export type SourceFilter = (typeof sourceFilters)[number];

export function filterSources(sources: readonly BackupSource[], filter: SourceFilter): BackupSource[] {
  if (filter === "all") {
    return [...sources];
  }
  const keep = filter === "attention" ? needsAttention : isProtected;
  return sources.filter((source) => keep(sourceStatus(source)));
}

// La rétention en clair : « 14 nuits · 8 semaines · 3 mois », et qui
// l'applique : la cible restic chaque nuit, l'hôte Proxmox à chaque tâche,
// le serveur à chaque archive.
interface RetentionView {
  value: string;
  note: string | null;
}

export function retentionView(t: Translate, protection: Pick<BackupProtection, "origin" | "retention" | "target_machine_name">, sourceMachine: string): RetentionView {
  const retention = protection.retention;
  if (!retention.known) {
    return { value: t("backup.retention_unknown"), note: null };
  }
  const value = declaredRetention(t, retention) ?? t("backup.retention_all");
  if (!retention.applied) {
    return { value, note: null };
  }
  if (protection.origin === "restic") {
    return { value, note: t("backup.retention_by_target", protection.target_machine_name) };
  }
  if (protection.origin === "observed") {
    return { value, note: t("backup.retention_by_proxmox", sourceMachine) };
  }
  return { value, note: t("backup.retention_by_server") };
}

// La dernière purge d'un dépôt restic : pas encore, en cours, faite, ou ce
// qui l'a empêchée (ton warn).
interface ForgetView {
  text: string;
  tone: Tone;
}

export function forgetView(t: Translate, forget: BackupForget | null, now: number, describeReason: (reason: string) => string): ForgetView {
  if (forget === null) {
    return { text: t("backup.forget_none"), tone: "neutral" };
  }
  const when = forget.at === null ? "" : ago(t, forget.at, now);
  if (forget.state === "sent") {
    return { text: t("backup.forget_sent", when), tone: "neutral" };
  }
  if (forget.state === "succeeded") {
    return { text: t("backup.forget_succeeded", when), tone: "neutral" };
  }
  const reason = protectionReasonText(t, forget.reason, describeReason) || t("backup.reason_unreported");
  return { text: t(`backup.forget_${forget.state}`, when, reason), tone: "warn" };
}

// Le plancher en clair : « 7 nuits » ; null quand il n'exige rien.
export function floorText(t: Translate, floor: RetentionFloor): string | null {
  return declaredRetention(t, { known: true, applied: true, keep_all: false, keep_last: 0, keep_yearly: 0, last_forget: null, ...floor });
}

// La phrase du plancher sur la carte d'une cible.
export function targetFloorText(t: Translate, target: Pick<BackupTarget, "name" | "retention_floor">): string {
  if (target.retention_floor === null) {
    return t("backup.target_floor_unknown", target.name);
  }
  const floor = floorText(t, target.retention_floor);
  return floor === null ? t("backup.target_floor_none", target.name) : t("backup.target_floor", target.name, floor, target.name);
}

// Le plancher qu'une nouvelle cible pose quand l'opérateur n'en dit pas
// d'autre : sept nuits, comme l'agent.
export const defaultFloorDays = 7;

// Les nombres gardés, du plus fin au plus large ; null quand rien n'est dit.
export function declaredRetention(t: Translate, retention: BackupRetention): string | null {
  if (retention.keep_all) {
    return null;
  }
  const parts: [number, string][] = [
    [retention.keep_last, "backup.retention_last"],
    [retention.keep_daily, "backup.retention_daily"],
    [retention.keep_weekly, "backup.retention_weekly"],
    [retention.keep_monthly, "backup.retention_monthly"],
    [retention.keep_yearly, "backup.retention_yearly"],
  ];
  const said = parts.filter(([count]) => count > 0).map(([count, key]) => counted(t, key, count));
  return said.length === 0 ? null : said.join(" · ");
}

// La rétention que l'assistant déclare : celle de la tâche nuit-pi de Proxmox.
export const defaultRetention = { keep_last: 0, keep_daily: 14, keep_weekly: 8, keep_monthly: 3, keep_yearly: 0 };

// rest-server manque sur la cible : elle l'a rapporté, ou son agent l'a dit
// en jugeant son droit.
export function isRestServerMissing(target: Pick<BackupTarget, "state" | "reason">): boolean {
  return target.state === "error" && target.reason === "binary_missing";
}

// restic manque sur la machine : elle tient restic.produce, son agent ne
// trouve pas le binaire.
export function isResticMissing(machine: BackupMachine): boolean {
  const produce = rightState(machine, "restic.produce");
  return !machine.restic_available && produce.state === "unavailable" && produce.reason === "restic_missing";
}

// hôte:port, l'IPv6 entre crochets.
export function hostPort(host: string, port: number): string {
  return host.includes(":") ? `[${host}]:${port}` : `${host}:${port}`;
}

// Ce qu'il faut ouvrir pour passer en direct. Le serveur rend les faits, la
// phrase se compose ici : aucune règle de pare-feu n'est exigée, on dit quoi
// ouvrir.
export function directHint(t: Translate, path: Pick<BackupPath, "source_name" | "target_host" | "target_port">): string {
  return t("backup.path_direct_hint", path.source_name, hostPort(path.target_host, path.target_port));
}

// Pourquoi ce chemin : la cible répond en direct, ou ce qui a manqué.
export function pathReasonText(t: Translate, path: BackupPath): string {
  if (path.reason === "reachable") {
    return t("backup.path_reason_reachable", path.target_name);
  }
  if (path.reason === "tcp_unreachable") {
    return t("backup.path_reason_tcp_unreachable", path.source_name, hostPort(path.target_host, path.target_port));
  }
  return t(`backup.path_reason_${path.reason}`, path.source_name, path.target_name);
}

// Un refus porte sa clé (« restic.source_right_missing ») ; un mot fermé des
// ordres passe par leur décodeur.
export function protectionReasonText(t: Translate, reason: string, describeReason: (reason: string) => string): string {
  if (reason === "") {
    return "";
  }
  return reason.includes(".") ? t(reason) : describeReason(reason);
}

// Le disque d'une cible en trois parts : ce que les sauvegardes y prennent,
// le reste de l'occupé, le libre. En pour cent du total, pour la jauge.
interface DiskShare {
  backup: number;
  other: number;
  free: number;
  backupPercent: number;
  otherPercent: number;
}

export function diskShare(target: Pick<BackupTarget, "disk_total_bytes" | "disk_free_bytes" | "disk_used_bytes" | "backup_bytes">): DiskShare | null {
  const total = target.disk_total_bytes;
  if (total <= 0) {
    return null;
  }
  const used = Math.max(target.disk_used_bytes, total - target.disk_free_bytes, 0);
  const backup = Math.min(Math.max(target.backup_bytes, 0), used);
  const other = used - backup;
  const percent = (part: number) => Math.round((part / total) * 1000) / 10;
  return { backup, other, free: Math.max(total - used, 0), backupPercent: percent(backup), otherPercent: percent(other) };
}

// La plus proche exécution à venir, et la plus récente réussie, toutes
// sources confondues ; null s'il n'y en a pas.
export function nextRunAt(sources: readonly BackupSource[], now: number): string | null {
  const upcoming = allProtections(sources)
    .map((protection) => protection.next_at)
    .filter((at): at is string => at !== null && Date.parse(at) > now)
    .sort((a, b) => Date.parse(a) - Date.parse(b));
  return upcoming[0] ?? null;
}

export function lastSuccessAt(sources: readonly BackupSource[]): string | null {
  const done = allProtections(sources)
    .filter((protection) => protection.state === "ok" || protection.state === "late")
    .map((protection) => protection.last_at)
    .filter((at): at is string => at !== null)
    .sort((a, b) => Date.parse(b) - Date.parse(a));
  return done[0] ?? null;
}

function allProtections(sources: readonly BackupSource[]): BackupProtection[] {
  return sources.flatMap((source) => source.protections);
}

// Le verdict de la page : combien sont à jour, combien attendent un geste, et
// la première qui en attend un (un échec avant une source sans copie).
interface BackupVerdict {
  tone: Tone;
  total: number;
  protectedCount: number;
  attention: number;
  failed: number;
  first: BackupSource | null;
}

export function verdictOf(sources: readonly BackupSource[]): BackupVerdict {
  const statuses = sources.map(sourceStatus);
  const failed = statuses.filter((status) => status === "failed").length;
  const attention = statuses.filter(needsAttention).length;
  const first = sortSources(sources).find((source) => needsAttention(sourceStatus(source))) ?? null;
  let tone: Tone = "ok";
  if (failed > 0) {
    tone = "danger";
  } else if (attention > 0) {
    tone = "warn";
  } else if (statuses.every((status) => status === "unknown")) {
    tone = "neutral";
  }
  return { tone, total: sources.length, protectedCount: statuses.filter(isProtected).length, attention, failed, first };
}

// Rien n'est sauvegardé, ni par openCloud, ni vu chez Proxmox.
export function isNothingSaved(overview: Pick<BackupOverview, "sources">): boolean {
  return overview.sources.every((source) => source.protections.length === 0);
}

// Un droit d'une machine tel que l'aperçu le dit ; « unknown » quand la
// machine n'y figure pas (l'agent jugera).
export function rightState(machine: BackupMachine | undefined, right: string): BackupMachine["rights"][number] {
  return machine?.rights.find((candidate) => candidate.right === right) ?? { right, state: "unknown", reason: "", path: "" };
}

// Pourquoi une machine ne peut pas faire ce que l'écran propose, et le geste
// qui y remédie : le droit à cocher au ré-enrôlement, l'écran où aller, ou le
// logiciel à installer (les commandes se lisent sous la liste).
export interface Unfitness {
  reason: string;
  // Le droit qui manque : le bouton « Ré-enrôler avec ce droit ».
  right?: { machineID: string; name: string; path?: string };
  link?: { to: string; label: string };
  install?: { software: MissingSoftware; machine: BackupMachine };
}

// La portée que l'assistant propose : une VM entière (l'hôte la lit) ou le
// dossier d'un service Compose.
export type ResticScope = "vm" | "service";

export function scopeOf(source: Pick<BackupSource, "kind">): ResticScope | null {
  if (source.kind === "vm" || source.kind === "lxc") {
    return "vm";
  }
  return source.kind === "service" ? "service" : null;
}

export function candidatesFor(sources: readonly BackupSource[], scope: ResticScope): BackupSource[] {
  return sources
    .filter((source) => scopeOf(source) === scope)
    .sort((a, b) => a.machine_name.localeCompare(b.machine_name) || a.name.localeCompare(b.name));
}

// La recherche de l'assistant, sans casse ni accent, sur le nom et la machine.
export function matchesSearch(source: Pick<BackupSource, "name" | "machine_name">, query: string): boolean {
  const wanted = folded(query.trim());
  return wanted === "" || folded(`${source.name} ${source.machine_name}`).includes(wanted);
}

// Les droits qu'une source demande à son agent : restic.produce, backup.produce
// pour lancer vzdump (une VM), restic.docker pour lire les volumes et lancer
// le dump des bases d'un service.
export function rightsWanted(source: Pick<BackupSource, "kind" | "parts">): string[] {
  if (scopeOf(source) === "vm") {
    return ["restic.produce", "backup.produce"];
  }
  return needsDocker(source) ? ["restic.produce", "restic.docker"] : ["restic.produce"];
}

function needsDocker(source: Pick<BackupSource, "parts">): boolean {
  return source.parts.some((part) => part.kind !== "folder");
}

// Une source se sauvegarde quand sa machine (l'hôte pour une VM) a le droit
// restic.produce, et backup.produce pour lancer vzdump ; l'ordre ne part que
// par son flux : hors ligne, rien ne se lie.
export function sourceUnfit(t: Translate, source: BackupSource, machines: readonly BackupMachine[]): Unfitness | null {
  const machine = machines.find((candidate) => candidate.machine_id === source.machine_id);
  const wanted = rightsWanted(source);
  if (machine !== undefined && isResticMissing(machine)) {
    return { reason: t("backup.unfit_restic_missing", source.machine_name), install: { software: "restic", machine } };
  }
  for (const right of wanted) {
    const held = rightState(machine, right);
    if (held.state === "missing") {
      return { reason: t("backup.unfit_right_missing", source.machine_name, right), right: { machineID: source.machine_id, name: right } };
    }
    if (held.state === "unavailable") {
      return { reason: t("backup.unfit_right_unavailable", source.machine_name, right), link: { to: `/machines/${source.machine_id}`, label: t("machine.fit_open") } };
    }
  }
  if (machine !== undefined && !machine.online) {
    return { reason: t("backup.unfit_offline", source.machine_name), link: { to: `/machines/${source.machine_id}`, label: t("machine.fit_open") } };
  }
  return null;
}

// Une machine que l'assistant propose comme cible : prête (rest-server en
// marche, en ligne), ou grisée avec sa raison et son geste.
interface TargetChoice {
  machine: BackupMachine;
  target: BackupTarget | null;
  unfit: Unfitness | null;
}

export function targetChoices(t: Translate, overview: BackupOverview, sourceMachineID: string, newTargetPath: (machineID: string) => string): TargetChoice[] {
  const choices = overview.machines.map((machine) => {
    const target = overview.targets.find((candidate) => candidate.kind === "restic" && candidate.machine_id === machine.machine_id) ?? null;
    return { machine, target, unfit: targetUnfit(t, machine, target, sourceMachineID, newTargetPath) };
  });
  const rank = (choice: TargetChoice) => (choice.unfit === null ? 0 : choice.target !== null ? 1 : rightState(choice.machine, "restic.target").state === "missing" ? 3 : 2);
  return choices.sort((a, b) => rank(a) - rank(b) || a.machine.machine_name.localeCompare(b.machine.machine_name));
}

function targetUnfit(t: Translate, machine: BackupMachine, target: BackupTarget | null, sourceMachineID: string, newTargetPath: (machineID: string) => string): Unfitness | null {
  if (machine.machine_id === sourceMachineID) {
    return { reason: t("backup.unfit_same_machine") };
  }
  if (target !== null) {
    if (!machine.online) {
      return { reason: t("backup.unfit_target_offline"), link: { to: `/machines/${machine.machine_id}`, label: t("machine.fit_open") } };
    }
    if (isRestServerMissing(target)) {
      return { reason: t("backup.unfit_rest_server_missing"), install: { software: "rest-server", machine } };
    }
    return target.state === "running" ? null : { reason: t(`backup.unfit_target_${target.state}`) };
  }
  const right = rightState(machine, "restic.target");
  if (right.state === "unavailable" && right.reason === "rest_server_missing") {
    return { reason: t("backup.unfit_rest_server_missing"), install: { software: "rest-server", machine } };
  }
  if (right.state === "missing") {
    return { reason: t("backup.unfit_target_right_missing"), right: { machineID: machine.machine_id, name: "restic.target" } };
  }
  if (right.state === "unavailable") {
    return { reason: t("backup.unfit_target_right_unavailable"), link: { to: `/machines/${machine.machine_id}`, label: t("machine.fit_open") } };
  }
  const reason = right.state === "unknown" ? t("backup.unfit_not_target_unknown") : t("backup.unfit_not_target");
  return { reason, link: { to: newTargetPath(machine.machine_id), label: t("backup.make_target_short") } };
}

// « Faire d'une machine une cible » propose les machines qui ont le droit
// restic.target et ne sont pas déjà cibles ; les autres restent au menu,
// grisées, avec la raison et le lien qui y remédie.
export function targetSetupUnfit(t: Translate, machine: BackupMachine | undefined, machineID: string): Unfit | null {
  const sheet = { to: `/machines/${machineID}`, label: t("machine.fit_open") };
  if (machine?.is_target === true) {
    return { reason: t("backup.unfit_already_target"), link: sheet };
  }
  const right = rightState(machine, "restic.target");
  if (right.state === "missing") {
    return { reason: t("backup.unfit_target_right_missing"), link: { to: reenrollPath(machineID, { name: "restic.target" }), label: t("machine.reenroll_with_right") } };
  }
  if (right.state === "unavailable" && right.reason === "rest_server_missing") {
    return { reason: t("backup.unfit_rest_server_missing"), link: sheet };
  }
  if (right.state === "unavailable") {
    return { reason: t("backup.unfit_target_right_unavailable"), link: sheet };
  }
  if (machine !== undefined && !machine.online) {
    return { reason: t("backup.unfit_target_offline"), link: sheet };
  }
  return null;
}

// Le chemin qu'une nouvelle sauvegarde prendra, d'après la dernière qui va
// de la même source vers la même cible ; null tant qu'aucune n'a tourné.
export function expectedPath(sources: readonly BackupSource[], sourceMachineID: string, targetMachineID: string): BackupPath | null {
  for (const source of sources) {
    if (source.machine_id !== sourceMachineID) {
      continue;
    }
    const known = source.protections.find((protection) => protection.origin === "restic" && protection.target_machine_id === targetMachineID && protection.path !== null);
    if (known?.path) {
      return known.path;
    }
  }
  return null;
}

// Les sauvegardes restic qu'« Accéder à mes sauvegardes » sait ouvrir : la
// source, la protection, la cible.
export interface ResticAccess {
  key: string;
  source: BackupSource;
  protection: BackupProtection;
}

export function resticAccesses(sources: readonly BackupSource[]): ResticAccess[] {
  return sources.flatMap((source) =>
    source.protections.filter((protection) => protection.origin === "restic").map((protection) => ({ key: `${source.key}/${protection.id}`, source, protection })),
  );
}

// Le nom du fichier sous lequel restic garde le flux de vzdump d'un invité
// (l'agent le fixe : restic déduplique d'une nuit à l'autre).
export function guestArchiveName(source: Pick<BackupSource, "kind" | "vmid">): string {
  const vmid = source.vmid ?? 0;
  return source.kind === "lxc" ? `/vzdump-lxc-${vmid}.tar` : `/vzdump-qemu-${vmid}.vma`;
}

// Le numéro proposé pour restaurer en VM neuve, à côté de l'originale.
export function restoreVMID(vmid: number | null): number {
  return (vmid ?? 0) + 1000;
}

// Un nom de dépôt sur la cible est l'identifiant de sa machine, en minuscules
// (agentwire.ResticUserFor).
export function repositoryUser(machineID: string): string {
  return machineID.toLowerCase();
}

// Le libellé d'une source : « VM 101 · sur pve », « Service · sur docker1 ».
export function sourceSubtitle(t: Translate, source: Pick<BackupSource, "kind" | "vmid" | "machine_name">): string {
  if (source.kind === "vm" || source.kind === "lxc") {
    return t(`backup.source_sub_${source.kind}`, source.vmid ?? 0, source.machine_name);
  }
  return t(`backup.source_sub_${source.kind}`, source.machine_name);
}
