import type { Capability, Machine } from "../api/machines";
import type { Translate } from "../i18n/context";
import { alwaysGranted, capabilityLabelKey, capabilityNames, describeUnavailable, type RightsValues } from "./machines";

// Les droits d'un agent manquent ici ou là ; ce fichier dit comment un écran
// le sait, et où il mène l'opérateur : le ré-enrôlement, déjà coché du droit.

// Un droit voulu : son nom et, pour un droit borné, le chemin à ajouter.
export interface WantedRight {
  name: string;
  path: string;
}

// L'adresse de la fenêtre « Ré-enrôler » d'une machine, avec le droit à y
// cocher d'avance.
export function reenrollPath(machineID: string, wanted?: { name: string; path?: string | undefined }): string {
  const base = `/machines/${machineID}/reenroller`;
  if (wanted === undefined) {
    return base;
  }
  const query = new URLSearchParams({ droit: wanted.name });
  if (wanted.path !== undefined && wanted.path !== "") {
    query.set("chemin", wanted.path);
  }
  return `${base}?${query.toString()}`;
}

// Le droit que l'adresse demande de cocher ; rien si le nom n'est pas un droit
// de la liste fermée (observe est toujours accordé, il n'a pas de case).
export function wantedRightOf(search: URLSearchParams): WantedRight | null {
  const name = search.get("droit");
  const known = capabilityNames.find((candidate) => candidate === name && candidate !== alwaysGranted);
  return known === undefined ? null : { name: known, path: search.get("chemin") ?? "" };
}

// Les valeurs du formulaire des droits avec le droit voulu coché ; son chemin
// s'ajoute à ceux qui y sont, sans doublon.
export function withWantedRight(values: RightsValues, wanted: WantedRight | null): RightsValues {
  if (wanted === null) {
    return values;
  }
  const rights = values.rights.includes(wanted.name) ? values.rights : [...values.rights, wanted.name];
  return {
    rights,
    backupPath: wanted.name === "backup.receive" ? addPath(values.backupPath, wanted.path) : values.backupPath,
    writePaths: wanted.name === "files.write" ? addPath(values.writePaths, wanted.path) : values.writePaths,
  };
}

function addPath(paths: string, path: string): string {
  const kept = paths.split(",").map((entry) => entry.trim()).filter((entry) => entry !== "");
  if (path === "" || kept.includes(path)) {
    return kept.join(", ");
  }
  return [...kept, path].join(", ");
}

// Les droits que l'agent a : ce qu'il a rapporté, sinon ce que son jeton
// demandait (il n'a pas encore parlé).
export function heldRights(agent: NonNullable<Machine["agent"]>): Capability[] {
  return agent.reported_at === null ? agent.requested : agent.capabilities;
}

// Une machine ne convient pas à un geste : pourquoi, et où aller pour y
// remédier.
export interface Unfit {
  reason: string;
  link: { to: string; label: string };
}

// Dit si la machine peut faire ce que demande right (null : il suffit d'un
// agent actif), ou pourquoi elle ne le peut pas. Un droit coché que le compte
// de l'agent ne peut pas exercer ne convient pas non plus : la raison est celle
// que la fiche donne.
export function unfitFor(t: Translate, machine: Pick<Machine, "id" | "agent">, right: string | null): Unfit | null {
  const agent = machine.agent;
  if (agent === null || agent.revoked_at !== null) {
    return { reason: t("machine.fit_no_agent"), link: { to: reenrollPath(machine.id), label: t("machine.fit_enroll") } };
  }
  if (right === null) {
    return null;
  }
  const held = heldRights(agent).find((capability) => capability.name === right);
  if (held === undefined) {
    return { reason: t("machine.fit_right_missing", t(capabilityLabelKey(right))), link: { to: reenrollPath(machine.id, { name: right }), label: t("machine.reenroll_with_right") } };
  }
  if ("unavailable" in held && typeof held.unavailable === "string" && held.unavailable !== "") {
    return { reason: describeUnavailable(t, held.unavailable, agent.run_as), link: { to: `/machines/${machine.id}`, label: t("machine.fit_open") } };
  }
  return null;
}

// Les refus du serveur qui disent un droit manquant, avec le droit qu'ils
// nomment ; les autres (action.right_missing) sont dits par l'écran qui sait
// quel ordre il a envoyé.
const refusalRights: Readonly<Record<string, string>> = {
  "deploy.right_missing": "docker.deploy",
  "backup.right_missing": "backup.receive",
  "backup.source_right_missing": "backup.produce",
};

export function rightOfRefusal(key: string): string | null {
  return refusalRights[key] ?? null;
}
