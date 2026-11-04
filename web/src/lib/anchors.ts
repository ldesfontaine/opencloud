import type { ApiError } from "../api/client";
import type { AgentAnchors, AnchorKind, Anchors } from "../api/machines";

// Les ancres de confiance locales : des valeurs que seul l'administrateur de la
// machine pose (à l'installation, ou en root avec « agent trust »). Aucun ordre
// de openCloud ne peut les changer ; sans ancre, l'agent refuse l'ordre qu'elle
// borne. Ce fichier dit comment un écran les montre et les demande.

// Le registre du serveur (internal/anchor), dans son ordre : le droit dont
// l'ordre est borné, et ce que l'ancre accepte. Un test relit les noms et les
// droits dans la réponse figée de la fiche d'une machine.
export const anchorKinds = [
  { name: "backup.recipient", right: "backup.produce", max: 2 },
  { name: "backup.guest", right: "backup.produce", max: 32 },
  { name: "restic.target-cert", right: "restic.produce", max: 4 },
] as const;

// Le texte de chaque champ du formulaire, par type d'ancre.
export type AnchorFields = Record<string, string>;

export const emptyAnchorFields: AnchorFields = {};

// Le suffixe des clés du catalogue d'un type : « restic.target-cert » devient
// « restic_target_cert ».
function anchorKey(name: string): string {
  return name.split(/[.-]/).join("_");
}

export function anchorLabelKey(name: string): string {
  return `anchor.label_${anchorKey(name)}`;
}

export function anchorHelpKey(name: string): string {
  return `anchor.help_${anchorKey(name)}`;
}

// Les types d'ancre que les droits cochés rendent utiles.
export function anchorKindsFor(rights: readonly string[]): typeof anchorKinds[number][] {
  return anchorKinds.filter((kind) => rights.includes(kind.right));
}

// Les valeurs d'un champ : séparées par des virgules, des espaces ou des fins de ligne.
export function splitAnchorValues(text: string): string[] {
  return text
    .split(/[\s,]+/)
    .map((value) => value.trim())
    .filter((value) => value !== "");
}

// Le corps de la requête : les valeurs des seuls types dont le droit est coché.
// Le serveur juge leur forme.
export function anchorsOf(fields: AnchorFields, rights: readonly string[]): Anchors {
  const anchors: Anchors = {};
  for (const kind of anchorKindsFor(rights)) {
    const values = splitAnchorValues(fields[kind.name] ?? "");
    if (values.length > 0) {
      anchors[kind.name] = values;
    }
  }
  return anchors;
}

// Les ancres que l'agent tient, en champs du formulaire : un ré-enrôlement
// efface son fichier, ses ancres se redonnent. Un type illisible n'a rien à
// recopier.
export function anchorFieldsOf(anchors: AgentAnchors | undefined): AnchorFields {
  const fields: AnchorFields = {};
  for (const kind of anchors?.kinds ?? []) {
    if (kind.state === "set") {
      fields[kind.name] = kind.values.join(", ");
    }
  }
  return fields;
}

// Cocher backup.produce propose la clé du trousseau de sauvegarde : c'est elle
// que cette machine chiffrera. Une saisie de l'opérateur reste.
export function withKeyringRecipient(fields: AnchorFields, recipient: string | undefined): AnchorFields {
  if (recipient === undefined || recipient === "" || (fields["backup.recipient"] ?? "").trim() !== "") {
    return fields;
  }
  return { ...fields, "backup.recipient": recipient };
}

// Les ancres ont changé depuis celles d'origine.
export function areAnchorsModified(fields: AnchorFields, initial: AnchorFields): boolean {
  return anchorKinds.some((kind) => splitAnchorValues(fields[kind.name] ?? "").join(",") !== splitAnchorValues(initial[kind.name] ?? "").join(","));
}

// Les types dont l'ordre est refusé aujourd'hui sur cette machine : le droit
// est coché et l'ancre manque ou ne se lit pas. Vide chez un agent d'avant les
// ancres.
export function blockingAnchors(anchors: AgentAnchors, rights: readonly string[]): AnchorKind[] {
  if (!anchors.reported) {
    return [];
  }
  return anchors.kinds.filter((kind) => kind.state !== "set" && rights.includes(kind.right));
}

// Les raisons d'un ordre refusé par une ancre ont la forme
// « anchor_missing:backup.guest » (aucune posée, illisibles, hors des ancres).
const anchorReasonPattern = /^anchor_(missing|unreadable|outside):([a-z.-]+)$/;

export function anchorOfReason(reason: string): { why: "missing" | "unreadable" | "outside"; name: string } | null {
  const match = anchorReasonPattern.exec(reason);
  if (match === null) {
    return null;
  }
  return { why: match[1] as "missing" | "unreadable" | "outside", name: match[2] ?? "" };
}

// La commande que le serveur joint au refus d'un ordre qu'une ancre ferait
// refuser (le 409 de anchor.*) : à lancer en root sur la machine. null pour tout
// autre refus.
// exact : la commande pose la valeur de l'ordre ; sinon c'est « trust list »,
// et l'écran renvoie à la fiche de la machine.
export interface AnchorFixCommand {
  command: string;
  exact: boolean;
}

export function commandOfRefusal(error: ApiError | string | null | undefined): AnchorFixCommand | null {
  if (error === null || error === undefined || typeof error === "string") {
    return null;
  }
  const payload = error.payload;
  if (typeof payload !== "object" || payload === null || !("command" in payload) || typeof payload.command !== "string" || payload.command === "") {
    return null;
  }
  return { command: payload.command, exact: "command_exact" in payload && payload.command_exact === true };
}
