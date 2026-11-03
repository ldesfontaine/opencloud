// Les gestes de l'explorateur de fichiers, sans écran : des chemins absolus
// séparés par « / », une sélection par noms, un curseur qui ne sort pas de la
// liste. Le composant FileExplorer les assemble ; la source des données lui est
// étrangère.

// Le chemin d'un fils d'un dossier.
export function childPath(folder: string, name: string): string {
  return folder === "/" ? `/${name}` : `${folder}/${name}`;
}

// Le dossier qui contient ce chemin ; la racine n'a pas de parent.
export function parentPath(path: string): string {
  const cut = path.lastIndexOf("/");
  return cut <= 0 ? "/" : path.slice(0, cut);
}

export interface Crumb {
  name: string;
  path: string;
}

// Le fil d'Ariane d'un chemin : chaque dossier avec le chemin qui y mène, la
// racine exclue (l'écran la nomme).
export function crumbsOf(path: string): Crumb[] {
  const crumbs: Crumb[] = [];
  let walked = "/";
  for (const name of path.split("/").filter((part) => part !== "")) {
    walked = childPath(walked, name);
    crumbs.push({ name, path: walked });
  }
  return crumbs;
}

// La sélection après un clic ou une pression sur Espace : le nom y entre ou en sort.
export function toggleName(selected: readonly string[], name: string): string[] {
  return selected.includes(name) ? selected.filter((candidate) => candidate !== name) : [...selected, name];
}

// Le curseur après une flèche : il reste dans la liste, et -1 quand elle est vide.
export function moveCursor(current: number, delta: number, length: number): number {
  if (length === 0) {
    return -1;
  }
  return Math.min(length - 1, Math.max(0, current + delta));
}
