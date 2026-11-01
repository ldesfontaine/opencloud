import type { Finding } from "./findings";

// Une anomalie trouvée par le parcours et pas corrigée dans ce chantier : elle
// attend son correctif. Chaque ligne se retire avec le correctif.
interface KnownFinding {
  where: RegExp;
  what: RegExp;
  reason: string;
}

export const knownFindings: KnownFinding[] = [];

export function isKnown(finding: Finding): boolean {
  return knownFindings.some((known) => known.where.test(finding.where) && known.what.test(finding.what));
}
