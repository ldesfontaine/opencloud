import { childPath } from "./fileExplorer";

// La restauration d'un chemin d'un instantané : toujours À CÔTÉ de l'original,
// dans <chemin>.restaure-<AAAAMMJJ-HHMMSS>. L'écran montre ce chemin exact
// avant de confirmer et envoie le même horodatage : ce qu'on lit est ce qui
// s'écrit (agentwire.RestoreDestination).
const restoreSuffix = ".restaure-";

// L'horodatage d'une restauration : AAAAMMJJ-HHMMSS, à l'heure du navigateur.
export function restoreStamp(at: Date): string {
  const pad = (value: number, width = 2) => String(value).padStart(width, "0");
  return `${pad(at.getFullYear(), 4)}${pad(at.getMonth() + 1)}${pad(at.getDate())}-${pad(at.getHours())}${pad(at.getMinutes())}${pad(at.getSeconds())}`;
}

export function restoreDestination(path: string, stamp: string): string {
  return `${path}${restoreSuffix}${stamp}`;
}

// Ce qu'une sélection restaure : le seul nom coché, sinon le dossier ouvert
// (jamais la racine : « tout l'instantané » ne se restaure pas d'un clic).
// null : rien à restaurer, ou plusieurs noms (un à la fois).
export function restoreSourcePath(folder: string, names: readonly string[]): string | null {
  if (names.length > 1) {
    return null;
  }
  const [name] = names;
  if (name !== undefined) {
    return childPath(folder, name);
  }
  return folder === "/" ? null : folder;
}
