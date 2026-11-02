import type { Account, Agent, Capability, Machine, ReportedCapability, RoleID, TokenResponse } from "../api/machines";
import type { SessionRole } from "../api/session";
import type { Translate } from "../i18n/context";
import type { SelectOption } from "./select";
import { seenAgo } from "./time";

// Le sous-titre des pages qui comptent les machines : rien, une, plusieurs.
export function machinesSubtitle(t: Translate, total: number, online: number): string {
  if (total === 0) {
    return t("overview.subtitle");
  }
  if (total === 1) {
    return t("machines.subtitle_one", online);
  }
  return t("machines.subtitle", total, online);
}

// Les droits d'un agent tels que le formulaire les tient : à l'ajout d'une
// machine, ou au ré-enrôlement qui les change.
export interface RightsValues {
  // Les droits cochés, hors observe, toujours là.
  rights: string[];
  // Le dossier où recevoir des sauvegardes, et ceux où écrire, séparés par
  // des virgules.
  backupPath: string;
  writePaths: string;
}

// Le formulaire d'une machine neuve : l'opérateur la nomme et coche les
// droits de son agent, le serveur répond par un jeton et la ligne
// d'installation qui les porte.
interface MachineFormValues extends RightsValues {
  name: string;
  role: RoleID;
}

// Le rôle générique d'abord, et probe.lan coché d'emblée : les sondes d'une
// machine visent d'abord son réseau local ; l'opérateur le décoche s'il veut
// les en empêcher. C'est le préréglage de ce rôle (machine.RolePreset).
export const emptyMachineForm: MachineFormValues = { name: "", role: "generic", rights: ["probe.lan"], backupPath: "", writePaths: "" };

// Choisir un rôle coche d'avance ses droits, tels que le serveur les dit à la
// session ; l'opérateur les ajuste ensuite sous « Avancé ».
export function rolePatch(role: RoleID, roles: readonly SessionRole[]): Pick<MachineFormValues, "role" | "rights"> {
  const preset = roles.find((offered) => offered.id === role)?.capabilities ?? [];
  return { role, rights: preset.filter((name) => name !== alwaysGranted) };
}

// La saisie a changé depuis le formulaire neuf : fermer la fenêtre de l'ajout
// demande alors s'il faut l'abandonner.
export function isMachineFormModified(values: MachineFormValues): boolean {
  const sameRights = values.rights.length === emptyMachineForm.rights.length && values.rights.every((right) => emptyMachineForm.rights.includes(right));
  return values.name.trim() !== "" || values.role !== emptyMachineForm.role || values.backupPath.trim() !== "" || values.writePaths.trim() !== "" || !sameRights;
}

// Les droits ont changé depuis ceux d'origine (l'ordre des cases n'y compte pas) :
// fermer la fenêtre du ré-enrôlement demande alors s'il faut les abandonner.
export function isRightsModified(values: RightsValues, initial: RightsValues): boolean {
  const sameRights = values.rights.length === initial.rights.length && values.rights.every((right) => initial.rights.includes(right));
  return !sameRights || values.backupPath.trim() !== initial.backupPath.trim() || values.writePaths.trim() !== initial.writePaths.trim();
}

// Les droits d'un agent en valeurs du formulaire : ce que son jeton
// demandait, pour les changer.
export function rightsFormOf(capabilities: Capability[]): RightsValues {
  const pathsOf = (name: string) => capabilities.find((capability) => capability.name === name)?.paths ?? [];
  return {
    rights: capabilities.map((capability) => capability.name).filter((name) => name !== alwaysGranted),
    backupPath: pathsOf("backup.receive").join(", "),
    writePaths: pathsOf("files.write").join(", "),
  };
}

// Ce que la page du jeton reçoit : la réponse du serveur, et les droits que
// le jeton porte, pour dire ce que l'unité de l'agent doit autoriser.
export interface TokenPageState extends TokenResponse {
  rights: Capability[];
}

export function tokenPageState(token: TokenResponse, rights: Capability[]): TokenPageState {
  return { ...token, rights };
}

// La liste fermée du serveur (machine.CapabilityNames), dans son ordre.
export const capabilityNames = [
  "observe",
  "probe.lan",
  "observe.env",
  "agent.update",
  "docker.deploy",
  "docker.update",
  "backup.receive",
  "backup.produce",
  "files.write",
  "proxmox.power",
  "proxmox.snapshot",
] as const;

// Les droits qui agissent sur les invités d'un hôte Proxmox, sans chemin.
const proxmoxRights: readonly string[] = ["proxmox.power", "proxmox.snapshot"];

// Les droits que le formulaire propose : ceux de Proxmox seulement quand
// l'agent a reconnu un hôte Proxmox VE sur la machine. Un droit déjà coché
// reste, pour pouvoir le décocher si le rôle a disparu.
export function offeredCapabilities(proxmoxDetected: boolean, checked: string[]): string[] {
  return capabilityNames.filter((name) => proxmoxDetected || !proxmoxRights.includes(name) || checked.includes(name));
}

// Le seul producteur de sauvegardes que le serveur connaisse (machine.Producers) :
// la portée de backup.produce est fixée, la case suffit.
export const backupProducer = "proxmox-vzdump";

// observe est toujours accordé : la case se montre cochée et grisée.
export const alwaysGranted = "observe";

