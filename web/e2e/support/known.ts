import type { Finding } from "./findings";

// Ce que le parcours voit sans que ce soit une panne de l'écran, ou en
// attendant son correctif :
//   expected : voulu, dû aux données fictives.
//   defect   : un défaut trouvé et pas corrigé dans ce chantier ; chaque ligne
//              se retire avec son correctif.
interface KnownFinding {
  kind: "expected" | "defect";
  where: RegExp;
  what: RegExp;
  reason: string;
}

const knownFindings: KnownFinding[] = [];

export function knownAs(finding: Finding): KnownFinding | undefined {
  return knownFindings.find((known) => known.where.test(finding.where) && known.what.test(finding.what));
}
