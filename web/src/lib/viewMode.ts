// La bascule cartes / tableau d'une liste, mémorisée par le navigateur. Le
// stockage peut manquer (navigation privée, données bloquées) ou lever une
// exception rien qu'en y touchant : on retombe alors sur le défaut, sans
// jamais casser la page.
export type ViewMode = "cards" | "table";

const viewModes: readonly ViewMode[] = ["cards", "table"];

// Le stockage se passe en fonction : `window.localStorage` lui-même peut lever.
type StorageGetter = () => Pick<Storage, "getItem" | "setItem">;

export function viewModeKey(list: string): string {
  return `view-mode:${list}`;
}

export function readViewMode(storage: StorageGetter, list: string, fallback: ViewMode): ViewMode {
  let stored: string | null;
  try {
    stored = storage().getItem(viewModeKey(list));
  } catch {
    return fallback;
  }
  const known = viewModes.find((mode) => mode === stored);
  return known ?? fallback;
}

// Une écriture refusée n'empêche pas la bascule : elle ne dure que la visite.
export function writeViewMode(storage: StorageGetter, list: string, mode: ViewMode): void {
  try {
    storage().setItem(viewModeKey(list), mode);
  } catch {
    // Rien à faire : le choix reste en mémoire jusqu'au rechargement.
  }
}