// La clé du libellé d'un droit : le point du nom devient « _ ».
export function capabilityLabelKey(name: string): string {
  return `machine.capability_${name.split(".").join("_")}`;
}

// Coche ou décoche un droit.
export function toggleRight(rights: string[], name: string): string[] {
  return rights.includes(name) ? rights.filter((right) => right !== name) : [...rights, name];
}

// Le corps de la requête : chaque droit coché, avec ses chemins pour un droit
// borné. Le serveur ajoute observe et juge les chemins.
export function capabilitiesOf(values: RightsValues): Capability[] {
  return capabilityNames
    .filter((name) => name !== alwaysGranted && values.rights.includes(name))
    .map((name) => ({ name, paths: pathsOf(name, values) }));
}

function pathsOf(name: string, values: RightsValues): string[] {
  if (name === "backup.receive") {
    return splitPaths(values.backupPath);
  }
  if (name === "files.write") {
    return splitPaths(values.writePaths);
  }
  if (name === "backup.produce") {
    return [backupProducer];
  }
  return [];
}

function splitPaths(text: string): string[] {
  return text
    .split(",")
    .map((path) => path.trim())
    .filter((path) => path !== "");
}

// Un droit en une ligne : son libellé, puis ses chemins.
export function describeCapability(t: Translate, capability: Capability): string {
  const label = t(capabilityLabelKey(capability.name));
  return capability.paths.length === 0 ? label : t("machine.right_with_paths", label, capability.paths.join(", "));
}

// Le compte de l'agent en une ligne : son nom et son uid, ou l'uid seul quand
// le compte n'a pas de nom lisible. null : l'agent n'a rien dit.
export function describeAccount(account: Account | null): string | null {
  if (account === null) {
    return null;
  }
  return account.name === "" ? String(account.uid) : `${account.name} (${account.uid})`;
}

// Le compte pour une phrase : son nom, ou son uid sans nom.
function accountLabel(account: Account | null): string {
  return account === null ? "" : account.name === "" ? String(account.uid) : account.name;
}

// Pourquoi un droit coché ne s'exerce pas : une phrase par raison. Sous root,
// un dossier refusé ne se règle pas par un chown : la phrase est autre.
export function describeUnavailable(t: Translate, reason: string, account: Account | null): string {
  if (reason === "path_denied") {
    return account?.uid === 0 ? t("machine.capability_unavailable_path_denied_root") : t("machine.capability_unavailable_path_denied", accountLabel(account));
  }
  return t(`machine.capability_unavailable_${reason}`);
}

// Les droits cochés que le compte de l'agent ne peut pas exercer, un par ligne :
// « droit : raison ». Vide quand il peut tout, ou n'a rien dit.
export function unavailableRights(t: Translate, agent: Pick<Agent, "capabilities" | "run_as">): string[] {
  return agent.capabilities
    .filter((capability: ReportedCapability) => capability.unavailable !== "")
    .map((capability) => t("machine.capability_unavailable_item", describeCapability(t, capability), describeUnavailable(t, capability.unavailable, agent.run_as)));
}

// « vu » d'une machine. En ligne, elle l'est à l'instant : son flux est
// ouvert. Le direct ne republie pas la liste à chaque signal, le dernier
// signal lu vieillirait à l'écran pendant que la machine répond.
export function machineSeen(t: Translate, machine: Pick<Machine, "online" | "last_seen_at">, now: number): string {
  if (machine.online) {
    return t("machine.seen_now");
  }
  return seenAgo(t, machine.last_seen_at, now);
}

// Une machine archivée : son agent est révoqué (supprimée en gardant
// l'historique), tout le reste demeure. Ré-enrôler la remet en service.
export function isArchived(machine: Pick<Machine, "agent">): boolean {
  return machine.agent !== null && machine.agent.revoked_at !== null;
}

// Les machines que l'on attend : les archivées restent dans la liste, mais ne
// comptent ni parmi les machines en ligne ni parmi celles en défaut.
export function liveMachines<M extends Pick<Machine, "agent">>(machines: M[]): M[] {
  return machines.filter((machine) => !isArchived(machine));
}

// Le bouton qui supprime dans la fenêtre « Supprimer la machine » : garder
// l'historique se défait (ré-enrôler), il part tout de suite ; effacer ne se
// défait pas, il attend le nom de la machine tapé tel quel.
export function canConfirmDeletion(machineName: string, typedName: string, keepHistory: boolean): boolean {
  return keepHistory || typedName === machineName;
}

// L'option d'un menu pour une machine : son nom, puis son type en discret, pour
// qu'on voie où elle tourne avant de la choisir.
// Sans type (une machine que la liste ne connaît plus), pas de mot : ne rien dire
// vaut mieux que dire « inconnu » d'une machine qu'on ne lit pas.
export function machineOption(t: Translate, machine: { id: string; name: string; kind?: Machine["kind"] | undefined }): SelectOption {
  return { value: machine.id, label: machine.name, hint: machine.kind === undefined ? undefined : t(`machine.kind_${machine.kind}`) };
}

// Le type de chaque machine de la liste, par identifiant : pour les écrans dont
// les machines viennent d'ailleurs (alertes, jetons) et ne portent pas le leur.
export function kindsByID(machines: readonly Pick<Machine, "id" | "kind">[]): ReadonlyMap<string, Machine["kind"]> {
  return new Map(machines.map((machine) => [machine.id, machine.kind]));
}
