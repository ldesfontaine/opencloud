import type { Capability, Machine, TokenResponse } from "../api/machines";
import type { Translate } from "../i18n/context";
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
export interface MachineFormValues extends RightsValues {
  name: string;
}

export const emptyMachineForm: MachineFormValues = { name: "", rights: [], backupPath: "", writePaths: "" };

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

// La racine des services qu'un agent déploie quand on ne lui en donne pas
// d'autre : brand.WorkspaceRoot côté Go (-root-dir).
export const workspaceRoot = "/srv/workspace";

// Les dossiers où l'agent écrit hors de son état, que son unité systemd ne
// laisse pas écrire sans ReadWritePaths : celui des sauvegardes reçues, et la
// racine des services dès qu'il déploie ou redéploie.
export function unitWritePaths(rights: Capability[]): string[] {
  const paths = [...(rights.find((capability) => capability.name === "backup.receive")?.paths ?? [])];
  const deploys = rights.some((capability) => capability.name === "docker.deploy" || capability.name === "docker.update");
  if (deploys && !paths.includes(workspaceRoot)) {
    paths.push(workspaceRoot);
  }
  return paths;
}

// Le fichier à poser dans le dossier drop-in de l'unité de l'agent.
export function unitDropIn(paths: string[]): string {
  return `[Service]\nReadWritePaths=${paths.join(" ")}`;
}

// La liste fermée du serveur (machine.CapabilityNames), dans son ordre.
export const capabilityNames = ["observe", "agent.update", "docker.deploy", "docker.update", "backup.receive", "files.write"] as const;

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

// « vu » d'une machine. En ligne, elle l'est à l'instant : son flux est
// ouvert. Le direct ne republie pas la liste à chaque signal, le dernier
// signal lu vieillirait à l'écran pendant que la machine répond.
export function machineSeen(t: Translate, machine: Pick<Machine, "online" | "last_seen_at">, now: number): string {
  if (machine.online) {
    return t("machine.seen_now");
  }
  return seenAgo(t, machine.last_seen_at, now);
}
