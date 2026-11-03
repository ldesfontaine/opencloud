import type { BackupProtection, BackupSource } from "../api/backups";
import type { CellTone, MatrixCell, MatrixColumn, MatrixGroup, MatrixPick } from "../components/Matrix";
import type { Translate } from "../i18n/context";
import { sortSources } from "./backupOverview";

// La matrice des sauvegardes : une ligne par source, groupées par machine ; une
// colonne par endroit où des copies arrivent (une cible restic, un stockage
// Proxmox), plus « Nulle part » quand une source n'a aucune copie. Une source
// sauvegardée à deux endroits a deux cases.

// L'endroit d'une protection : sa cible, ou le stockage que Proxmox écrit. Deux
// origines différentes ne partagent pas une colonne même si le nom est le même.
export function placeOf(protection: BackupProtection): { id: string; name: string; kind: "observed" | "archive" | "restic" } {
  if (protection.origin === "observed") {
    const storage = protection.archive_storage || protection.storage;
    return { id: `storage:${storage}`, name: storage, kind: "observed" };
  }
  return { id: `machine:${protection.target_machine_id}`, name: protection.target_machine_name, kind: protection.origin };
}

export const nowhereColumn = "nowhere";

export function matrixColumns(t: Translate, sources: readonly BackupSource[]): MatrixColumn[] {
  const columns = new Map<string, MatrixColumn>();
  let hasUnprotected = false;
  for (const source of sources) {
    hasUnprotected ||= source.protections.length === 0;
    for (const protection of source.protections) {
      const place = placeOf(protection);
      if (!columns.has(place.id)) {
        columns.set(place.id, { id: place.id, label: place.name, hint: t(`backup.matrix_place_${place.kind}`) });
      }
    }
  }
  const list = [...columns.values()].sort((a, b) => a.label.localeCompare(b.label));
  // « Nulle part » vient en premier : c'est ce qui demande l'opérateur, il ne doit
  // jamais se cacher derrière le défilement d'une matrice large.
  return hasUnprotected ? [{ id: nowhereColumn, label: t("backup.matrix_nowhere"), hint: t("backup.matrix_nowhere_hint") }, ...list] : list;
}

// Le ton d'une case : l'état de la protection, et le gris creux d'une copie que
// Proxmox fait de lui-même tant qu'elle va bien.
function cellToneOf(protection: BackupProtection): CellTone {
  switch (protection.state) {
    case "failed":
    case "refused":
      return "danger";
    case "late":
    case "never":
      return "warn";
    default:
      return protection.origin === "observed" ? "observed" : "ok";
  }
}

function cellLabel(t: Translate, protection: BackupProtection): string {
  if (protection.state === "failed" || protection.state === "refused") {
    return t(`backup.status_protection_${protection.state}`);
  }
  if (protection.state === "ok" && protection.origin === "observed") {
    return t("backup.matrix_observed");
  }
  if (protection.state === "ok" && protection.path?.path === "relay") {
    return t("backup.path_relay_pill");
  }
  return t(`backup.status_${protection.state}`);
}

export function matrixCells(t: Translate, source: BackupSource): Record<string, MatrixCell> {
  if (source.protections.length === 0) {
    return { [nowhereColumn]: { tone: source.observed_unknown ? "observed" : "none", label: t(source.observed_unknown ? "backup.status_unknown" : "backup.matrix_none") } };
  }
  const cells: Record<string, MatrixCell> = {};
  for (const protection of source.protections) {
    const cell: MatrixCell = { tone: cellToneOf(protection), label: cellLabel(t, protection) };
    if (protection.copies !== null) {
      cell.detail = String(protection.copies);
    }
    cells[placeOf(protection).id] = cell;
  }
  return cells;
}

// Les lignes groupées par machine ; ce qui demande l'opérateur passe en tête.
export function matrixGroups(t: Translate, sources: readonly BackupSource[]): MatrixGroup[] {
  const groups = new Map<string, MatrixGroup>();
  for (const source of sortSources(sources)) {
    const group = groups.get(source.machine_id) ?? { id: source.machine_id, label: source.machine_name, rows: [] };
    group.rows.push({
      id: source.key,
      label: source.kind === "database" ? t("backup.source_database") : source.name,
      hint: t(`backup.source_sub_${source.kind}`, source.vmid ?? source.machine_name, source.machine_name),
      cells: matrixCells(t, source),
    });
    groups.set(source.machine_id, group);
  }
  return [...groups.values()].sort((a, b) => a.label.localeCompare(b.label));
}

// Ce que la case choisie désigne : la source et sa protection à cet endroit
// (aucune pour « Nulle part »). null si la source ou la case n'existe plus.
export function pickedOf(sources: readonly BackupSource[], pick: MatrixPick | null): { source: BackupSource; protection: BackupProtection | null } | null {
  if (pick === null) {
    return null;
  }
  const source = sources.find((candidate) => candidate.key === pick.row);
  if (source === undefined) {
    return null;
  }
  if (pick.column === nowhereColumn) {
    return source.protections.length === 0 ? { source, protection: null } : null;
  }
  const protection = source.protections.find((candidate) => placeOf(candidate).id === pick.column);
  return protection === undefined ? null : { source, protection };
}

// Les sept dernières nuits d'une protection restic, de la plus ancienne à la plus
// récente : la frise de l'inspecteur. Les passages que le serveur garde, pas
// plus ; un passage qui n'a pas réussi est rouge, une nuit sans passage connu est
// vide.
const nightsShown = 7;

export function nightsOf(protection: BackupProtection): CellTone[] {
  const nights = protection.runs.slice(0, nightsShown).map((run): CellTone => (run.state === "succeeded" ? "ok" : "danger"));
  return [...Array<CellTone>(nightsShown - nights.length).fill("none"), ...nights.reverse()];
}
