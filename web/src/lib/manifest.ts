import type { Apply, ApplyState, ManifestCarry, ManifestChange, ManifestFieldChange } from "../api/manifest";
import type { Translate } from "../i18n/context";
import type { Tone } from "./tone";

// Les états d'un passage, tels que le serveur les écrit ; un test Go vérifie
// que chacun a son libellé dans les deux langues.
export const applyStates: readonly ApplyState[] = ["confirmed", "applied", "failed"];

// La pastille d'un passage : appliqué ok, arrêté en échec, en cours à surveiller.
export function stateTone(state: ApplyState): Tone {
  switch (state) {
    case "applied":
      return "ok";
    case "failed":
      return "danger";
    case "confirmed":
      return "warn";
  }
}

// « +1 ~2 −0 » : ce qu'un passage crée, modifie, supprime.
export function summaryText(summary: Pick<Apply["summary"], "create" | "update" | "delete">): string {
  return `+${summary.create} ~${summary.update} −${summary.delete}`;
}

// Le nombre de changements d'un passage, écrits ou non.
export function totalChanges(summary: Pick<Apply["summary"], "create" | "update" | "delete">): number {
  return summary.create + summary.update + summary.delete;
}

// « Sonde wiki-http » : le genre, puis le nom tel que le fichier le donne.
export function changeLabel(t: Translate, change: Pick<ManifestChange, "kind" | "name">): string {
  return `${t(`manifest.kind_${change.kind}`)} ${change.name}`;
}

// Une valeur vide s'écrit « ∅ » : un champ qui apparaît ou disparaît se lit.
function valueText(value: string): string {
  return value === "" ? "∅" : value;
}

export function fieldText(change: ManifestFieldChange): string {
  return `${change.field} : ${valueText(change.before)} → ${valueText(change.after)}`;
}

// Un nombre avec le séparateur de milliers de la langue : « 1 204 », « 1,204 ».
export function formatCount(language: string, count: number): string {
  return new Intl.NumberFormat(language).format(count);
}

// « key » ou « key_one » selon le nombre, comme la langue le règle (en
// français zéro est un singulier, en anglais non). Le nombre, écrit avec son
// séparateur, est le premier argument du texte.
export function countedText(t: Translate, language: string, key: string, count: number, ...args: (string | number)[]): string {
  const singular = new Intl.PluralRules(language).select(count) === "one";
  return t(singular ? `${key}_one` : key, formatCount(language, count), ...args);
}

// « 1 204 essais, 80 jours » : ce qu'une suppression emporte.
export function carriesText(t: Translate, language: string, carries: ManifestCarry[]): string {
  return carries.map((carry) => countedText(t, language, `manifest.carries_${carry.what}`, carry.count)).join(", ");
}

// « 2 écrits sur 3 » : où en est un passage arrêté.
export function writtenText(t: Translate, language: string, done: number, total: number): string {
  return countedText(t, language, "manifest.written", done, formatCount(language, total));
}

// « sha256:1a2b3c4d… » : assez pour reconnaître un fichier.
export function shortHash(hash: string): string {
  const [algorithm = "", digest = ""] = hash.split(":");
  if (digest.length <= 8) {
    return hash;
  }
  return `${algorithm}:${digest.slice(0, 8)}…`;
}

// L'objet qu'un passage arrêté a refusé, « sonde/wiki-http » lu comme « Sonde
// wiki-http ».
export function failedObjectText(t: Translate, object: string): string {
  const slash = object.indexOf("/");
  if (slash < 0) {
    return object;
  }
  return `${t(`manifest.kind_${object.slice(0, slash)}`)} ${object.slice(slash + 1)}`;
}

// L'acteur d'un passage : la commande locale ou l'opérateur de l'interface,
// avec le compte ou l'identifiant quand il est connu.
export function actorText(t: Translate, apply: Pick<Apply, "actor_kind" | "actor_name">): string {
  const kind = t(`manifest.actor_${apply.actor_kind}`);
  return apply.actor_name === "" ? kind : `${kind} · ${apply.actor_name}`;
}
