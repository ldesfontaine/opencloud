import type { Finding } from "./findings";

// Ce que le parcours voit sans que ce soit une panne de l'écran, ou en
// attendant son correctif :
//   expected : voulu, dû aux données fictives (aucun agent n'est connecté).
//   defect   : un défaut trouvé et pas corrigé dans ce chantier ; chaque ligne
//              se retire avec son correctif.
interface KnownFinding {
  kind: "expected" | "defect";
  where: RegExp;
  what: RegExp;
  reason: string;
}

const knownFindings: KnownFinding[] = [
  {
    kind: "expected",
    where: /./,
    what: /(\/logs(\/stream)?|\/actions\/[\w-]+) répond 503$/,
    reason: "la machine fictive n'a pas d'agent connecté : un ordre ou des journaux répondent 503, l'écran le dit",
  },
];

export function knownAs(finding: Finding): KnownFinding | undefined {
  return knownFindings.find((known) => known.where.test(finding.where) && known.what.test(finding.what));
}
