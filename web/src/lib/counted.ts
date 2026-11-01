import type { Translate } from "../i18n/context";

// Écrit un compte au singulier ou au pluriel : la clé « key_one » pour 1, « key »
// sinon, le nombre en premier argument (le catalogue serveur fait de même,
// lang.Catalog.Counted). Le zéro suit le pluriel ici : l'écran n'écrit un compte
// que s'il y a quelque chose à compter.
export function counted(t: Translate, key: string, count: number): string {
  return count === 1 ? t(`${key}_one`, count) : t(key, count);
}
